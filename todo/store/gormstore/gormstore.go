package gormstore

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"gorm.io/gorm"
)

const maxBulkIDs = 100

var tagRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// GormStore implements store.Store using GORM.
type GormStore struct {
	db        *gorm.DB
	observers []store.StoreObserver
	mu        sync.RWMutex // protects observers slice
	source    string       // "cli", "mcp-stdio", "mcp-http"
}

// New creates a GormStore, runs migrations, and returns it.
func New(db *gorm.DB) (*GormStore, error) {
	if err := db.AutoMigrate(
		&model.Task{},
		&model.TaskBlocker{},
		&model.TaskTag{},
		&model.Link{},
		&model.Note{},
	); err != nil {
		return nil, fmt.Errorf("automigrate: %w", err)
	}
	return &GormStore{db: db, source: "cli"}, nil
}

// SetSource sets the source identifier for audit events.
func (s *GormStore) SetSource(source string) {
	s.source = source
}

// AddObserver registers an observer to receive store events.
func (s *GormStore) AddObserver(o store.StoreObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, o)
}

func (s *GormStore) emit(event store.StoreEvent) {
	event.Source = s.source
	s.mu.RLock()
	observers := s.observers
	s.mu.RUnlock()
	for _, o := range observers {
		o.OnEvent(context.Background(), event)
	}
}

func (s *GormStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// --- Validation helpers ---

func validateID(id uint) error {
	if id == 0 {
		return &model.ValidationError{Field: "id", Message: "must be > 0"}
	}
	return nil
}

func validateTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return &model.ValidationError{Field: "title", Message: "required and non-empty"}
	}
	if len(title) > 500 {
		return &model.ValidationError{Field: "title", Message: "max 500 characters"}
	}
	return nil
}

func validateDescription(desc string) error {
	if len(desc) > 10000 {
		return &model.ValidationError{Field: "description", Message: "max 10000 characters"}
	}
	return nil
}

func validateTag(tag string) error {
	if tag == "" {
		return &model.ValidationError{Field: "tag", Message: "non-empty"}
	}
	if len(tag) > 100 {
		return &model.ValidationError{Field: "tag", Message: "max 100 characters"}
	}
	if !tagRegex.MatchString(tag) {
		return &model.ValidationError{Field: "tag", Message: "alphanumeric, hyphens, underscores only"}
	}
	return nil
}

func validateTags(tags []string) error {
	for _, t := range tags {
		if err := validateTag(t); err != nil {
			return err
		}
	}
	return nil
}

func validateNoteText(text string) error {
	if strings.TrimSpace(text) == "" {
		return &model.ValidationError{Field: "text", Message: "required and non-empty"}
	}
	if len(text) > 50000 {
		return &model.ValidationError{Field: "text", Message: "max 50000 characters"}
	}
	return nil
}

func validateLinkURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return &model.ValidationError{Field: "url", Message: "required and non-empty"}
	}
	if len(url) > 2000 {
		return &model.ValidationError{Field: "url", Message: "max 2000 characters"}
	}
	return nil
}

func validateSearchQuery(q string) error {
	if strings.TrimSpace(q) == "" {
		return &model.ValidationError{Field: "query", Message: "required and non-empty"}
	}
	if len(q) > 500 {
		return &model.ValidationError{Field: "query", Message: "max 500 characters"}
	}
	return nil
}

// --- Task existence helper ---

func (s *GormStore) taskExists(tx *gorm.DB, id uint) (*model.Task, error) {
	var task model.Task
	if err := tx.First(&task, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("task %d: %w", id, model.ErrNotFound)
		}
		return nil, err
	}
	return &task, nil
}

// --- Subtree collection via recursive CTE ---

func (s *GormStore) collectSubtreeIDs(tx *gorm.DB, rootID uint) ([]uint, error) {
	var ids []uint
	err := tx.Raw(`
		WITH RECURSIVE subtree AS (
			SELECT id FROM tasks WHERE id = ?
			UNION ALL
			SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
		) SELECT id FROM subtree
	`, rootID).Scan(&ids).Error
	return ids, err
}

// --- Blocking cycle detection ---

// hasBlockingCycle checks if adding "blockerID blocks taskID" would create a cycle.
// A cycle exists if taskID already transitively blocks blockerID.
func (s *GormStore) hasBlockingCycle(tx *gorm.DB, taskID, blockerID uint) (bool, []uint) {
	// Walk from blockerID upward through its own blockers to see if we reach taskID.
	// "blockerID's blockers" are tasks that block the blocker.
	visited := map[uint]bool{blockerID: true}
	queue := []uint{blockerID}
	path := []uint{blockerID}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Find what blocks `current`
		var blockerIDs []uint
		tx.Model(&model.TaskBlocker{}).
			Where("task_id = ?", current).
			Pluck("blocker_id", &blockerIDs)

		for _, bid := range blockerIDs {
			if bid == taskID {
				return true, append(path, taskID)
			}
			if !visited[bid] {
				visited[bid] = true
				queue = append(queue, bid)
				path = append(path, bid)
			}
		}
	}
	return false, nil
}

// --- Parent cycle detection ---

func (s *GormStore) hasParentCycle(tx *gorm.DB, taskID, parentID uint) (bool, []uint) {
	// Walk from parentID upward to check if taskID is an ancestor.
	current := parentID
	path := []uint{taskID, parentID}
	visited := map[uint]bool{taskID: true, parentID: true}

	for {
		var task model.Task
		if err := tx.Select("parent_id").First(&task, current).Error; err != nil {
			return false, nil
		}
		if task.ParentID == nil {
			return false, nil
		}
		if *task.ParentID == taskID {
			return true, append(path, taskID)
		}
		if visited[*task.ParentID] {
			return true, append(path, *task.ParentID)
		}
		visited[*task.ParentID] = true
		path = append(path, *task.ParentID)
		current = *task.ParentID
	}
}

// --- Priority propagation ---

// propagatePriorityUp adjusts blocker priorities when a blocked task's priority
// becomes more important. Walks up the blocker chain.
func (s *GormStore) propagatePriorityUp(tx *gorm.DB, taskID uint, priority int) error {
	// Find tasks that block this task
	var blockerIDs []uint
	if err := tx.Model(&model.TaskBlocker{}).
		Where("task_id = ?", taskID).
		Pluck("blocker_id", &blockerIDs).Error; err != nil {
		return err
	}

	for _, bid := range blockerIDs {
		var blocker model.Task
		if err := tx.First(&blocker, bid).Error; err != nil {
			continue
		}
		if blocker.Priority > priority {
			if err := tx.Model(&blocker).Update("priority", priority).Error; err != nil {
				return err
			}
			// Cascade further up
			if err := s.propagatePriorityUp(tx, bid, priority); err != nil {
				return err
			}
		}
	}
	return nil
}

// clampBlockerPriority ensures a task's priority is not worse than the best
// priority of tasks it blocks.
func (s *GormStore) clampBlockerPriority(tx *gorm.DB, taskID uint, requestedPriority int) (int, error) {
	// Find tasks blocked by this task
	var minPriority *int
	err := tx.Model(&model.TaskBlocker{}).
		Select("MIN(t.priority)").
		Joins("JOIN tasks t ON t.id = task_blockers.task_id").
		Where("task_blockers.blocker_id = ?", taskID).
		Scan(&minPriority).Error
	if err != nil {
		return requestedPriority, err
	}
	if minPriority != nil && requestedPriority > *minPriority {
		return *minPriority, nil
	}
	return requestedPriority, nil
}

// --- External blocker check ---

// checkExternalBlockers returns an error if any task in the set blocks a task outside the set.
func (s *GormStore) checkExternalBlockers(tx *gorm.DB, taskIDs []uint) error {
	if len(taskIDs) == 0 {
		return nil
	}
	idSet := make(map[uint]bool, len(taskIDs))
	for _, id := range taskIDs {
		idSet[id] = true
	}

	// Find all tasks blocked by any task in the set
	var blockers []model.TaskBlocker
	if err := tx.Where("blocker_id IN ?", taskIDs).Find(&blockers).Error; err != nil {
		return err
	}

	for _, b := range blockers {
		if !idSet[b.TaskID] {
			return &model.BlockingExternalError{
				BlockingTaskID: b.BlockerID,
				BlockedTaskID:  b.TaskID,
			}
		}
	}
	return nil
}

// --- CRUD: Tasks ---

func (s *GormStore) CreateTask(title, description string, priority int, dueAt *time.Time, tags []string) (*model.Task, error) {
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	if err := validateDescription(description); err != nil {
		return nil, err
	}
	if err := validateTags(tags); err != nil {
		return nil, err
	}

	task := model.Task{
		Title:       title,
		Description: description,
		Priority:    priority,
		State:       model.StateNew,
		DueAt:       dueAt,
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		for _, tag := range tags {
			tt := model.TaskTag{TaskID: task.ID, Tag: tag}
			if err := tx.Create(&tt).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Reload with tags
	s.db.Preload("Tags").First(&task, task.ID)

	s.emit(store.StoreEvent{
		Type:    "task.created",
		TaskIDs: []uint{task.ID},
	})

	return &task, nil
}

func (s *GormStore) GetTask(id uint) (*model.TaskDetail, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}

	var task model.Task
	err := s.db.
		Preload("Notes").
		Preload("Blockers").
		Preload("Tags").
		Preload("Links").
		Preload("Children").
		Preload("Parent").
		First(&task, id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("task %d: %w", id, model.ErrNotFound)
		}
		return nil, err
	}

	// Compute blocking list (tasks this one blocks)
	var blocking []model.Task
	var blockedTaskIDs []uint
	s.db.Model(&model.TaskBlocker{}).
		Where("blocker_id = ?", id).
		Pluck("task_id", &blockedTaskIDs)
	if len(blockedTaskIDs) > 0 {
		s.db.Where("id IN ?", blockedTaskIDs).Find(&blocking)
	}

	return &model.TaskDetail{
		Task:     task,
		Blocking: blocking,
	}, nil
}

func (s *GormStore) ListTasks(opts store.ListTasksOptions) ([]model.Task, error) {
	q := s.db.Model(&model.Task{}).Preload("Tags")

	// ParentID implies IncludeSubtasks
	if opts.ParentID != nil {
		if err := validateID(*opts.ParentID); err != nil {
			return nil, err
		}
		subtreeIDs, err := s.collectSubtreeIDs(s.db, *opts.ParentID)
		if err != nil {
			return nil, err
		}
		q = q.Where("id IN ?", subtreeIDs)
	} else if !opts.IncludeSubtasks {
		q = q.Where("parent_id IS NULL")
	}

	if !opts.IncludeArchived {
		q = q.Where("archived = ?", false)
	}
	if opts.State != nil {
		q = q.Where("state = ?", *opts.State)
	}
	if opts.Overdue {
		q = q.Where("due_at IS NOT NULL AND due_at < ?", time.Now().UTC())
	}

	// Tag filter (AND logic)
	for _, tag := range opts.Tags {
		q = q.Where("id IN (SELECT task_id FROM task_tags WHERE tag = ?)", tag)
	}

	// Sort
	switch opts.SortBy {
	case "due":
		q = q.Order("due_at ASC NULLS LAST, priority ASC")
	case "created":
		q = q.Order("created_at DESC")
	case "updated":
		q = q.Order("updated_at DESC")
	default: // "priority"
		q = q.Order("priority ASC, created_at DESC")
	}

	var tasks []model.Task
	if err := q.Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *GormStore) UpdateTask(id uint, opts store.UpdateTaskOptions) (*model.Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}

	var task model.Task
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&task, id).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("task %d: %w", id, model.ErrNotFound)
			}
			return err
		}

		updates := map[string]any{}
		changes := map[string]store.Change{}

		if opts.Title != nil {
			if err := validateTitle(*opts.Title); err != nil {
				return err
			}
			changes["title"] = store.Change{Old: task.Title, New: *opts.Title}
			updates["title"] = *opts.Title
		}
		if opts.Description != nil {
			if err := validateDescription(*opts.Description); err != nil {
				return err
			}
			changes["description"] = store.Change{Old: task.Description, New: *opts.Description}
			updates["description"] = *opts.Description
		}
		if opts.Priority != nil {
			newPriority := *opts.Priority
			// Clamp if this task blocks others
			clamped, err := s.clampBlockerPriority(tx, id, newPriority)
			if err != nil {
				return err
			}
			changes["priority"] = store.Change{Old: task.Priority, New: clamped}
			updates["priority"] = clamped

			// Propagate up if this task is blocked
			if clamped < task.Priority {
				if err := s.propagatePriorityUp(tx, id, clamped); err != nil {
					return err
				}
			}
		}
		if opts.ClearDueAt {
			changes["due_at"] = store.Change{Old: task.DueAt, New: nil}
			updates["due_at"] = nil
		} else if opts.DueAt != nil {
			utc := opts.DueAt.UTC()
			changes["due_at"] = store.Change{Old: task.DueAt, New: utc}
			updates["due_at"] = utc
		}

		if len(updates) == 0 {
			return nil // no changes
		}

		if err := tx.Model(&task).Updates(updates).Error; err != nil {
			return err
		}

		// Reload
		return tx.First(&task, id).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(store.StoreEvent{
		Type:    "task.updated",
		TaskIDs: []uint{id},
	})

	return &task, nil
}

func (s *GormStore) SetTaskState(id uint, state model.TaskState) (*model.Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	if state == model.StateBlocked {
		return nil, fmt.Errorf("use AddBlockers to set Blocked state: %w", model.ErrInvalidState)
	}
	if !model.ValidTaskStates[state] {
		return nil, &model.ValidationError{Field: "state", Message: fmt.Sprintf("invalid state: %s", state)}
	}

	var task model.Task
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&task, id).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("task %d: %w", id, model.ErrNotFound)
			}
			return err
		}
		if task.Archived {
			return model.ErrArchived
		}

		oldState := task.State

		// Clear blocker entries for this task
		if err := tx.Where("task_id = ?", id).Delete(&model.TaskBlocker{}).Error; err != nil {
			return err
		}

		// If Done: remove this task from other tasks' blockers and auto-unblock
		if state == model.StateDone {
			// Find tasks blocked by this one
			var blockedTaskIDs []uint
			if err := tx.Model(&model.TaskBlocker{}).
				Where("blocker_id = ?", id).
				Pluck("task_id", &blockedTaskIDs).Error; err != nil {
				return err
			}

			// Remove this task as a blocker
			if err := tx.Where("blocker_id = ?", id).Delete(&model.TaskBlocker{}).Error; err != nil {
				return err
			}

			// Auto-unblock tasks with zero remaining blockers
			for _, btid := range blockedTaskIDs {
				var count int64
				tx.Model(&model.TaskBlocker{}).Where("task_id = ?", btid).Count(&count)
				if count == 0 {
					tx.Model(&model.Task{}).Where("id = ? AND state = ?", btid, model.StateBlocked).
						Update("state", model.StateUnblocked)
				}
			}
		}

		task.State = state
		if err := tx.Model(&task).Update("state", state).Error; err != nil {
			return err
		}

		_ = oldState // used for event
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.emit(store.StoreEvent{
		Type:    "task.state_changed",
		TaskIDs: []uint{id},
		Changes: map[string]store.Change{"state": {Old: string(task.State), New: string(state)}},
	})

	s.db.First(&task, id) // reload
	return &task, nil
}

func (s *GormStore) AddBlockers(taskID uint, blockerIDs []uint) (*model.Task, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}

	var task model.Task
	err := s.db.Transaction(func(tx *gorm.DB) error {
		t, err := s.taskExists(tx, taskID)
		if err != nil {
			return err
		}
		task = *t
		if task.Archived {
			return model.ErrArchived
		}

		for _, bid := range blockerIDs {
			if err := validateID(bid); err != nil {
				return err
			}
			if bid == taskID {
				return &model.ValidationError{Field: "blocker_id", Message: "cannot block self"}
			}

			blocker, err := s.taskExists(tx, bid)
			if err != nil {
				return fmt.Errorf("blocker %d: %w", bid, model.ErrNotFound)
			}
			if blocker.State == model.StateDone {
				return &model.ValidationError{Field: "blocker_id", Message: fmt.Sprintf("task %d is Done", bid)}
			}
			if blocker.Archived {
				return &model.ValidationError{Field: "blocker_id", Message: fmt.Sprintf("task %d is archived", bid)}
			}

			// Cycle detection
			if hasCycle, path := s.hasBlockingCycle(tx, taskID, bid); hasCycle {
				return &model.CycleDetectedError{Path: path}
			}

			// Insert (idempotent)
			tb := model.TaskBlocker{TaskID: taskID, BlockerID: bid}
			if err := tx.Where(tb).FirstOrCreate(&tb).Error; err != nil {
				return err
			}

			// Adjust blocker priority
			if blocker.Priority > task.Priority {
				if err := tx.Model(blocker).Update("priority", task.Priority).Error; err != nil {
					return err
				}
				if err := s.propagatePriorityUp(tx, bid, task.Priority); err != nil {
					return err
				}
			}
		}

		// Set state to Blocked
		return tx.Model(&task).Update("state", model.StateBlocked).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(store.StoreEvent{
		Type:    "task.blockers_added",
		TaskIDs: []uint{taskID},
	})

	s.db.Preload("Blockers").First(&task, taskID)
	return &task, nil
}

func (s *GormStore) RemoveBlockers(taskID uint, blockerIDs []uint) (*model.Task, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}

	var task model.Task
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, taskID); err != nil {
			return err
		}

		for _, bid := range blockerIDs {
			tx.Where("task_id = ? AND blocker_id = ?", taskID, bid).Delete(&model.TaskBlocker{})
		}

		// Check remaining blockers
		var count int64
		tx.Model(&model.TaskBlocker{}).Where("task_id = ?", taskID).Count(&count)
		if count == 0 {
			// Auto-transition to Unblocked if currently Blocked
			tx.Model(&model.Task{}).Where("id = ? AND state = ?", taskID, model.StateBlocked).
				Update("state", model.StateUnblocked)
		}

		return tx.First(&task, taskID).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(store.StoreEvent{
		Type:    "task.blockers_removed",
		TaskIDs: []uint{taskID},
	})

	return &task, nil
}

func (s *GormStore) SetParent(id uint, parentID *uint) error {
	if err := validateID(id); err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, id); err != nil {
			return err
		}

		if parentID == nil {
			return tx.Model(&model.Task{}).Where("id = ?", id).Update("parent_id", nil).Error
		}

		if err := validateID(*parentID); err != nil {
			return err
		}
		if *parentID == id {
			return &model.ValidationError{Field: "parent_id", Message: "cannot be own parent"}
		}
		if _, err := s.taskExists(tx, *parentID); err != nil {
			return err
		}

		if hasCycle, path := s.hasParentCycle(tx, id, *parentID); hasCycle {
			return &model.CycleDetectedError{Path: path}
		}

		return tx.Model(&model.Task{}).Where("id = ?", id).Update("parent_id", *parentID).Error
	})
}

func (s *GormStore) ArchiveTask(id uint, archived bool) error {
	if err := validateID(id); err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		subtreeIDs, err := s.collectSubtreeIDs(tx, id)
		if err != nil {
			return err
		}

		if archived {
			// Check external blockers
			if err := s.checkExternalBlockers(tx, subtreeIDs); err != nil {
				return err
			}
		} else {
			// On unarchive, validate preserved blocker relationships
			for _, tid := range subtreeIDs {
				var blockerIDs []uint
				tx.Model(&model.TaskBlocker{}).Where("task_id = ?", tid).Pluck("blocker_id", &blockerIDs)
				for _, bid := range blockerIDs {
					var blocker model.Task
					if err := tx.First(&blocker, bid).Error; err != nil {
						// Blocker no longer exists — clean up
						tx.Where("task_id = ? AND blocker_id = ?", tid, bid).Delete(&model.TaskBlocker{})
						continue
					}
					if blocker.State == model.StateDone {
						// Blocker is Done — clean up
						tx.Where("task_id = ? AND blocker_id = ?", tid, bid).Delete(&model.TaskBlocker{})
					}
				}
				// If task was Blocked and has no more blockers, transition to Unblocked
				var remaining int64
				tx.Model(&model.TaskBlocker{}).Where("task_id = ?", tid).Count(&remaining)
				if remaining == 0 {
					tx.Model(&model.Task{}).Where("id = ? AND state = ?", tid, model.StateBlocked).
						Update("state", model.StateUnblocked)
				}
			}
		}

		return tx.Model(&model.Task{}).Where("id IN ?", subtreeIDs).Update("archived", archived).Error
	})
}

func (s *GormStore) DeleteTask(id uint, recursive bool) error {
	if err := validateID(id); err != nil {
		return err
	}

	var deletedIDs []uint

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, id); err != nil {
			return err
		}

		if recursive {
			subtreeIDs, err := s.collectSubtreeIDs(tx, id)
			if err != nil {
				return err
			}

			// Check external blockers for the entire subtree
			if err := s.checkExternalBlockers(tx, subtreeIDs); err != nil {
				return err
			}

			// Delete all related data for the subtree
			for _, tid := range subtreeIDs {
				s.deleteTaskData(tx, tid)
			}

			deletedIDs = subtreeIDs
			return tx.Where("id IN ?", subtreeIDs).Delete(&model.Task{}).Error
		}

		// Non-recursive: check only the task itself
		if err := s.checkExternalBlockers(tx, []uint{id}); err != nil {
			return err
		}

		// Promote children
		if err := tx.Model(&model.Task{}).Where("parent_id = ?", id).Update("parent_id", nil).Error; err != nil {
			return err
		}

		// Find tasks blocked by this task (before we delete blocker entries)
		var blockedByMe []uint
		tx.Model(&model.TaskBlocker{}).Where("blocker_id = ?", id).Pluck("task_id", &blockedByMe)

		// Delete task data
		s.deleteTaskData(tx, id)

		if err := tx.Delete(&model.Task{}, id).Error; err != nil {
			return err
		}

		// Auto-unblock tasks that lost their last blocker
		for _, btid := range blockedByMe {
			var count int64
			tx.Model(&model.TaskBlocker{}).Where("task_id = ?", btid).Count(&count)
			if count == 0 {
				tx.Model(&model.Task{}).Where("id = ? AND state = ?", btid, model.StateBlocked).
					Update("state", model.StateUnblocked)
			}
		}

		deletedIDs = []uint{id}
		return nil
	})
	if err != nil {
		return err
	}

	s.emit(store.StoreEvent{
		Type:    "task.deleted",
		TaskIDs: deletedIDs,
	})

	return nil
}

// deleteTaskData removes all associated data for a single task (not the task itself).
func (s *GormStore) deleteTaskData(tx *gorm.DB, taskID uint) {
	tx.Where("task_id = ?", taskID).Delete(&model.Note{})
	tx.Where("task_id = ?", taskID).Delete(&model.Link{})
	tx.Where("task_id = ?", taskID).Delete(&model.TaskTag{})
	tx.Where("task_id = ? OR blocker_id = ?", taskID, taskID).Delete(&model.TaskBlocker{})
}

// --- Search ---

func (s *GormStore) SearchTasks(query string) ([]model.Task, error) {
	if err := validateSearchQuery(query); err != nil {
		return nil, err
	}
	pattern := "%" + strings.ToLower(query) + "%"
	var tasks []model.Task
	err := s.db.Where("LOWER(title) LIKE ? OR LOWER(description) LIKE ?", pattern, pattern).
		Order("priority ASC").
		Find(&tasks).Error
	return tasks, err
}

func (s *GormStore) SearchNotes(query string) ([]model.Note, error) {
	if err := validateSearchQuery(query); err != nil {
		return nil, err
	}
	pattern := "%" + strings.ToLower(query) + "%"
	var notes []model.Note
	err := s.db.Where("LOWER(text) LIKE ?", pattern).Find(&notes).Error
	return notes, err
}

// --- Bulk operations ---

func (s *GormStore) BulkUpdateState(ids []uint, state model.TaskState) ([]model.Task, error) {
	if len(ids) > maxBulkIDs {
		return nil, &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}
	if state == model.StateBlocked {
		return nil, fmt.Errorf("use AddBlockers for Blocked state: %w", model.ErrInvalidState)
	}

	// Process in ascending ID order
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var results []model.Task
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := validateID(id); err != nil {
				return err
			}
			var task model.Task
			if err := tx.First(&task, id).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return fmt.Errorf("task %d: %w", id, model.ErrNotFound)
				}
				return err
			}
			if task.Archived {
				return fmt.Errorf("task %d: %w", id, model.ErrArchived)
			}

			// Clear blockers
			tx.Where("task_id = ?", id).Delete(&model.TaskBlocker{})

			// Done cascade
			if state == model.StateDone {
				var blockedTaskIDs []uint
				tx.Model(&model.TaskBlocker{}).Where("blocker_id = ?", id).Pluck("task_id", &blockedTaskIDs)
				tx.Where("blocker_id = ?", id).Delete(&model.TaskBlocker{})
				for _, btid := range blockedTaskIDs {
					var count int64
					tx.Model(&model.TaskBlocker{}).Where("task_id = ?", btid).Count(&count)
					if count == 0 {
						tx.Model(&model.Task{}).Where("id = ? AND state = ?", btid, model.StateBlocked).
							Update("state", model.StateUnblocked)
					}
				}
			}

			tx.Model(&task).Update("state", state)
			tx.First(&task, id)
			results = append(results, task)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.emit(store.StoreEvent{
		Type:    "task.bulk_state_changed",
		TaskIDs: ids,
	})

	return results, nil
}

func (s *GormStore) BulkUpdatePriority(ids []uint, priority int) ([]model.Task, error) {
	if len(ids) > maxBulkIDs {
		return nil, &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}

	var results []model.Task
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := validateID(id); err != nil {
				return err
			}
			var task model.Task
			if err := tx.First(&task, id).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return fmt.Errorf("task %d: %w", id, model.ErrNotFound)
				}
				return err
			}

			clamped, err := s.clampBlockerPriority(tx, id, priority)
			if err != nil {
				return err
			}
			tx.Model(&task).Update("priority", clamped)
			if clamped < task.Priority {
				s.propagatePriorityUp(tx, id, clamped)
			}
			tx.First(&task, id)
			results = append(results, task)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.emit(store.StoreEvent{
		Type:    "task.bulk_priority_changed",
		TaskIDs: ids,
	})

	return results, nil
}

func (s *GormStore) BulkAddTags(ids []uint, tags []string) error {
	if len(ids) > maxBulkIDs {
		return &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}
	if err := validateTags(tags); err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := validateID(id); err != nil {
				return err
			}
			if _, err := s.taskExists(tx, id); err != nil {
				return err
			}
			for _, tag := range tags {
				tt := model.TaskTag{TaskID: id, Tag: tag}
				tx.Where(tt).FirstOrCreate(&tt)
			}
		}
		return nil
	})
}

func (s *GormStore) BulkRemoveTags(ids []uint, tags []string) error {
	if len(ids) > maxBulkIDs {
		return &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := validateID(id); err != nil {
				return err
			}
			for _, tag := range tags {
				tx.Where("task_id = ? AND tag = ?", id, tag).Delete(&model.TaskTag{})
			}
		}
		return nil
	})
}

// --- Tags ---

func (s *GormStore) AddTags(taskID uint, tags []string) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	if err := validateTags(tags); err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, taskID); err != nil {
			return err
		}

		// Check tag count
		var existing int64
		tx.Model(&model.TaskTag{}).Where("task_id = ?", taskID).Count(&existing)
		if int(existing)+len(tags) > 50 {
			return &model.ValidationError{Field: "tags", Message: "max 50 tags per task"}
		}

		for _, tag := range tags {
			tt := model.TaskTag{TaskID: taskID, Tag: tag}
			tx.Where(tt).FirstOrCreate(&tt)
		}
		return nil
	})
}

func (s *GormStore) RemoveTags(taskID uint, tags []string) error {
	if err := validateID(taskID); err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, tag := range tags {
			tx.Where("task_id = ? AND tag = ?", taskID, tag).Delete(&model.TaskTag{})
		}
		return nil
	})
}

// --- Links ---

func (s *GormStore) AddLink(taskID uint, linkType model.LinkType, url string) (*model.Link, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	if !model.ValidLinkTypes[linkType] {
		return nil, &model.ValidationError{Field: "type", Message: fmt.Sprintf("invalid link type: %s", linkType)}
	}
	if err := validateLinkURL(url); err != nil {
		return nil, err
	}

	link := model.Link{TaskID: taskID, Type: linkType, URL: url}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, taskID); err != nil {
			return err
		}
		return tx.Create(&link).Error
	})
	if err != nil {
		return nil, err
	}
	return &link, nil
}

func (s *GormStore) ListLinks(taskID uint) ([]model.Link, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	var links []model.Link
	err := s.db.Where("task_id = ?", taskID).Find(&links).Error
	return links, err
}

func (s *GormStore) DeleteLink(taskID uint, linkID uint) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	if err := validateID(linkID); err != nil {
		return err
	}

	result := s.db.Where("id = ? AND task_id = ?", linkID, taskID).Delete(&model.Link{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("link %d for task %d: %w", linkID, taskID, model.ErrNotFound)
	}
	return result.Error
}

// --- Notes ---

func (s *GormStore) AddNote(taskID uint, text string) (*model.Note, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	if err := validateNoteText(text); err != nil {
		return nil, err
	}

	note := model.Note{TaskID: taskID, Text: text}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, taskID); err != nil {
			return err
		}
		return tx.Create(&note).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(store.StoreEvent{
		Type:    "note.created",
		TaskIDs: []uint{taskID},
		NoteIDs: []uint{note.ID},
	})

	return &note, nil
}

func (s *GormStore) UpdateNote(taskID uint, noteID uint, text string) (*model.Note, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	if err := validateID(noteID); err != nil {
		return nil, err
	}
	if err := validateNoteText(text); err != nil {
		return nil, err
	}

	var note model.Note
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND task_id = ?", noteID, taskID).First(&note).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("note %d for task %d: %w", noteID, taskID, model.ErrNotFound)
			}
			return err
		}
		return tx.Model(&note).Update("text", text).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(store.StoreEvent{
		Type:    "note.updated",
		TaskIDs: []uint{taskID},
		NoteIDs: []uint{noteID},
	})

	return &note, nil
}

func (s *GormStore) ListNotes(taskID uint) ([]model.Note, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	var notes []model.Note
	err := s.db.Where("task_id = ?", taskID).Order("created_at ASC").Find(&notes).Error
	return notes, err
}

func (s *GormStore) DeleteNote(taskID uint, noteID uint) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	if err := validateID(noteID); err != nil {
		return err
	}

	result := s.db.Where("id = ? AND task_id = ?", noteID, taskID).Delete(&model.Note{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("note %d for task %d: %w", noteID, taskID, model.ErrNotFound)
	}

	s.emit(store.StoreEvent{
		Type:    "note.deleted",
		TaskIDs: []uint{taskID},
		NoteIDs: []uint{noteID},
	})

	return result.Error
}

// Compile-time interface check
var _ store.Store = (*GormStore)(nil)
