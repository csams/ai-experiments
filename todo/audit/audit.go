package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/csams/todo/store"
)

// DefaultValueCap is the per-string-value rune budget when none is
// configured. 256 runes is generous for routine context (state
// transitions, title edits, blocker IDs surfaced as text) and short
// enough that an audit log line stays scannable.
const DefaultValueCap = 256

// Options configures the audit logger's redaction behavior.
type Options struct {
	// ValueCap is the maximum runes of any string value in a Change
	// payload. 0 or negative falls back to DefaultValueCap. Has no
	// effect when FullValues is true.
	ValueCap int

	// FullValues disables truncation entirely. Use only when you need
	// the full content in audit logs (debugging, compliance audits
	// where the log IS the source of truth) and accept the trade-offs:
	// log lines can be megabyte-scale (a 100k-char description
	// edit emits two ~100k attribute values), and any sensitive data
	// pasted into a description / note / checkpoint flows verbatim.
	FullValues bool
}

// Logger is a StoreObserver that logs all store mutations using structured logging.
type Logger struct {
	logger     *slog.Logger
	valueCap   int
	fullValues bool
}

// NewLogger creates an AuditLogger backed by the given slog.Logger.
// Pass Options{} for default behavior (256-rune cap, truncation on).
func NewLogger(logger *slog.Logger, opts Options) *Logger {
	cap := opts.ValueCap
	if cap <= 0 {
		cap = DefaultValueCap
	}
	return &Logger{
		logger:     logger.With("component", "audit"),
		valueCap:   cap,
		fullValues: opts.FullValues,
	}
}

// OnEvent logs a store event as a structured info-level log entry.
func (a *Logger) OnEvent(_ context.Context, event store.StoreEvent) {
	attrs := []any{
		"operation", event.Type,
		"source", event.Source,
	}
	if event.Actor != "" {
		// HTTP MCP transport stamps the matched bearer-auth key label
		// onto the event so multi-client deployments can attribute each
		// mutation to a specific actor. Empty for stdio MCP and CLI.
		attrs = append(attrs, "actor", event.Actor)
	}

	if len(event.TaskIDs) > 0 {
		attrs = append(attrs, "task_ids", event.TaskIDs)
	}
	if len(event.NoteIDs) > 0 {
		attrs = append(attrs, "note_ids", event.NoteIDs)
	}
	if len(event.Changes) > 0 {
		fields := make([]string, 0, len(event.Changes))
		for field := range event.Changes {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			change := event.Changes[field]
			attrs = append(attrs,
				"change."+field+".old", a.redact(change.Old),
				"change."+field+".new", a.redact(change.New),
			)
		}
	}

	a.logger.Info("audit", attrs...)
}

// redact returns v truncated to ValueCap runes plus an elision marker
// when v is an over-cap string. Non-string values (ints, *time.Time,
// nil, booleans, IDs) pass through unchanged because they're naturally
// bounded.
//
// FullValues=true short-circuits to a pass-through, mirroring the
// pre-PR-5 behavior for callers that explicitly opted out of
// truncation.
func (a *Logger) redact(v any) any {
	if a.fullValues {
		return v
	}
	s, ok := v.(string)
	if !ok {
		return v
	}
	total := utf8.RuneCountInString(s)
	if total <= a.valueCap {
		return v
	}
	var b strings.Builder
	// Pre-size for the kept portion plus a generous marker. Marker
	// length is bounded (~30 chars) so an over-cap of ~30 is fine.
	b.Grow(len(s)/total*a.valueCap + 32)
	kept := 0
	for _, r := range s {
		if kept >= a.valueCap {
			break
		}
		b.WriteRune(r)
		kept++
	}
	fmt.Fprintf(&b, "…[truncated, %d more chars]", total-a.valueCap)
	return b.String()
}

// Compile-time interface check.
var _ store.StoreObserver = (*Logger)(nil)
