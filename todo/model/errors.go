package model

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors — use errors.Is() for matching.
var (
	ErrNotFound     = errors.New("not found")
	ErrArchived     = errors.New("operation not permitted on archived task")
	ErrInvalidState = errors.New("invalid state transition")
)

// ValidationError indicates a field-level validation failure.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error on %s: %s", e.Field, e.Message)
}

// BlockingExternalError indicates a task blocks something outside the affected set.
type BlockingExternalError struct {
	BlockingTaskID uint
	BlockedTaskID  uint
}

func (e *BlockingExternalError) Error() string {
	return fmt.Sprintf("task %d is blocking task %d which is outside the affected set", e.BlockingTaskID, e.BlockedTaskID)
}

// CycleDetectedError indicates a cycle in blocking or parent relationships.
type CycleDetectedError struct {
	Path []uint
}

func (e *CycleDetectedError) Error() string {
	parts := make([]string, len(e.Path))
	for i, id := range e.Path {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return fmt.Sprintf("cycle detected: %s", strings.Join(parts, " → "))
}
