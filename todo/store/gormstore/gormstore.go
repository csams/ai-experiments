package gormstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"gorm.io/gorm"
)

const maxBulkIDs       = 100
const defaultQueryLimit = 200

var tagRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// GormStore implements store.Store using GORM.
type GormStore struct {
	db        *gorm.DB
	observers []store.StoreObserver
	mu        sync.RWMutex // protects observers slice
	source    string       // "cli", "mcp-stdio", "mcp-http"
	syncEmit  bool         // if true, call observers synchronously (for tests)
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
// Called from cmd/mcp.go to tag events with the transport type.
func (s *GormStore) SetSource(source string) {
	s.source = source
}

// SetSyncEmit controls whether observer callbacks are called synchronously.
// When true (used in tests), observers are called inline; when false (default,
// used in production), observers run in goroutines with timeout and panic recovery.
func (s *GormStore) SetSyncEmit(sync bool) {
	s.syncEmit = sync
}

// AddObserver registers an observer to receive store events.
func (s *GormStore) AddObserver(o store.StoreObserver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, o)
}

// DB returns the underlying *gorm.DB handle. Used by pgvector to share the
// database connection.
func (s *GormStore) DB() *gorm.DB {
	return s.db
}

func (s *GormStore) emit(ctx context.Context, event store.StoreEvent) {
	event.Source = s.source
	s.mu.RLock()
	observers := s.observers
	s.mu.RUnlock()
	for _, o := range observers {
		if s.syncEmit {
			o.OnEvent(ctx, event)
		} else {
			go func(obs store.StoreObserver) {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("observer panic", "panic", r, "stack", string(debug.Stack()))
					}
				}()
				obsCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				obs.OnEvent(obsCtx, event)
			}(o)
		}
	}
}

func (s *GormStore) Close(ctx context.Context) error {
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
	if len(title) > 512 {
		return &model.ValidationError{Field: "title", Message: "max 512 characters"}
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

// --- LIKE wildcard escaping ---

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// --- Task existence helper ---

func (s *GormStore) taskExists(tx *gorm.DB, id uint) (*model.Task, error) {
	var task model.Task
	if err := tx.First(&task, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("task %d: %w", id, model.ErrNotFound)
		}
		return nil, err
	}
	return &task, nil
}

func (s *GormStore) taskExistsActive(tx *gorm.DB, id uint) (*model.Task, error) {
	task, err := s.taskExists(tx, id)
	if err != nil {
		return nil, err
	}
	if task.Archived {
		return nil, fmt.Errorf("task %d: %w", id, model.ErrArchived)
	}
	return task, nil
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
func (s *GormStore) hasBlockingCycle(tx *gorm.DB, taskID, blockerID uint) (bool, []uint, error) {
	// Walk from blockerID upward through its own blockers to see if we reach taskID.
	// "blockerID's blockers" are tasks that block the blocker.
	visited := map[uint]bool{}
	parent := map[uint]uint{}
	queue := []uint{blockerID}
	visited[blockerID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Find what blocks `current`
		var upstreamBlockers []model.TaskBlocker
		if err := tx.Where("task_id = ?", current).Find(&upstreamBlockers).Error; err != nil {
			return false, nil, fmt.Errorf("cycle detection query: %w", err)
		}
		for _, b := range upstreamBlockers {
			if b.BlockerID == taskID {
				// Found cycle. Reconstruct path from blockerID to current via parent map,
				// then append taskID to close the cycle.
				var path []uint
				at := current
				for at != blockerID {
					path = append([]uint{at}, path...)
					at = parent[at]
				}
				path = append([]uint{blockerID}, path...)
				// The cycle: taskID -> blockerID -> ... -> current -> taskID
				path = append([]uint{taskID}, path...)
				path = append(path, taskID)
				return true, path, nil
			}
			if !visited[b.BlockerID] {
				visited[b.BlockerID] = true
				parent[b.BlockerID] = current
				queue = append(queue, b.BlockerID)
			}
		}
	}
	return false, nil, nil
}

// --- Parent cycle detection ---

func (s *GormStore) hasParentCycle(tx *gorm.DB, taskID, parentID uint) (bool, []uint, error) {
	// Walk from parentID upward to check if taskID is an ancestor.
	current := parentID
	path := []uint{taskID, parentID}
	visited := map[uint]bool{taskID: true, parentID: true}

	for {
		var task model.Task
		if err := tx.Select("parent_id").First(&task, current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil, nil
			}
			return false, nil, fmt.Errorf("cycle detection query: %w", err)
		}
		if task.ParentID == nil {
			return false, nil, nil
		}
		if *task.ParentID == taskID {
			return true, append(path, taskID), nil
		}
		if visited[*task.ParentID] {
			return true, append(path, *task.ParentID), nil
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
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
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

func (s *GormStore) CreateTask(ctx context.Context, title, description string, priority int, dueAt *time.Time, tags []string) (*model.Task, error) {
	if err := validateTitle(title); err != nil {
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

	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
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
	if err := db.Preload("Tags").First(&task, task.ID).Error; err != nil {
		return nil, fmt.Errorf("reload task %d: %w", task.ID, err)
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.created",
		TaskIDs: []uint{task.ID},
	})

	return &task, nil
}

func (s *GormStore) CreateSubtask(ctx context.Context, parentID uint, title, description string, priority int, dueAt *time.Time, tags []string) (*model.Task, error) {
	if err := validateID(parentID); err != nil {
		return nil, err
	}
	if err := validateTitle(title); err != nil {
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
		ParentID:    &parentID,
	}

	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExistsActive(tx, parentID); err != nil {
			return err
		}
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
	if err := db.Preload("Tags").First(&task, task.ID).Error; err != nil {
		return nil, fmt.Errorf("reload task %d: %w", task.ID, err)
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.created",
		TaskIDs: []uint{task.ID},
	})

	return &task, nil
}

func (s *GormStore) GetTask(ctx context.Context, id uint) (*model.TaskDetail, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	var task model.Task
	err := db.
		Preload("Notes").
		Preload("Blockers").
		Preload("Tags").
		Preload("Links").
		Preload("Children").
		Preload("Parent").
		First(&task, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("task %d: %w", id, model.ErrNotFound)
		}
		return nil, err
	}

	// Compute blocking list (tasks this one blocks)
	var blocking []model.Task
	var blockedTaskIDs []uint
	if err := db.Model(&model.TaskBlocker{}).
		Where("blocker_id = ?", id).
		Pluck("task_id", &blockedTaskIDs).Error; err != nil {
		return nil, err
	}
	if len(blockedTaskIDs) > 0 {
		if err := db.Where("id IN ?", blockedTaskIDs).Find(&blocking).Error; err != nil {
			return nil, err
		}
	}

	return &model.TaskDetail{
		Task:     task,
		Blocking: blocking,
	}, nil
}

func (s *GormStore) ListTasks(ctx context.Context, opts store.ListTasksOptions) ([]model.Task, error) {
	db := s.db.WithContext(ctx)
	q := db.Model(&model.Task{}).Preload("Tags")

	// ParentID implies IncludeSubtasks
	if opts.ParentID != nil {
		if err := validateID(*opts.ParentID); err != nil {
			return nil, err
		}
		subtreeIDs, err := s.collectSubtreeIDs(db, *opts.ParentID)
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

	// Pagination
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	q = q.Limit(limit)
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}

	var tasks []model.Task
	if err := q.Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *GormStore) UpdateTask(ctx context.Context, id uint, opts store.UpdateTaskOptions) (*model.Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	var task model.Task
	var changes map[string]store.Change
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&task, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("task %d: %w", id, model.ErrNotFound)
			}
			return err
		}
		if task.Archived {
			return model.ErrArchived
		}

		updates := map[string]any{}
		changes = map[string]store.Change{}

		if opts.Title != nil {
			if err := validateTitle(*opts.Title); err != nil {
				return err
			}
			changes["title"] = store.Change{Old: task.Title, New: *opts.Title}
			updates["title"] = *opts.Title
		}
		if opts.Description != nil {
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

	if len(changes) > 0 {
		s.emit(ctx, store.StoreEvent{
			Type:    "task.updated",
			TaskIDs: []uint{id},
			Changes: changes,
		})
	}

	return &task, nil
}

func (s *GormStore) SetTaskState(ctx context.Context, id uint, state model.TaskState) (*model.Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	if state == model.StateBlocked {
		return nil, fmt.Errorf("use AddBlockers to set Blocked state: %w", model.ErrInvalidState)
	}
	if !model.ValidTaskStates[state] {
		return nil, &model.ValidationError{Field: "state", Message: fmt.Sprintf("invalid state: %s", state)}
	}

	db := s.db.WithContext(ctx)
	var task model.Task
	var oldState model.TaskState
	affectedIDs := []uint{id}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&task, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("task %d: %w", id, model.ErrNotFound)
			}
			return err
		}
		if task.Archived {
			return model.ErrArchived
		}

		oldState = task.State

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
				if err := tx.Model(&model.TaskBlocker{}).Where("task_id = ?", btid).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					if err := tx.Model(&model.Task{}).Where("id = ? AND state = ?", btid, model.StateBlocked).
						Update("state", model.StateUnblocked).Error; err != nil {
						return err
					}
					affectedIDs = append(affectedIDs, btid)
				}
			}
		}

		task.State = state
		if err := tx.Model(&task).Update("state", state).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.state_changed",
		TaskIDs: affectedIDs,
		Changes: map[string]store.Change{"state": {Old: string(oldState), New: string(state)}},
	})

	if err := db.First(&task, id).Error; err != nil {
		return nil, fmt.Errorf("reload task %d: %w", id, err)
	}
	return &task, nil
}

func (s *GormStore) AddBlockers(ctx context.Context, taskID uint, blockerIDs []uint) (*model.Task, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	var task model.Task
	err := db.Transaction(func(tx *gorm.DB) error {
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
			hasCycle, path, err := s.hasBlockingCycle(tx, taskID, bid)
			if err != nil {
				return err
			}
			if hasCycle {
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

	s.emit(ctx, store.StoreEvent{
		Type:    "task.blockers_added",
		TaskIDs: []uint{taskID},
	})

	if err := db.Preload("Blockers").First(&task, taskID).Error; err != nil {
		return nil, fmt.Errorf("reload task %d: %w", taskID, err)
	}
	return &task, nil
}

func (s *GormStore) RemoveBlockers(ctx context.Context, taskID uint, blockerIDs []uint) (*model.Task, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	var task model.Task
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, taskID); err != nil {
			return err
		}

		for _, bid := range blockerIDs {
			if err := tx.Where("task_id = ? AND blocker_id = ?", taskID, bid).Delete(&model.TaskBlocker{}).Error; err != nil {
				return err
			}
		}

		// Check remaining blockers
		var count int64
		if err := tx.Model(&model.TaskBlocker{}).Where("task_id = ?", taskID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			// Auto-transition to Unblocked if currently Blocked
			if err := tx.Model(&model.Task{}).Where("id = ? AND state = ?", taskID, model.StateBlocked).
				Update("state", model.StateUnblocked).Error; err != nil {
				return err
			}
		}

		return tx.First(&task, taskID).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.blockers_removed",
		TaskIDs: []uint{taskID},
	})

	return &task, nil
}

func (s *GormStore) SetParent(ctx context.Context, id uint, parentID *uint) error {
	if err := validateID(id); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
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

		hasCycle, path, err := s.hasParentCycle(tx, id, *parentID)
		if err != nil {
			return err
		}
		if hasCycle {
			return &model.CycleDetectedError{Path: path}
		}

		return tx.Model(&model.Task{}).Where("id = ?", id).Update("parent_id", *parentID).Error
	})
	if err != nil {
		return err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.parent_changed",
		TaskIDs: []uint{id},
	})

	return nil
}

func (s *GormStore) ArchiveTask(ctx context.Context, id uint, archived bool) error {
	if err := validateID(id); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	var subtreeIDs []uint
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		subtreeIDs, err = s.collectSubtreeIDs(tx, id)
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
				if err := tx.Model(&model.TaskBlocker{}).Where("task_id = ?", tid).Pluck("blocker_id", &blockerIDs).Error; err != nil {
					return err
				}
				for _, bid := range blockerIDs {
					var blocker model.Task
					if err := tx.First(&blocker, bid).Error; err != nil {
						// Blocker no longer exists — clean up
						if err := tx.Where("task_id = ? AND blocker_id = ?", tid, bid).Delete(&model.TaskBlocker{}).Error; err != nil {
							return err
						}
						continue
					}
					if blocker.State == model.StateDone || blocker.Archived {
						// Blocker is Done or Archived — clean up
						if err := tx.Where("task_id = ? AND blocker_id = ?", tid, bid).Delete(&model.TaskBlocker{}).Error; err != nil {
							return err
						}
					}
				}
				// If task was Blocked and has no more blockers, transition to Unblocked
				var remaining int64
				if err := tx.Model(&model.TaskBlocker{}).Where("task_id = ?", tid).Count(&remaining).Error; err != nil {
					return err
				}
				if remaining == 0 {
					if err := tx.Model(&model.Task{}).Where("id = ? AND state = ?", tid, model.StateBlocked).
						Update("state", model.StateUnblocked).Error; err != nil {
						return err
					}
				}
			}
		}

		return tx.Model(&model.Task{}).Where("id IN ?", subtreeIDs).Update("archived", archived).Error
	})
	if err != nil {
		return err
	}

	eventType := "task.archived"
	if !archived {
		eventType = "task.unarchived"
	}
	s.emit(ctx, store.StoreEvent{
		Type:    eventType,
		TaskIDs: subtreeIDs,
	})

	return nil
}

func (s *GormStore) DeleteTask(ctx context.Context, id uint, recursive bool) error {
	if err := validateID(id); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	var deletedIDs []uint

	err := db.Transaction(func(tx *gorm.DB) error {
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
				if err := s.deleteTaskData(tx, tid); err != nil {
					return err
				}
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
		if err := tx.Model(&model.TaskBlocker{}).Where("blocker_id = ?", id).Pluck("task_id", &blockedByMe).Error; err != nil {
			return err
		}

		// Delete task data
		if err := s.deleteTaskData(tx, id); err != nil {
			return err
		}

		if err := tx.Delete(&model.Task{}, id).Error; err != nil {
			return err
		}

		// Auto-unblock tasks that lost their last blocker
		for _, btid := range blockedByMe {
			var count int64
			if err := tx.Model(&model.TaskBlocker{}).Where("task_id = ?", btid).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Model(&model.Task{}).Where("id = ? AND state = ?", btid, model.StateBlocked).
					Update("state", model.StateUnblocked).Error; err != nil {
					return err
				}
			}
		}

		deletedIDs = []uint{id}
		return nil
	})
	if err != nil {
		return err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.deleted",
		TaskIDs: deletedIDs,
	})

	return nil
}

// deleteTaskData removes all associated data for a single task (not the task itself).
func (s *GormStore) deleteTaskData(tx *gorm.DB, taskID uint) error {
	if err := tx.Where("task_id = ?", taskID).Delete(&model.Note{}).Error; err != nil {
		return err
	}
	if err := tx.Where("task_id = ?", taskID).Delete(&model.Link{}).Error; err != nil {
		return err
	}
	if err := tx.Where("task_id = ?", taskID).Delete(&model.TaskTag{}).Error; err != nil {
		return err
	}
	if err := tx.Where("task_id = ? OR blocker_id = ?", taskID, taskID).Delete(&model.TaskBlocker{}).Error; err != nil {
		return err
	}
	return nil
}

// --- Search ---

func (s *GormStore) SearchTasks(ctx context.Context, query string) ([]model.Task, error) {
	if err := validateSearchQuery(query); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	pattern := "%" + escapeLike(strings.ToLower(query)) + "%"
	var tasks []model.Task
	err := db.Where("LOWER(title) LIKE ? ESCAPE '\\' OR LOWER(description) LIKE ? ESCAPE '\\'", pattern, pattern).
		Order("priority ASC").
		Limit(defaultQueryLimit).
		Find(&tasks).Error
	return tasks, err
}

func (s *GormStore) SearchNotes(ctx context.Context, query string) ([]model.Note, error) {
	if err := validateSearchQuery(query); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	pattern := "%" + escapeLike(strings.ToLower(query)) + "%"
	var notes []model.Note
	err := db.Where("LOWER(text) LIKE ? ESCAPE '\\'", pattern).Limit(defaultQueryLimit).Find(&notes).Error
	return notes, err
}

// --- Bulk operations ---

func (s *GormStore) BulkUpdateState(ctx context.Context, ids []uint, state model.TaskState) ([]model.Task, error) {
	if len(ids) > maxBulkIDs {
		return nil, &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}
	if state == model.StateBlocked {
		return nil, fmt.Errorf("use AddBlockers for Blocked state: %w", model.ErrInvalidState)
	}
	if !model.ValidTaskStates[state] {
		return nil, &model.ValidationError{Field: "state", Message: "invalid state"}
	}

	// Process in ascending ID order
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	db := s.db.WithContext(ctx)
	var results []model.Task
	affectedIDs := make([]uint, len(ids))
	copy(affectedIDs, ids)
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := validateID(id); err != nil {
				return err
			}
			var task model.Task
			if err := tx.First(&task, id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("task %d: %w", id, model.ErrNotFound)
				}
				return err
			}
			if task.Archived {
				return fmt.Errorf("task %d: %w", id, model.ErrArchived)
			}

			// Clear blockers
			if err := tx.Where("task_id = ?", id).Delete(&model.TaskBlocker{}).Error; err != nil {
				return err
			}

			// Done cascade
			if state == model.StateDone {
				var blockedTaskIDs []uint
				if err := tx.Model(&model.TaskBlocker{}).Where("blocker_id = ?", id).Pluck("task_id", &blockedTaskIDs).Error; err != nil {
					return err
				}
				if err := tx.Where("blocker_id = ?", id).Delete(&model.TaskBlocker{}).Error; err != nil {
					return err
				}
				for _, btid := range blockedTaskIDs {
					var count int64
					if err := tx.Model(&model.TaskBlocker{}).Where("task_id = ?", btid).Count(&count).Error; err != nil {
						return err
					}
					if count == 0 {
						if err := tx.Model(&model.Task{}).Where("id = ? AND state = ?", btid, model.StateBlocked).
							Update("state", model.StateUnblocked).Error; err != nil {
							return err
						}
						affectedIDs = append(affectedIDs, btid)
					}
				}
			}

			if err := tx.Model(&task).Update("state", state).Error; err != nil {
				return err
			}
			if err := tx.First(&task, id).Error; err != nil {
				return err
			}
			results = append(results, task)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.bulk_state_changed",
		TaskIDs: affectedIDs,
	})

	return results, nil
}

func (s *GormStore) BulkUpdatePriority(ctx context.Context, ids []uint, priority int) ([]model.Task, error) {
	if len(ids) > maxBulkIDs {
		return nil, &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}

	db := s.db.WithContext(ctx)
	var results []model.Task
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := validateID(id); err != nil {
				return err
			}
			var task model.Task
			if err := tx.First(&task, id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("task %d: %w", id, model.ErrNotFound)
				}
				return err
			}

			clamped, err := s.clampBlockerPriority(tx, id, priority)
			if err != nil {
				return err
			}
			oldPriority := task.Priority
			if err := tx.Model(&task).Update("priority", clamped).Error; err != nil {
				return err
			}
			if clamped < oldPriority {
				if err := s.propagatePriorityUp(tx, id, clamped); err != nil {
					return err
				}
			}
			if err := tx.First(&task, id).Error; err != nil {
				return err
			}
			results = append(results, task)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.bulk_priority_changed",
		TaskIDs: ids,
	})

	return results, nil
}

func (s *GormStore) BulkAddTags(ctx context.Context, ids []uint, tags []string) error {
	if len(ids) > maxBulkIDs {
		return &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}
	if err := validateTags(tags); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := validateID(id); err != nil {
				return err
			}
			if _, err := s.taskExists(tx, id); err != nil {
				return err
			}

			// Check tag count limit
			var existing int64
			if err := tx.Model(&model.TaskTag{}).Where("task_id = ?", id).Count(&existing).Error; err != nil {
				return err
			}
			if int(existing)+len(tags) > 50 {
				return &model.ValidationError{Field: "tags", Message: fmt.Sprintf("task %d: max 50 tags per task", id)}
			}

			for _, tag := range tags {
				tt := model.TaskTag{TaskID: id, Tag: tag}
				if err := tx.Where(tt).FirstOrCreate(&tt).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.tags_changed",
		TaskIDs: ids,
	})

	return nil
}

func (s *GormStore) BulkRemoveTags(ctx context.Context, ids []uint, tags []string) error {
	if len(ids) > maxBulkIDs {
		return &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}

	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			if err := validateID(id); err != nil {
				return err
			}
			if _, err := s.taskExists(tx, id); err != nil {
				return err
			}
			for _, tag := range tags {
				if err := tx.Where("task_id = ? AND tag = ?", id, tag).Delete(&model.TaskTag{}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.tags_changed",
		TaskIDs: ids,
	})

	return nil
}

// --- Tags ---

func (s *GormStore) AddTags(ctx context.Context, taskID uint, tags []string) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	if err := validateTags(tags); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExistsActive(tx, taskID); err != nil {
			return err
		}

		// Check tag count
		var existing int64
		if err := tx.Model(&model.TaskTag{}).Where("task_id = ?", taskID).Count(&existing).Error; err != nil {
			return err
		}
		if int(existing)+len(tags) > 50 {
			return &model.ValidationError{Field: "tags", Message: "max 50 tags per task"}
		}

		for _, tag := range tags {
			tt := model.TaskTag{TaskID: taskID, Tag: tag}
			if err := tx.Where(tt).FirstOrCreate(&tt).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.tags_changed",
		TaskIDs: []uint{taskID},
	})

	return nil
}

func (s *GormStore) RemoveTags(ctx context.Context, taskID uint, tags []string) error {
	if err := validateID(taskID); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExistsActive(tx, taskID); err != nil {
			return err
		}
		for _, tag := range tags {
			if err := tx.Where("task_id = ? AND tag = ?", taskID, tag).Delete(&model.TaskTag{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "task.tags_changed",
		TaskIDs: []uint{taskID},
	})

	return nil
}

// --- Links ---

func (s *GormStore) AddLink(ctx context.Context, taskID uint, linkType model.LinkType, url string) (*model.Link, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	if !model.ValidLinkTypes[linkType] {
		return nil, &model.ValidationError{Field: "type", Message: fmt.Sprintf("invalid link type: %s", linkType)}
	}
	if err := validateLinkURL(url); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	link := model.Link{TaskID: taskID, Type: linkType, URL: url}
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExistsActive(tx, taskID); err != nil {
			return err
		}
		return tx.Create(&link).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "link.created",
		TaskIDs: []uint{taskID},
	})

	return &link, nil
}

func (s *GormStore) ListLinks(ctx context.Context, taskID uint) ([]model.Link, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	var links []model.Link
	err := db.Where("task_id = ?", taskID).Find(&links).Error
	return links, err
}

func (s *GormStore) DeleteLink(ctx context.Context, taskID uint, linkID uint) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	if err := validateID(linkID); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	result := db.Where("id = ? AND task_id = ?", linkID, taskID).Delete(&model.Link{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("link %d for task %d: %w", linkID, taskID, model.ErrNotFound)
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "link.deleted",
		TaskIDs: []uint{taskID},
	})

	return nil
}

// --- Notes ---

func (s *GormStore) AddNote(ctx context.Context, taskID uint, text string) (*model.Note, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	if err := validateNoteText(text); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	note := model.Note{TaskID: taskID, Text: text}
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExistsActive(tx, taskID); err != nil {
			return err
		}
		return tx.Create(&note).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "note.created",
		TaskIDs: []uint{taskID},
		NoteIDs: []uint{note.ID},
	})

	return &note, nil
}

func (s *GormStore) UpdateNote(ctx context.Context, taskID uint, noteID uint, text string) (*model.Note, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	if err := validateID(noteID); err != nil {
		return nil, err
	}
	if err := validateNoteText(text); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	var note model.Note
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExistsActive(tx, taskID); err != nil {
			return err
		}
		if err := tx.Where("id = ? AND task_id = ?", noteID, taskID).First(&note).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("note %d for task %d: %w", noteID, taskID, model.ErrNotFound)
			}
			return err
		}
		return tx.Model(&note).Update("text", text).Error
	})
	if err != nil {
		return nil, err
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "note.updated",
		TaskIDs: []uint{taskID},
		NoteIDs: []uint{noteID},
	})

	return &note, nil
}

func (s *GormStore) ListNotes(ctx context.Context, taskID uint) ([]model.Note, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	var notes []model.Note
	err := db.Where("task_id = ?", taskID).Order("created_at ASC").Find(&notes).Error
	return notes, err
}

func (s *GormStore) DeleteNote(ctx context.Context, taskID uint, noteID uint) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	if err := validateID(noteID); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	result := db.Where("id = ? AND task_id = ?", noteID, taskID).Delete(&model.Note{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("note %d for task %d: %w", noteID, taskID, model.ErrNotFound)
	}

	s.emit(ctx, store.StoreEvent{
		Type:    "note.deleted",
		TaskIDs: []uint{taskID},
		NoteIDs: []uint{noteID},
	})

	return nil
}

// Compile-time interface check
var _ store.Store = (*GormStore)(nil)
