package model

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors — use errors.Is() for matching.
//
// The plain sentinels (ErrNotFound / ErrArchived / ErrInvalidState) are
// returned wrapped via fmt.Errorf("...: %w", model.ErrXxx) at the
// callsite. The structured-error sentinels below
// (ErrValidation / ErrBlockingExternal / ErrCycle) pair with the typed
// errors below them: callers can either errors.Is(err, ErrCycle) for a
// category check or errors.As(err, &ce) when they want the structured
// payload (path, IDs, etc.).
var (
	ErrNotFound         = errors.New("not found")
	ErrArchived         = errors.New("operation not permitted on archived task")
	ErrInvalidState     = errors.New("invalid state transition")
	ErrValidation       = errors.New("validation error")
	ErrBlockingExternal = errors.New("task blocks an external task")
	ErrCycle            = errors.New("cycle detected")
)

// ValidationError indicates a field-level validation failure.
//
// Wraps ErrValidation so callers can either errors.Is(err, ErrValidation)
// for a generic category check (e.g. mapping to HTTP 400) or
// errors.As(err, &ve) when they need Field / Message for a user-facing
// error message.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error on %s: %s", e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

// BlockingExternalError indicates a task blocks something outside the
// affected set. Wraps ErrBlockingExternal.
type BlockingExternalError struct {
	BlockingTaskID uint
	BlockedTaskID  uint
}

func (e *BlockingExternalError) Error() string {
	return fmt.Sprintf("task %d is blocking task %d which is outside the affected set", e.BlockingTaskID, e.BlockedTaskID)
}

func (e *BlockingExternalError) Unwrap() error { return ErrBlockingExternal }

// CycleDetectedError indicates a cycle in blocking or parent
// relationships. Wraps ErrCycle.
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

func (e *CycleDetectedError) Unwrap() error { return ErrCycle }
