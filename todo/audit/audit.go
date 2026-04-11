package audit

import (
	"context"
	"log/slog"

	"github.com/csams/todo/store"
)

// Logger is a StoreObserver that logs all store mutations using structured logging.
type Logger struct {
	logger *slog.Logger
}

// NewLogger creates an AuditLogger backed by the given slog.Logger.
func NewLogger(logger *slog.Logger) *Logger {
	return &Logger{logger: logger.With("component", "audit")}
}

// OnEvent logs a store event as a structured info-level log entry.
func (a *Logger) OnEvent(_ context.Context, event store.StoreEvent) {
	attrs := []any{
		"operation", event.Type,
		"source", event.Source,
	}

	if len(event.TaskIDs) > 0 {
		attrs = append(attrs, "task_ids", event.TaskIDs)
	}
	if len(event.NoteIDs) > 0 {
		attrs = append(attrs, "note_ids", event.NoteIDs)
	}
	if len(event.Changes) > 0 {
		for field, change := range event.Changes {
			attrs = append(attrs,
				"change."+field+".old", change.Old,
				"change."+field+".new", change.New,
			)
		}
	}

	a.logger.Info("audit", attrs...)
}

// Compile-time interface check.
var _ store.StoreObserver = (*Logger)(nil)
