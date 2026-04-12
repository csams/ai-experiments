package model

import "time"

// TaskState represents the lifecycle state of a task.
type TaskState string

const (
	StateNew         TaskState = "New"
	StateProgressing TaskState = "Progressing"
	StateBlocked     TaskState = "Blocked"
	StateUnblocked   TaskState = "Unblocked"
	StateDone        TaskState = "Done"
)

// ValidTaskStates is the set of all valid task states.
var ValidTaskStates = map[TaskState]bool{
	StateNew:         true,
	StateProgressing: true,
	StateBlocked:     true,
	StateUnblocked:   true,
	StateDone:        true,
}

// LinkType categorizes external references attached to tasks.
type LinkType string

const (
	LinkJira LinkType = "jira"
	LinkPR   LinkType = "pr"
	LinkURL  LinkType = "url"
)

// ValidLinkTypes is the set of all valid link types.
var ValidLinkTypes = map[LinkType]bool{
	LinkJira: true,
	LinkPR:   true,
	LinkURL:  true,
}

// Task is the core entity in the system.
type Task struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	Title       string     `gorm:"not null;size:512" json:"title"`
	Description string     `json:"description,omitempty"`
	Priority    int        `gorm:"not null;default:0" json:"priority"`
	State       TaskState  `gorm:"not null;default:'New';size:20" json:"state"`
	Archived    bool       `gorm:"not null;default:false" json:"archived"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	ParentID    *uint      `json:"parent_id,omitempty"`
	VectorDirty bool       `gorm:"not null;default:false" json:"-"`

	Parent   *Task     `gorm:"foreignKey:ParentID" json:"parent,omitempty"`
	Children []Task    `gorm:"foreignKey:ParentID" json:"children,omitempty"`
	Blockers []Task    `gorm:"many2many:task_blockers;joinForeignKey:TaskID;joinReferences:BlockerID" json:"blockers,omitempty"`
	Tags     []TaskTag `gorm:"foreignKey:TaskID" json:"tags,omitempty"`
	Links    []Link    `gorm:"constraint:OnDelete:CASCADE" json:"links,omitempty"`
	Notes    []Note    `gorm:"constraint:OnDelete:CASCADE" json:"notes,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskDetail is the enriched return type for GetTask, including computed blocking list.
type TaskDetail struct {
	Task
	Blocking []Task `json:"blocking"` // tasks this one is blocking (computed, not stored)
}

// TaskBlocker is the join table for the many-to-many blocking relationship.
type TaskBlocker struct {
	TaskID    uint `gorm:"primaryKey"`
	BlockerID uint `gorm:"primaryKey"`
}

// TaskTag is the join table for task-to-tag associations.
type TaskTag struct {
	TaskID uint   `gorm:"primaryKey"`
	Tag    string `gorm:"primaryKey;size:100"`
}

// Link is an external reference (JIRA, PR, URL) attached to a task.
type Link struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TaskID    uint      `gorm:"not null;index" json:"task_id"`
	Type      LinkType  `gorm:"not null;size:10" json:"type"`
	URL       string    `gorm:"not null;size:2000" json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// Note is a free-text annotation attached to a task.
type Note struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	TaskID      uint      `gorm:"not null;index" json:"task_id"`
	Text        string    `gorm:"not null" json:"text"`
	VectorDirty bool      `gorm:"not null;default:false" json:"-"`
	CreatedAt   time.Time `json:"created_at"`
}
