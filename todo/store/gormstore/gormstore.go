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
	"unicode/utf8"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/csams/todo/textutil"
	"gorm.io/gorm"
)

const maxBulkIDs = 100
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
	if err := migrateNotesTaskIDNullable(db); err != nil {
		return nil, fmt.Errorf("notes nullable migration: %w", err)
	}
	if err := db.AutoMigrate(
		&model.Task{},
		&model.TaskBlocker{},
		&model.TaskTag{},
		&model.Link{},
		&model.Note{},
		&model.Checkpoint{},
	); err != nil {
		return nil, fmt.Errorf("automigrate: %w", err)
	}
	return &GormStore{db: db, source: "cli"}, nil
}

// migrateNotesTaskIDNullable drops the NOT NULL constraint from notes.task_id.
// AutoMigrate does not change column nullability, so we handle it explicitly.
// No-op if the notes table doesn't exist yet (fresh DB) or if task_id is already nullable.
func migrateNotesTaskIDNullable(db *gorm.DB) error {
	if !db.Migrator().HasTable(&model.Note{}) {
		return nil
	}
	switch db.Dialector.Name() {
	case "postgres":
		var isNullable string
		row := db.Raw(
			"SELECT is_nullable FROM information_schema.columns WHERE table_name = 'notes' AND column_name = 'task_id'",
		).Row()
		if err := row.Scan(&isNullable); err != nil {
			return fmt.Errorf("postgres column lookup: %w", err)
		}
		if isNullable == "NO" {
			if err := db.Exec("ALTER TABLE notes ALTER COLUMN task_id DROP NOT NULL").Error; err != nil {
				return fmt.Errorf("postgres drop not null: %w", err)
			}
		}
	case "sqlite":
		// PRAGMA table_info returns columns: cid, name, type, notnull, dflt_value, pk.
		// Tags map to those exact column names (case-insensitive).
		type sqliteCol struct {
			CID     int     `gorm:"column:cid"`
			Name    string  `gorm:"column:name"`
			Type    string  `gorm:"column:type"`
			NotNull int     `gorm:"column:notnull"`
			Dflt    *string `gorm:"column:dflt_value"`
			PK      int     `gorm:"column:pk"`
		}
		var cols []sqliteCol
		if err := db.Raw("PRAGMA table_info(notes)").Scan(&cols).Error; err != nil {
			return fmt.Errorf("sqlite table_info: %w", err)
		}
		var taskIDNotNull, hasVectorDirty bool
		for _, c := range cols {
			if c.Name == "task_id" && c.NotNull == 1 {
				taskIDNotNull = true
			}
			if c.Name == "vector_dirty" {
				hasVectorDirty = true
			}
		}
		if !taskIDNotNull {
			return nil
		}
		// 12-step ALTER: rebuild table without NOT NULL on task_id. Only legacy columns
		// are copied; AutoMigrate adds archived/updated_at after this step.
		// PRAGMA foreign_keys can only change outside a transaction, so toggle it around
		// the rebuild and run the schema changes inside db.Transaction so partial failures
		// roll back cleanly.
		if err := db.Exec("PRAGMA foreign_keys=OFF").Error; err != nil {
			return fmt.Errorf("sqlite pragma off: %w", err)
		}
		txErr := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE notes_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				task_id INTEGER,
				text TEXT NOT NULL,
				vector_dirty INTEGER NOT NULL DEFAULT 0,
				created_at DATETIME
			)`).Error; err != nil {
				return fmt.Errorf("create notes_new: %w", err)
			}
			var copyStmt string
			if hasVectorDirty {
				copyStmt = `INSERT INTO notes_new (id, task_id, text, vector_dirty, created_at)
					SELECT id, task_id, text, COALESCE(vector_dirty, 0), created_at FROM notes`
			} else {
				copyStmt = `INSERT INTO notes_new (id, task_id, text, created_at)
					SELECT id, task_id, text, created_at FROM notes`
			}
			if err := tx.Exec(copyStmt).Error; err != nil {
				return fmt.Errorf("copy rows: %w", err)
			}
			if err := tx.Exec("DROP TABLE notes").Error; err != nil {
				return fmt.Errorf("drop old: %w", err)
			}
			if err := tx.Exec("ALTER TABLE notes_new RENAME TO notes").Error; err != nil {
				return fmt.Errorf("rename: %w", err)
			}
			if err := tx.Exec("CREATE INDEX idx_notes_task_id ON notes(task_id)").Error; err != nil {
				return fmt.Errorf("recreate index: %w", err)
			}
			return nil
		})
		// Always restore FK enforcement, even on rebuild failure.
		if pragmaErr := db.Exec("PRAGMA foreign_keys=ON").Error; pragmaErr != nil && txErr == nil {
			return fmt.Errorf("sqlite pragma on: %w", pragmaErr)
		}
		if txErr != nil {
			return fmt.Errorf("sqlite rebuild: %w", txErr)
		}
	}
	return nil
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

func validateTitle(title string) (string, error) {
	clean, err := textutil.Sanitize(title)
	if err != nil {
		return "", &model.ValidationError{Field: "title", Message: err.Error()}
	}
	if clean == "" {
		return "", &model.ValidationError{Field: "title", Message: "required and non-empty"}
	}
	if utf8.RuneCountInString(clean) > 512 {
		return "", &model.ValidationError{Field: "title", Message: "max 512 characters"}
	}
	return clean, nil
}

func validateDescription(desc string) (string, error) {
	if desc == "" {
		return "", nil
	}
	clean, err := textutil.Sanitize(desc)
	if err != nil {
		return "", &model.ValidationError{Field: "description", Message: err.Error()}
	}
	if utf8.RuneCountInString(clean) > 100000 {
		return "", &model.ValidationError{Field: "description", Message: "max 100000 characters"}
	}
	return clean, nil
}

func validateTag(tag string) (string, error) {
	clean, err := textutil.Sanitize(tag)
	if err != nil {
		return "", &model.ValidationError{Field: "tag", Message: err.Error()}
	}
	if clean == "" {
		return "", &model.ValidationError{Field: "tag", Message: "non-empty"}
	}
	if len(clean) > 100 {
		return "", &model.ValidationError{Field: "tag", Message: "max 100 characters"}
	}
	if !tagRegex.MatchString(clean) {
		return "", &model.ValidationError{Field: "tag", Message: "alphanumeric, hyphens, underscores only"}
	}
	return clean, nil
}

func validateTags(tags []string) ([]string, error) {
	clean := make([]string, len(tags))
	for i, t := range tags {
		c, err := validateTag(t)
		if err != nil {
			return nil, err
		}
		clean[i] = c
	}
	return clean, nil
}

func validateNoteText(text string) (string, error) {
	clean, err := textutil.Sanitize(text)
	if err != nil {
		return "", &model.ValidationError{Field: "text", Message: err.Error()}
	}
	if clean == "" {
		return "", &model.ValidationError{Field: "text", Message: "required and non-empty"}
	}
	if utf8.RuneCountInString(clean) > 50000 {
		return "", &model.ValidationError{Field: "text", Message: "max 50000 characters"}
	}
	return clean, nil
}

func validateCheckpointRecap(text string) (string, error) {
	clean, err := textutil.Sanitize(text)
	if err != nil {
		return "", &model.ValidationError{Field: "recap", Message: err.Error()}
	}
	if clean == "" {
		return "", &model.ValidationError{Field: "recap", Message: "required and non-empty"}
	}
	if utf8.RuneCountInString(clean) > 10000 {
		return "", &model.ValidationError{Field: "recap", Message: "max 10000 characters"}
	}
	return clean, nil
}

func validateCheckpointNextSteps(text string) (string, error) {
	clean, err := textutil.Sanitize(text)
	if err != nil {
		return "", &model.ValidationError{Field: "next_steps", Message: err.Error()}
	}
	if clean == "" {
		return "", &model.ValidationError{Field: "next_steps", Message: "required and non-empty"}
	}
	if utf8.RuneCountInString(clean) > 10000 {
		return "", &model.ValidationError{Field: "next_steps", Message: "max 10000 characters"}
	}
	return clean, nil
}

func validateCheckpointOpenThreads(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	clean, err := textutil.Sanitize(text)
	if err != nil {
		return "", &model.ValidationError{Field: "open_threads", Message: err.Error()}
	}
	if utf8.RuneCountInString(clean) > 10000 {
		return "", &model.ValidationError{Field: "open_threads", Message: "max 10000 characters"}
	}
	return clean, nil
}

func validateLinkURL(url string) (string, error) {
	clean, err := textutil.Sanitize(url)
	if err != nil {
		return "", &model.ValidationError{Field: "url", Message: err.Error()}
	}
	if clean == "" {
		return "", &model.ValidationError{Field: "url", Message: "required and non-empty"}
	}
	if len(clean) > 2000 {
		return "", &model.ValidationError{Field: "url", Message: "max 2000 bytes"}
	}
	return clean, nil
}

func validateLinkDescription(desc string) (string, error) {
	clean, err := textutil.Sanitize(desc)
	if err != nil {
		return "", &model.ValidationError{Field: "description", Message: err.Error()}
	}
	if utf8.RuneCountInString(clean) > 1000 {
		return "", &model.ValidationError{Field: "description", Message: "max 1000 characters"}
	}
	return clean, nil
}

func validateSearchQuery(q string) (string, error) {
	clean, err := textutil.Sanitize(q)
	if err != nil {
		return "", &model.ValidationError{Field: "query", Message: err.Error()}
	}
	if clean == "" {
		return "", &model.ValidationError{Field: "query", Message: "required and non-empty"}
	}
	if utf8.RuneCountInString(clean) > 500 {
		return "", &model.ValidationError{Field: "query", Message: "max 500 characters"}
	}
	return clean, nil
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
	var err error
	if title, err = validateTitle(title); err != nil {
		return nil, err
	}
	if description, err = validateDescription(description); err != nil {
		return nil, err
	}
	if tags, err = validateTags(tags); err != nil {
		return nil, err
	}

	task := model.Task{
		Title:       title,
		Description: model.PtrIfNonEmpty(description),
		Priority:    priority,
		State:       model.StateNew,
		DueAt:       dueAt,
	}

	db := s.db.WithContext(ctx)
	err = db.Transaction(func(tx *gorm.DB) error {
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
	var err error
	if title, err = validateTitle(title); err != nil {
		return nil, err
	}
	if description, err = validateDescription(description); err != nil {
		return nil, err
	}
	if tags, err = validateTags(tags); err != nil {
		return nil, err
	}

	task := model.Task{
		Title:       title,
		Description: model.PtrIfNonEmpty(description),
		Priority:    priority,
		State:       model.StateNew,
		DueAt:       dueAt,
		ParentID:    &parentID,
	}

	db := s.db.WithContext(ctx)
	err = db.Transaction(func(tx *gorm.DB) error {
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

// taskBaseColumns is the always-loaded column set for GetTask and ListTasks.
// `description` is added when opts.Include["description"]. `parent_id` must be
// present even when "parent" is not requested, otherwise GORM silently no-ops
// Preload("Parent").
//
// Tags are always loaded via Preload("Tags") in both GetTask and ListTasks —
// they are cheap, bounded, and the vector syncer's chunk-building logic
// (store/synced/synced.go buildTaskHeader) depends on them being present.
// Do not move Tags behind an opt-in without updating the syncer.
var taskBaseColumns = []string{
	"id", "title", "priority", "state", "archived",
	"due_at", "parent_id", "created_at", "updated_at",
}

func (s *GormStore) GetTask(ctx context.Context, id uint, opts store.GetTaskOptions) (*model.TaskDetail, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}

	inc := opts.Include

	db := s.db.WithContext(ctx)
	cols := append([]string{}, taskBaseColumns...)
	if inc["description"] {
		cols = append(cols, "description")
	}

	// Tags and Checkpoint are always loaded (cheap, bounded).
	q := db.Select(cols).Preload("Tags").Preload("Checkpoint")
	if inc["notes"] {
		q = q.Preload("Notes")
	}
	if inc["blockers"] {
		q = q.Preload("Blockers")
	}
	if inc["links"] {
		q = q.Preload("Links")
	}
	if inc["children"] {
		q = q.Preload("Children")
	}
	if inc["parent"] {
		q = q.Preload("Parent")
	}

	var task model.Task
	if err := q.First(&task, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("task %d: %w", id, model.ErrNotFound)
		}
		return nil, err
	}

	detail := &model.TaskDetail{Task: task}

	if inc["blocking"] {
		var blockedTaskIDs []uint
		if err := db.Model(&model.TaskBlocker{}).
			Where("blocker_id = ?", id).
			Pluck("task_id", &blockedTaskIDs).Error; err != nil {
			return nil, err
		}
		if len(blockedTaskIDs) > 0 {
			var blocking []model.Task
			if err := db.Where("id IN ?", blockedTaskIDs).Find(&blocking).Error; err != nil {
				return nil, err
			}
			detail.Blocking = blocking
		}
	}

	return detail, nil
}

// GetTasks returns multiple task details in the caller's input order.
// Duplicates collapse to first occurrence. Missing IDs go to NotFound rather
// than producing an error. Read-only — no transaction, no observer events.
//
// Ordering note: bulk mutation ops sort IDs ascending for deterministic
// locking; reads preserve input order so callers can align ids[i] with
// either the found result or the not_found list.
func (s *GormStore) GetTasks(ctx context.Context, ids []uint, opts store.GetTaskOptions) (store.BatchGetTasksResult, error) {
	result := store.BatchGetTasksResult{
		Tasks:    make([]model.TaskDetail, 0),
		NotFound: make([]uint, 0),
	}

	if len(ids) == 0 {
		return result, &model.ValidationError{Field: "ids", Message: "must not be empty"}
	}
	if len(ids) > maxBulkIDs {
		return result, &model.ValidationError{Field: "ids", Message: fmt.Sprintf("max %d IDs per call", maxBulkIDs)}
	}

	// Dedup preserving first-occurrence order; remember each ID's input index.
	uniqueIDs := make([]uint, 0, len(ids))
	inputIndex := make(map[uint]int, len(ids))
	for i, id := range ids {
		if err := validateID(id); err != nil {
			return result, err
		}
		if _, seen := inputIndex[id]; seen {
			continue
		}
		inputIndex[id] = i
		uniqueIDs = append(uniqueIDs, id)
	}

	inc := opts.Include

	db := s.db.WithContext(ctx)
	cols := append([]string{}, taskBaseColumns...)
	if inc["description"] {
		cols = append(cols, "description")
	}

	q := db.Model(&model.Task{}).Select(cols).Preload("Tags").Preload("Checkpoint")
	if inc["notes"] {
		q = q.Preload("Notes")
	}
	if inc["blockers"] {
		q = q.Preload("Blockers")
	}
	if inc["links"] {
		q = q.Preload("Links")
	}
	if inc["children"] {
		q = q.Preload("Children")
	}
	if inc["parent"] {
		q = q.Preload("Parent")
	}

	var tasks []model.Task
	if err := q.Where("id IN ?", uniqueIDs).Find(&tasks).Error; err != nil {
		return result, err
	}

	// Build per-ID Blocking lists with two batched queries when requested.
	blockingByTaskID := map[uint][]model.Task{}
	if inc["blocking"] && len(tasks) > 0 {
		var rows []model.TaskBlocker
		if err := db.Model(&model.TaskBlocker{}).Where("blocker_id IN ?", uniqueIDs).Find(&rows).Error; err != nil {
			return result, err
		}
		blockedIDsByBlocker := map[uint][]uint{}
		blockedIDSet := map[uint]struct{}{}
		for _, r := range rows {
			blockedIDsByBlocker[r.BlockerID] = append(blockedIDsByBlocker[r.BlockerID], r.TaskID)
			blockedIDSet[r.TaskID] = struct{}{}
		}
		if len(blockedIDSet) > 0 {
			blockedIDs := make([]uint, 0, len(blockedIDSet))
			for id := range blockedIDSet {
				blockedIDs = append(blockedIDs, id)
			}
			var blocking []model.Task
			if err := db.Where("id IN ?", blockedIDs).Find(&blocking).Error; err != nil {
				return result, err
			}
			byID := make(map[uint]model.Task, len(blocking))
			for _, t := range blocking {
				byID[t.ID] = t
			}
			for blockerID, blocked := range blockedIDsByBlocker {
				list := make([]model.Task, 0, len(blocked))
				for _, bid := range blocked {
					if t, ok := byID[bid]; ok {
						list = append(list, t)
					}
				}
				blockingByTaskID[blockerID] = list
			}
		}
	}

	details := make([]model.TaskDetail, len(tasks))
	foundIDs := make(map[uint]struct{}, len(tasks))
	for i, t := range tasks {
		d := model.TaskDetail{Task: t}
		if blocking, ok := blockingByTaskID[t.ID]; ok {
			d.Blocking = blocking
		}
		details[i] = d
		foundIDs[t.ID] = struct{}{}
	}

	// Sort details by the caller's input position.
	sort.Slice(details, func(i, j int) bool {
		return inputIndex[details[i].ID] < inputIndex[details[j].ID]
	})
	result.Tasks = details

	// Collect missing IDs in input order.
	for _, id := range uniqueIDs {
		if _, ok := foundIDs[id]; !ok {
			result.NotFound = append(result.NotFound, id)
		}
	}

	return result, nil
}

func (s *GormStore) ListTasks(ctx context.Context, opts store.ListTasksOptions) ([]model.TaskListItem, error) {
	db := s.db.WithContext(ctx)
	inc := opts.Include
	cols := append([]string{}, taskBaseColumns...)
	if inc["description"] {
		cols = append(cols, "description")
	}
	q := db.Model(&model.Task{}).Select(cols).Preload("Tags")
	if inc["notes"] {
		q = q.Preload("Notes")
	}
	if inc["blockers"] {
		q = q.Preload("Blockers")
	}
	if inc["links"] {
		q = q.Preload("Links")
	}
	if inc["children"] {
		q = q.Preload("Children")
	}
	if inc["parent"] {
		q = q.Preload("Parent")
	}

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
	if opts.HasDueDate != nil {
		if *opts.HasDueDate {
			q = q.Where("due_at IS NOT NULL")
		} else {
			q = q.Where("due_at IS NULL")
		}
	}
	if opts.DueBefore != nil {
		q = q.Where("due_at IS NOT NULL AND due_at < ?", *opts.DueBefore)
	}
	if opts.DueAfter != nil {
		q = q.Where("due_at IS NOT NULL AND due_at > ?", *opts.DueAfter)
	}
	if opts.DueOn != nil {
		start := time.Date(opts.DueOn.Year(), opts.DueOn.Month(), opts.DueOn.Day(), 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 0, 1)
		q = q.Where("due_at IS NOT NULL AND due_at >= ? AND due_at < ?", start, end)
	}
	if opts.PriorityMin != nil {
		q = q.Where("priority >= ?", *opts.PriorityMin)
	}
	if opts.PriorityMax != nil {
		q = q.Where("priority <= ?", *opts.PriorityMax)
	}

	// Tag filter (AND logic)
	if len(opts.Tags) > 0 {
		cleanTags, err := validateTags(opts.Tags)
		if err != nil {
			return nil, err
		}
		for _, tag := range cleanTags {
			q = q.Where("id IN (SELECT task_id FROM task_tags WHERE tag = ?)", tag)
		}
	}
	// Tag subset filter: task's tags must all be within the given set
	if len(opts.TagsSubsetOf) > 0 {
		cleanSubset, err := validateTags(opts.TagsSubsetOf)
		if err != nil {
			return nil, err
		}
		q = q.Where("NOT EXISTS (SELECT 1 FROM task_tags WHERE task_tags.task_id = tasks.id AND tag NOT IN ?)", cleanSubset)
	}

	// Query: case-insensitive substring across title, description, and link
	// descriptions. Gated on non-empty so the zero value of ListTasksOptions
	// remains a no-op (validateSearchQuery rejects empty strings).
	if opts.Query != "" {
		query, err := validateSearchQuery(opts.Query)
		if err != nil {
			return nil, err
		}
		pattern := "%" + escapeLike(strings.ToLower(query)) + "%"
		q = q.Where(
			"LOWER(tasks.title) LIKE ? ESCAPE '\\' OR "+
				"LOWER(tasks.description) LIKE ? ESCAPE '\\' OR "+
				"EXISTS (SELECT 1 FROM links WHERE links.task_id = tasks.id AND LOWER(links.description) LIKE ? ESCAPE '\\')",
			pattern, pattern, pattern,
		)
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

	items := make([]model.TaskListItem, len(tasks))
	for i := range tasks {
		items[i] = model.TaskListItem{Task: tasks[i]}
	}
	if len(tasks) > 0 {
		taskIDs := make([]uint, len(tasks))
		for i := range tasks {
			taskIDs[i] = tasks[i].ID
		}
		var withCheckpoint []uint
		if err := db.Model(&model.Checkpoint{}).
			Where("task_id IN ?", taskIDs).
			Pluck("task_id", &withCheckpoint).Error; err != nil {
			return nil, err
		}
		set := make(map[uint]bool, len(withCheckpoint))
		for _, id := range withCheckpoint {
			set[id] = true
		}
		for i := range items {
			items[i].HasCheckpoint = set[items[i].ID]
		}
	}
	return items, nil
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
			cleanTitle, err := validateTitle(*opts.Title)
			if err != nil {
				return err
			}
			changes["title"] = store.Change{Old: task.Title, New: cleanTitle}
			updates["title"] = cleanTitle
		}
		if opts.Description != nil {
			cleanDesc, err := validateDescription(*opts.Description)
			if err != nil {
				return err
			}
			changes["description"] = store.Change{Old: model.DerefStr(task.Description), New: cleanDesc}
			updates["description"] = model.PtrIfNonEmpty(cleanDesc)
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

func (s *GormStore) DeleteTask(ctx context.Context, id uint, opts store.DeleteTaskOptions) error {
	if err := validateID(id); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	var deletedIDs []uint
	var orphanedNoteIDs, deletedNoteIDs []uint

	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, id); err != nil {
			return err
		}

		if opts.Recursive {
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
				orphaned, deleted, err := s.deleteTaskData(tx, tid, opts.DeleteNotes)
				if err != nil {
					return err
				}
				orphanedNoteIDs = append(orphanedNoteIDs, orphaned...)
				deletedNoteIDs = append(deletedNoteIDs, deleted...)
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
		orphaned, deleted, err := s.deleteTaskData(tx, id, opts.DeleteNotes)
		if err != nil {
			return err
		}
		orphanedNoteIDs = orphaned
		deletedNoteIDs = deleted

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

	// Emit after commit.
	if len(orphanedNoteIDs) > 0 {
		s.emit(ctx, store.StoreEvent{
			Type:    "note.updated",
			TaskIDs: deletedIDs,
			NoteIDs: orphanedNoteIDs,
		})
	}
	if len(deletedNoteIDs) > 0 {
		s.emit(ctx, store.StoreEvent{
			Type:    "note.deleted",
			TaskIDs: deletedIDs,
			NoteIDs: deletedNoteIDs,
		})
	}
	s.emit(ctx, store.StoreEvent{
		Type:    "task.deleted",
		TaskIDs: deletedIDs,
	})

	return nil
}

// deleteTaskData removes tags/links/blockers for a task and either orphans (default)
// or hard-deletes its notes. Returns the note IDs that were orphaned and the note IDs
// that were hard-deleted; the caller emits events after the surrounding transaction commits.
func (s *GormStore) deleteTaskData(tx *gorm.DB, taskID uint, deleteNotes bool) ([]uint, []uint, error) {
	var orphanedNoteIDs, deletedNoteIDs []uint

	// Collect note IDs first so the caller can emit events after commit.
	var noteIDs []uint
	if err := tx.Model(&model.Note{}).Where("task_id = ?", taskID).Pluck("id", &noteIDs).Error; err != nil {
		return nil, nil, err
	}

	if deleteNotes {
		if len(noteIDs) > 0 {
			if err := tx.Where("id IN ?", noteIDs).Delete(&model.Note{}).Error; err != nil {
				return nil, nil, err
			}
			deletedNoteIDs = noteIDs
		}
	} else {
		if len(noteIDs) > 0 {
			// Set task_id = NULL via a map so GORM emits the SQL we want.
			if err := tx.Model(&model.Note{}).Where("id IN ?", noteIDs).
				Updates(map[string]any{"task_id": nil}).Error; err != nil {
				return nil, nil, err
			}
			orphanedNoteIDs = noteIDs
		}
	}

	if err := tx.Where("task_id = ?", taskID).Delete(&model.Link{}).Error; err != nil {
		return nil, nil, err
	}
	if err := tx.Where("task_id = ?", taskID).Delete(&model.TaskTag{}).Error; err != nil {
		return nil, nil, err
	}
	if err := tx.Where("task_id = ? OR blocker_id = ?", taskID, taskID).Delete(&model.TaskBlocker{}).Error; err != nil {
		return nil, nil, err
	}
	if err := tx.Where("task_id = ?", taskID).Delete(&model.Checkpoint{}).Error; err != nil {
		return nil, nil, err
	}
	return orphanedNoteIDs, deletedNoteIDs, nil
}

// --- Search ---

func (s *GormStore) SearchTasks(ctx context.Context, query string) ([]model.Task, error) {
	var err error
	if query, err = validateSearchQuery(query); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	pattern := "%" + escapeLike(strings.ToLower(query)) + "%"
	var tasks []model.Task
	err = db.Where("LOWER(title) LIKE ? ESCAPE '\\' OR LOWER(description) LIKE ? ESCAPE '\\'", pattern, pattern).
		Order("priority ASC").
		Limit(defaultQueryLimit).
		Find(&tasks).Error
	return tasks, err
}

func (s *GormStore) SearchNotes(ctx context.Context, query string, opts store.SearchNotesOptions) ([]model.Note, error) {
	var err error
	if query, err = validateSearchQuery(query); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	pattern := "%" + escapeLike(strings.ToLower(query)) + "%"
	q := db.Where("LOWER(text) LIKE ? ESCAPE '\\'", pattern)
	if !opts.IncludeArchived {
		q = q.Where("archived = ?", false)
	}
	if opts.TaskID != nil {
		q = q.Where("task_id = ?", *opts.TaskID)
	}
	var notes []model.Note
	err = q.Limit(defaultQueryLimit).Find(&notes).Error
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
	var err error
	if tags, err = validateTags(tags); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	err = db.Transaction(func(tx *gorm.DB) error {
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
	var err error
	if tags, err = validateTags(tags); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	err = db.Transaction(func(tx *gorm.DB) error {
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
	var err error
	if tags, err = validateTags(tags); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	err = db.Transaction(func(tx *gorm.DB) error {
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
	var err error
	if tags, err = validateTags(tags); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	err = db.Transaction(func(tx *gorm.DB) error {
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

func (s *GormStore) AddLink(ctx context.Context, taskID uint, linkType model.LinkType, url, description string) (*model.Link, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	if !model.ValidLinkTypes[linkType] {
		return nil, &model.ValidationError{Field: "type", Message: fmt.Sprintf("invalid link type: %s", linkType)}
	}
	var err error
	if url, err = validateLinkURL(url); err != nil {
		return nil, err
	}
	if description, err = validateLinkDescription(description); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	link := model.Link{TaskID: taskID, Type: linkType, URL: url, Description: description}
	err = db.Transaction(func(tx *gorm.DB) error {
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

func (s *GormStore) UpdateLink(ctx context.Context, taskID, linkID uint, opts store.UpdateLinkOptions) (*model.Link, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	if err := validateID(linkID); err != nil {
		return nil, err
	}

	updates := map[string]any{}
	if opts.Type != nil {
		if !model.ValidLinkTypes[*opts.Type] {
			return nil, &model.ValidationError{Field: "type", Message: fmt.Sprintf("invalid link type: %s", *opts.Type)}
		}
		updates["type"] = *opts.Type
	}
	if opts.URL != nil {
		clean, err := validateLinkURL(*opts.URL)
		if err != nil {
			return nil, err
		}
		updates["url"] = clean
	}
	if opts.Description != nil {
		clean, err := validateLinkDescription(*opts.Description)
		if err != nil {
			return nil, err
		}
		updates["description"] = clean
	}

	db := s.db.WithContext(ctx)
	var link model.Link
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExistsActive(tx, taskID); err != nil {
			return err
		}
		if err := tx.Where("id = ? AND task_id = ?", linkID, taskID).First(&link).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("link %d for task %d: %w", linkID, taskID, model.ErrNotFound)
			}
			return err
		}
		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&link).Updates(updates).Error
	})
	if err != nil {
		return nil, err
	}

	if len(updates) > 0 {
		s.emit(ctx, store.StoreEvent{
			Type:    "link.updated",
			TaskIDs: []uint{taskID},
		})
	}

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

func validateOptionalTaskID(taskID *uint) error {
	if taskID == nil {
		return nil
	}
	return validateID(*taskID)
}

func (s *GormStore) AddNote(ctx context.Context, taskID *uint, text string) (*model.Note, error) {
	if err := validateOptionalTaskID(taskID); err != nil {
		return nil, err
	}
	var err error
	if text, err = validateNoteText(text); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	note := model.Note{TaskID: taskID, Text: text}
	err = db.Transaction(func(tx *gorm.DB) error {
		if taskID != nil {
			if _, err := s.taskExistsActive(tx, *taskID); err != nil {
				return err
			}
		}
		return tx.Create(&note).Error
	})
	if err != nil {
		return nil, err
	}

	event := store.StoreEvent{
		Type:    "note.created",
		NoteIDs: []uint{note.ID},
	}
	if taskID != nil {
		event.TaskIDs = []uint{*taskID}
	}
	s.emit(ctx, event)

	return &note, nil
}

func (s *GormStore) UpdateNote(ctx context.Context, noteID uint, opts store.UpdateNoteOptions) (*model.Note, error) {
	if err := validateID(noteID); err != nil {
		return nil, err
	}
	if opts.Text == nil && !opts.SetTaskID && opts.Archived == nil {
		return nil, &model.ValidationError{Field: "opts", Message: "at least one of text, task_id, archived must be provided"}
	}

	var cleanText string
	if opts.Text != nil {
		var err error
		if cleanText, err = validateNoteText(*opts.Text); err != nil {
			return nil, err
		}
	}
	if opts.SetTaskID && opts.TaskID != nil {
		if err := validateID(*opts.TaskID); err != nil {
			return nil, err
		}
	}

	db := s.db.WithContext(ctx)
	var note model.Note
	var oldTaskID *uint
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&note, noteID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("note %d: %w", noteID, model.ErrNotFound)
			}
			return err
		}
		oldTaskID = note.TaskID

		updates := map[string]any{}
		if opts.Text != nil {
			updates["text"] = cleanText
		}
		if opts.SetTaskID {
			if opts.TaskID != nil {
				if _, err := s.taskExistsActive(tx, *opts.TaskID); err != nil {
					return err
				}
			}
			updates["task_id"] = opts.TaskID
		}
		if opts.Archived != nil {
			updates["archived"] = *opts.Archived
		}

		if err := tx.Model(&note).Updates(updates).Error; err != nil {
			return err
		}
		// Reload to capture updated_at and any other side effects.
		return tx.First(&note, noteID).Error
	})
	if err != nil {
		return nil, err
	}

	event := store.StoreEvent{
		Type:    "note.updated",
		NoteIDs: []uint{noteID},
	}
	taskIDSet := map[uint]struct{}{}
	if oldTaskID != nil {
		taskIDSet[*oldTaskID] = struct{}{}
	}
	if note.TaskID != nil {
		taskIDSet[*note.TaskID] = struct{}{}
	}
	for tid := range taskIDSet {
		event.TaskIDs = append(event.TaskIDs, tid)
	}
	s.emit(ctx, event)

	return &note, nil
}

func (s *GormStore) ListNotes(ctx context.Context, taskID *uint) ([]model.Note, error) {
	if err := validateOptionalTaskID(taskID); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	var notes []model.Note
	q := db.Model(&model.Note{})
	if taskID == nil {
		q = q.Where("task_id IS NULL")
	} else {
		q = q.Where("task_id = ?", *taskID)
	}
	err := q.Order("created_at ASC").Find(&notes).Error
	return notes, err
}

func (s *GormStore) ListAllNotes(ctx context.Context) ([]model.Note, error) {
	db := s.db.WithContext(ctx)
	var notes []model.Note
	err := db.Order("created_at ASC").Find(&notes).Error
	return notes, err
}

func (s *GormStore) GetNotesByIDs(ctx context.Context, ids []uint) ([]model.Note, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	db := s.db.WithContext(ctx)
	var notes []model.Note
	err := db.Where("id IN ?", ids).Find(&notes).Error
	return notes, err
}

func (s *GormStore) DeleteNote(ctx context.Context, noteID uint) error {
	if err := validateID(noteID); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	var note model.Note
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&note, noteID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("note %d: %w", noteID, model.ErrNotFound)
			}
			return err
		}
		return tx.Delete(&model.Note{}, noteID).Error
	})
	if err != nil {
		return err
	}

	event := store.StoreEvent{
		Type:    "note.deleted",
		NoteIDs: []uint{noteID},
	}
	if note.TaskID != nil {
		event.TaskIDs = []uint{*note.TaskID}
	}
	s.emit(ctx, event)

	return nil
}

func (s *GormStore) ArchiveNote(ctx context.Context, noteID uint, archived bool) error {
	if err := validateID(noteID); err != nil {
		return err
	}

	db := s.db.WithContext(ctx)
	var note model.Note
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&note, noteID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("note %d: %w", noteID, model.ErrNotFound)
			}
			return err
		}
		return tx.Model(&note).Update("archived", archived).Error
	})
	if err != nil {
		return err
	}

	eventType := "note.archived"
	if !archived {
		eventType = "note.unarchived"
	}
	event := store.StoreEvent{
		Type:    eventType,
		NoteIDs: []uint{noteID},
	}
	if note.TaskID != nil {
		event.TaskIDs = []uint{*note.TaskID}
	}
	s.emit(ctx, event)

	return nil
}

// --- Checkpoints ---

func (s *GormStore) GetCheckpoint(ctx context.Context, taskID uint) (*model.Checkpoint, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	if _, err := s.taskExists(db, taskID); err != nil {
		return nil, err
	}
	var cp model.Checkpoint
	if err := db.Where("task_id = ?", taskID).First(&cp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("checkpoint for task %d: %w", taskID, model.ErrNotFound)
		}
		return nil, err
	}
	return &cp, nil
}

func (s *GormStore) SetCheckpoint(ctx context.Context, taskID uint, opts store.SetCheckpointOptions) (*model.Checkpoint, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	cleanRecap, err := validateCheckpointRecap(opts.Recap)
	if err != nil {
		return nil, err
	}
	cleanNext, err := validateCheckpointNextSteps(opts.NextSteps)
	if err != nil {
		return nil, err
	}
	cleanOpen, err := validateCheckpointOpenThreads(opts.OpenThreads)
	if err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)
	var cp model.Checkpoint
	var changes map[string]store.Change
	var eventType string

	err = db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExistsActive(tx, taskID); err != nil {
			return err
		}

		// Resolve insert vs update by attempting an INSERT first. The unique
		// index on task_id makes this race-safe across both SQLite and
		// Postgres — a concurrent caller's INSERT will fail with a duplicate
		// key error, after which we fall through to the UPDATE path.
		cp = model.Checkpoint{
			TaskID:      taskID,
			Recap:       cleanRecap,
			NextSteps:   cleanNext,
			OpenThreads: cleanOpen,
		}
		createErr := tx.Create(&cp).Error
		if createErr == nil {
			eventType = "checkpoint.created"
			return nil
		}
		if !isUniqueViolation(createErr) {
			return createErr
		}

		// A row already exists — load it, compute the diff, and update only
		// if at least one field actually changed.
		var existing model.Checkpoint
		if err := tx.Where("task_id = ?", taskID).First(&existing).Error; err != nil {
			return err
		}
		changes = map[string]store.Change{}
		if existing.Recap != cleanRecap {
			changes["recap"] = store.Change{Old: existing.Recap, New: cleanRecap}
		}
		if existing.NextSteps != cleanNext {
			changes["next_steps"] = store.Change{Old: existing.NextSteps, New: cleanNext}
		}
		if existing.OpenThreads != cleanOpen {
			changes["open_threads"] = store.Change{Old: existing.OpenThreads, New: cleanOpen}
		}
		if len(changes) == 0 {
			// No-op: nothing to update and no event to emit.
			cp = existing
			return nil
		}
		updates := map[string]any{
			"recap":        cleanRecap,
			"next_steps":   cleanNext,
			"open_threads": cleanOpen,
		}
		if err := tx.Model(&existing).Updates(updates).Error; err != nil {
			return err
		}
		// Reload to capture the bumped UpdatedAt.
		if err := tx.Where("task_id = ?", taskID).First(&cp).Error; err != nil {
			return err
		}
		eventType = "checkpoint.updated"
		return nil
	})
	if err != nil {
		return nil, err
	}

	if eventType != "" {
		event := store.StoreEvent{
			Type:    eventType,
			TaskIDs: []uint{taskID},
		}
		if eventType == "checkpoint.updated" {
			event.Changes = changes
		}
		s.emit(ctx, event)
	}
	return &cp, nil
}

// isUniqueViolation returns true if err is a duplicate-key error from a
// unique-index violation. Detects both SQLite and Postgres error text.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") || // SQLite
		strings.Contains(s, "duplicate key value violates unique constraint") || // Postgres
		strings.Contains(s, "SQLSTATE 23505") // Postgres pq error
}

func (s *GormStore) DeleteCheckpoint(ctx context.Context, taskID uint) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.taskExists(tx, taskID); err != nil {
			return err
		}
		res := tx.Where("task_id = ?", taskID).Delete(&model.Checkpoint{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("checkpoint for task %d: %w", taskID, model.ErrNotFound)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.emit(ctx, store.StoreEvent{
		Type:    "checkpoint.deleted",
		TaskIDs: []uint{taskID},
	})
	return nil
}

// Compile-time interface check
var _ store.Store = (*GormStore)(nil)
