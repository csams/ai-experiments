package store

import (
	"context"
	"time"

	"github.com/csams/todo/model"
)

// Store defines the interface for all task tracking operations.
// All mutating operations must be wrapped in transactions.
type Store interface {
	// Tasks
	CreateTask(ctx context.Context, title, description string, priority int, dueAt *time.Time, tags []string) (*model.Task, error)
	CreateSubtask(ctx context.Context, parentID uint, title, description string, priority int, dueAt *time.Time, tags []string) (*model.Task, error)
	GetTask(ctx context.Context, id uint) (*model.TaskDetail, error)
	ListTasks(ctx context.Context, opts ListTasksOptions) ([]model.Task, error)
	UpdateTask(ctx context.Context, id uint, opts UpdateTaskOptions) (*model.Task, error)
	SetTaskState(ctx context.Context, id uint, state model.TaskState) (*model.Task, error) // non-Blocked only; Blocked returns ErrInvalidState
	AddBlockers(ctx context.Context, taskID uint, blockerIDs []uint) (*model.Task, error)
	RemoveBlockers(ctx context.Context, taskID uint, blockerIDs []uint) (*model.Task, error)
	SetParent(ctx context.Context, id uint, parentID *uint) error
	ArchiveTask(ctx context.Context, id uint, archived bool) error
	DeleteTask(ctx context.Context, id uint, recursive bool) error
	SearchTasks(ctx context.Context, query string) ([]model.Task, error)
	SearchNotes(ctx context.Context, query string) ([]model.Note, error)

	// Bulk operations (max 100 IDs per call)
	BulkUpdateState(ctx context.Context, ids []uint, state model.TaskState) ([]model.Task, error)
	BulkUpdatePriority(ctx context.Context, ids []uint, priority int) ([]model.Task, error)
	BulkAddTags(ctx context.Context, ids []uint, tags []string) error
	BulkRemoveTags(ctx context.Context, ids []uint, tags []string) error

	// Tags
	AddTags(ctx context.Context, taskID uint, tags []string) error
	RemoveTags(ctx context.Context, taskID uint, tags []string) error

	// Links
	AddLink(ctx context.Context, taskID uint, linkType model.LinkType, url string) (*model.Link, error)
	ListLinks(ctx context.Context, taskID uint) ([]model.Link, error)
	DeleteLink(ctx context.Context, taskID uint, linkID uint) error

	// Notes
	AddNote(ctx context.Context, taskID uint, text string) (*model.Note, error)
	UpdateNote(ctx context.Context, taskID uint, noteID uint, text string) (*model.Note, error)
	ListNotes(ctx context.Context, taskID uint) ([]model.Note, error)
	DeleteNote(ctx context.Context, taskID uint, noteID uint) error

	// Lifecycle
	Close(ctx context.Context) error
}

// UpdateTaskOptions holds optional fields for updating a task.
// Nil pointer fields are not changed.
type UpdateTaskOptions struct {
	Title       *string
	Description *string
	Priority    *int
	DueAt       *time.Time
	ClearDueAt  bool // if true, set DueAt to nil
}

// ListTasksOptions controls filtering and sorting for ListTasks.
type ListTasksOptions struct {
	IncludeArchived bool
	IncludeSubtasks bool             // false = top-level only; true = all tasks (implied by ParentID)
	ParentID        *uint            // filter to subtree rooted at this task (recursive, includes root)
	State           *model.TaskState // nil = all states
	Tags            []string         // AND logic: task must have all specified tags
	Overdue         bool             // only tasks past due date
	SortBy          string           // "priority" (default), "due", "created", "updated"
	Limit           int
	Offset          int
}

// StoreEvent is emitted by the store after successful mutations.
// Observers (audit logger, vector syncer) receive these events.
type StoreEvent struct {
	Type    string            // e.g., "task.created", "task.updated", "note.created"
	TaskIDs []uint            // affected task IDs
	NoteIDs []uint            // affected note IDs (if applicable)
	Source  string            // "cli", "mcp-stdio", "mcp-http"
	Changes map[string]Change // field -> {old, new} for updates
}

// Change records a before/after value for a field.
type Change struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// StoreObserver receives events after successful store mutations.
type StoreObserver interface {
	OnEvent(ctx context.Context, event StoreEvent)
}

// SemanticSearcher provides vector-based semantic search capabilities.
// Injected as an optional nil-able parameter into the MCP server.
type SemanticSearcher interface {
	SemanticSearch(ctx context.Context, query string, opts SemanticSearchOptions) ([]SemanticSearchResult, error)
	SemanticSearchContext(ctx context.Context, taskID uint, opts SemanticSearchOptions) ([]SemanticSearchResult, error)
}

// SemanticSearchOptions controls semantic search behavior.
type SemanticSearchOptions struct {
	Limit           int    // max results (default 10)
	Type            string // filter by "task", "note", or "" for all
	TaskID          *uint  // filter to a specific task's entities
	IncludeArchived bool   // when false (default), exclude archived items
}

// SemanticSearchResult is a single result from semantic search.
type SemanticSearchResult struct {
	ID       string         // e.g., "task:42", "note:17"
	Text     string         // the embedded text
	Metadata map[string]any // task_id, type, etc.
	Score    float32        // similarity score (higher = more similar)
}
