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
	CreateTask(ctx context.Context, opts CreateTaskOptions) (*model.Task, error)
	GetTask(ctx context.Context, id uint, opts GetTaskOptions) (*model.TaskDetail, error)
	// GetTasks fetches multiple tasks by ID (max 100). Returns results in the
	// caller's input order with duplicate IDs collapsed to first occurrence.
	// Missing IDs are reported in NotFound rather than as an error — only
	// validation failures and DB errors return non-nil error. Reads are
	// non-transactional and emit no StoreEvent.
	GetTasks(ctx context.Context, ids []uint, opts GetTaskOptions) (BatchGetTasksResult, error)
	ListTasks(ctx context.Context, opts ListTasksOptions) ([]model.TaskListItem, error)
	UpdateTask(ctx context.Context, id uint, opts UpdateTaskOptions) (*model.Task, error)
	// SetTaskState transitions a task's state. Cannot set Blocked directly
	// (returns ErrInvalidState — use UpdateBlockers/AddBlockers). When the
	// task is currently Blocked and the target is anything other than Done,
	// outstanding blocker rows are preserved by default and the call returns
	// ErrInvalidState; pass SetTaskStateOptions.ForceClearBlockers=true to
	// drop the blockers as part of the transition. Done is terminal and
	// always clears blocker rows (in both directions) and auto-unblocks
	// dependents whose blocker counts hit zero.
	SetTaskState(ctx context.Context, id uint, state model.TaskState, opts SetTaskStateOptions) (*model.Task, error)
	AddBlockers(ctx context.Context, taskID uint, blockerIDs []uint) (*model.Task, error)
	RemoveBlockers(ctx context.Context, taskID uint, blockerIDs []uint) (*model.Task, error)
	UpdateBlockers(ctx context.Context, taskID uint, add, remove []uint) (*model.Task, error) // combined add+remove in one txn; at least one of add/remove must be non-empty
	SetParent(ctx context.Context, id uint, parentID *uint) error
	ArchiveTask(ctx context.Context, id uint, archived bool) error
	DeleteTask(ctx context.Context, id uint, opts DeleteTaskOptions) error
	SearchTasks(ctx context.Context, query string) ([]model.Task, error)

	// Bulk operations (max 100 IDs per call)
	// BulkUpdateState applies the same SetTaskState semantics across the array
	// in one transaction (see SetTaskState for the blocker-handling rules,
	// including ForceClearBlockers). A single rejected task aborts the whole
	// batch — no partial commit.
	BulkUpdateState(ctx context.Context, ids []uint, state model.TaskState, opts SetTaskStateOptions) ([]model.Task, error)
	BulkUpdatePriority(ctx context.Context, ids []uint, priority int) ([]model.Task, error)
	BulkAddTags(ctx context.Context, ids []uint, tags []string) error
	BulkRemoveTags(ctx context.Context, ids []uint, tags []string) error

	// Tags
	AddTags(ctx context.Context, taskID uint, tags []string) error
	RemoveTags(ctx context.Context, taskID uint, tags []string) error

	// Links
	AddLink(ctx context.Context, taskID uint, linkType model.LinkType, url, description string) (*model.Link, error)
	UpdateLink(ctx context.Context, taskID, linkID uint, opts UpdateLinkOptions) (*model.Link, error)
	ListLinks(ctx context.Context, taskID uint) ([]model.Link, error)
	DeleteLink(ctx context.Context, taskID uint, linkID uint) error

	// Checkpoints — singleton per task ("resume here" bookmark).
	GetCheckpoint(ctx context.Context, taskID uint) (*model.Checkpoint, error)
	SetCheckpoint(ctx context.Context, taskID uint, opts SetCheckpointOptions) (*model.Checkpoint, error)
	DeleteCheckpoint(ctx context.Context, taskID uint) error

	// Notes — taskID nil means standalone (no parent task).
	AddNote(ctx context.Context, taskID *uint, text string) (*model.Note, error)
	UpdateNote(ctx context.Context, noteID uint, opts UpdateNoteOptions) (*model.Note, error)
	ListNotes(ctx context.Context, opts ListNotesOptions) ([]model.Note, error)
	GetNotesByIDs(ctx context.Context, ids []uint) ([]model.Note, error)
	DeleteNote(ctx context.Context, noteID uint) error
	ArchiveNote(ctx context.Context, noteID uint, archived bool) error

	// Lifecycle
	Close(ctx context.Context) error
}

// GetTaskOptions controls which optional fields GetTask loads.
// Empty Include means cheap fields only: id, title, priority, state, archived,
// due_at, parent_id, created_at, updated_at, tags, checkpoint. Opt-in keys:
// "description", "notes", "links", "parent", "children", "blockers", "blocking".
type GetTaskOptions struct {
	Include map[string]bool
}

// BatchGetTasksResult is the return value of Store.GetTasks. Both slices are
// guaranteed non-nil (initialized via make) so JSON serialization renders []
// rather than null at any layer.
type BatchGetTasksResult struct {
	Tasks    []model.TaskDetail `json:"tasks"`     // input order, de-duplicated
	NotFound []uint             `json:"not_found"` // valid IDs that had no matching task
}

// CreateTaskOptions holds the fields for CreateTask. ParentID, if non-nil,
// creates the task as a subtask of that parent (which must exist and not be
// archived). Links, if non-empty, attaches each link inside the same
// transaction so creation is atomic — any validation failure rolls the task
// (and tags) back. Only one task.created event is emitted regardless of
// inline link count.
type CreateTaskOptions struct {
	Title       string
	Description string
	Priority    int
	DueAt       *time.Time
	Tags        []string
	Links       []model.LinkInput
	ParentID    *uint // nil = top-level; non-nil = subtask under this parent
}

// SetTaskStateOptions controls SetTaskState / BulkUpdateState side effects.
//
// ForceClearBlockers, when true, permits transitioning a Blocked task to a
// non-Done state by deleting its outstanding task_blockers rows as part of
// the transaction. Without the flag, such a transition is rejected with
// ErrInvalidState so callers do not silently lose dependency information.
//
// The flag has no effect when:
//   - The target state is Done — Done is terminal and always clears the
//     task's blocker rows (and removes the task from other tasks' blockers,
//     auto-unblocking dependents).
//   - The task is not currently Blocked — there are no blocker rows to
//     preserve, so the flag is a no-op.
type SetTaskStateOptions struct {
	ForceClearBlockers bool
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

// UpdateNoteOptions holds optional fields for updating a note.
// Nil pointer fields and SetTaskID=false leave the corresponding column unchanged.
// To make a note standalone, set SetTaskID=true with TaskID=nil.
// To reparent, set SetTaskID=true with TaskID=&newID.
type UpdateNoteOptions struct {
	Text      *string
	SetTaskID bool
	TaskID    *uint
	Archived  *bool
}

// DeleteTaskOptions controls task deletion behavior.
type DeleteTaskOptions struct {
	Recursive   bool
	DeleteNotes bool // false = orphan notes (set task_id=NULL); true = hard-delete notes
}

// SetCheckpointOptions holds the fields for SetCheckpoint (upsert).
// Recap and NextSteps are required (non-empty after sanitize). OpenThreads
// is optional and may be empty.
type SetCheckpointOptions struct {
	Recap       string
	NextSteps   string
	OpenThreads string
}

// NoteScope selects which notes ListNotes returns when TaskID is nil.
// The zero value (NoteScopeAll) returns attached + standalone notes.
type NoteScope string

const (
	NoteScopeAll        NoteScope = ""           // attached + standalone (default)
	NoteScopeStandalone NoteScope = "standalone" // only orphan notes (task_id IS NULL)
	NoteScopeAttached   NoteScope = "attached"   // only notes with a parent task (task_id IS NOT NULL)
)

// ListNotesOptions controls ListNotes filtering. The zero value returns all
// notes (attached + standalone), excluding archived, ordered by created_at ASC.
//
// When TaskID is non-nil, Scope is ignored — results are scoped to that
// task's notes. When TaskID is nil, Scope selects the breadth of results.
//
// Query, if non-empty, applies a case-insensitive substring filter on the
// note text. It composes (AND) with the scope and archive filters.
//
// Limit caps the result count. Zero means no explicit cap — except when
// Query is non-empty, in which case the store applies a default cap to
// preserve the old SearchNotes behavior.
type ListNotesOptions struct {
	TaskID          *uint     // restrict to this task's notes
	Scope           NoteScope // applied only when TaskID is nil
	Query           string    // optional case-insensitive substring filter on text (max 500 chars)
	IncludeArchived bool      // default false
	Limit           int       // 0 = no cap (or store default when Query is set); >0 = cap
}

// UpdateLinkOptions holds optional fields for updating a link.
// Nil pointer fields are not changed. For Description, &"" explicitly clears
// the field to empty (description is optional). URL must remain non-empty —
// passing &"" is rejected by validateLinkURL.
type UpdateLinkOptions struct {
	Type        *model.LinkType
	URL         *string
	Description *string
}

// ListTasksOptions controls filtering and sorting for ListTasks.
type ListTasksOptions struct {
	IncludeArchived bool
	IncludeSubtasks bool              // false = top-level only; true = all tasks (implied by ParentID)
	ParentID        *uint             // filter to subtree rooted at this task (recursive, includes root)
	States          []model.TaskState // OR logic: task state must match any of these; empty = all states
	Tags            []string          // AND logic: task must have all specified tags
	Overdue         bool              // only tasks past due date
	SortBy          string            // "priority" (default), "due", "created", "updated"
	Limit           int
	Offset          int

	// Due date filters
	HasDueDate *bool      // true = has due date, false = no due date set
	DueBefore  *time.Time // due_at < X (excludes tasks with no due date)
	DueAfter   *time.Time // due_at > X (excludes tasks with no due date)
	DueOn      *time.Time // due_at falls on this calendar day (UTC)

	// Priority filters
	PriorityMin *int // priority >= X (inclusive)
	PriorityMax *int // priority <= X (inclusive)

	// Tag subset filter
	TagsSubsetOf []string // task's tags must all be within this set

	// Query is a case-insensitive substring filter on title, description, and
	// any link description. Empty = no filter; non-empty composes (AND) with
	// all other filters above.
	Query string

	// Optional opt-in fields per item. Empty means cheap fields only
	// (id, title, priority, state, archived, due_at, parent_id, timestamps,
	// tags). Opt-in keys match the list_tasks include enum.
	Include map[string]bool
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

// SemanticSearchResult is a single result from semantic search, aggregated
// across all chunks of one parent doc.
type SemanticSearchResult struct {
	ID       string         // parent doc identifier: "task:42" or "note:17"
	Text     string         // text of the best-scoring chunk (for back-compat)
	Metadata map[string]any // task_id, type, etc.
	Score    float32        // max similarity score across this doc's matched chunks
	Chunks   []ChunkMatch   // every matched chunk, sorted by Score desc
}

// ChunkMatch is one matched chunk within a SemanticSearchResult.
type ChunkMatch struct {
	Text       string
	Score      float32
	ChunkIndex int
}
