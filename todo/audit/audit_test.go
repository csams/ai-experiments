package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/csams/todo/store"
)

// captureLogger builds an audit.Logger that writes JSON entries into the
// returned buffer so tests can decode them and inspect per-field shape.
func captureLogger(t *testing.T, opts Options) (*Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	sl := slog.New(slog.NewJSONHandler(&buf, nil))
	return NewLogger(sl, opts), &buf
}

// emitAndDecode fires one event through the audit logger and decodes the
// single JSON record it produces.
func emitAndDecode(t *testing.T, lg *Logger, buf *bytes.Buffer, event store.StoreEvent) map[string]any {
	t.Helper()
	buf.Reset()
	lg.OnEvent(context.Background(), event)
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode audit JSON: %v\nraw: %s", err, buf.String())
	}
	return rec
}

func TestRedact_StringOverCapTruncatedWithMarker(t *testing.T) {
	lg, buf := captureLogger(t, Options{ValueCap: 32})
	longText := strings.Repeat("a", 200) // 200 runes, over the 32 cap

	rec := emitAndDecode(t, lg, buf, store.StoreEvent{
		Type:    "task.updated",
		TaskIDs: []uint{1},
		Changes: map[string]store.Change{
			"description": {Old: "", New: longText},
		},
	})

	got, ok := rec["change.description.new"].(string)
	if !ok {
		t.Fatalf("change.description.new missing or wrong type; rec=%v", rec)
	}
	if !strings.Contains(got, "[truncated, 168 more chars]") {
		t.Errorf("missing elision marker; got %q", got)
	}
	// Inspect only the content prefix (everything before the ellipsis
	// marker); counting 'a' across the full string is misleading
	// because the marker contains the letter too ("trunc-a-ted",
	// "ch-a-rs").
	marker := strings.Index(got, "…")
	if marker < 0 {
		t.Fatalf("ellipsis marker missing; got %q", got)
	}
	prefix := got[:marker]
	if strings.Count(prefix, "a") != 32 {
		t.Errorf("kept %d 'a' runes in prefix, want 32; prefix=%q", strings.Count(prefix, "a"), prefix)
	}
	// The full 200-character original must NOT appear.
	if strings.Contains(got, longText) {
		t.Error("full original string leaked into audit log")
	}
}

func TestRedact_StringUnderCapPassesThrough(t *testing.T) {
	lg, buf := captureLogger(t, Options{ValueCap: 100})
	short := "Progressing"

	rec := emitAndDecode(t, lg, buf, store.StoreEvent{
		Type:    "task.state_changed",
		TaskIDs: []uint{1},
		Changes: map[string]store.Change{
			"state": {Old: "New", New: short},
		},
	})

	if got := rec["change.state.new"]; got != short {
		t.Errorf("short string mutated: got %v, want %q", got, short)
	}
	if s, _ := rec["change.state.new"].(string); strings.Contains(s, "[truncated") {
		t.Errorf("under-cap value should not carry a truncation marker; got %q", s)
	}
}

func TestRedact_NonStringValuesPassThrough(t *testing.T) {
	lg, buf := captureLogger(t, Options{ValueCap: 2})
	due := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

	rec := emitAndDecode(t, lg, buf, store.StoreEvent{
		Type:    "task.updated",
		TaskIDs: []uint{1},
		Changes: map[string]store.Change{
			"priority": {Old: 5, New: 1},
			"due_at":   {Old: nil, New: &due},
		},
	})

	// JSON-decoded numbers come back as float64.
	if v, ok := rec["change.priority.new"].(float64); !ok || v != 1 {
		t.Errorf("priority.new = %v (%T), want 1", rec["change.priority.new"], rec["change.priority.new"])
	}
	if v, ok := rec["change.priority.old"].(float64); !ok || v != 5 {
		t.Errorf("priority.old = %v, want 5", rec["change.priority.old"])
	}
	// Time value is not a string — should not be truncated to "ti".
	if v, _ := rec["change.due_at.new"].(string); v == "ti" {
		t.Errorf("time value was treated as string and truncated: %q", v)
	}
}

func TestRedact_FullValuesDisablesTruncation(t *testing.T) {
	lg, buf := captureLogger(t, Options{ValueCap: 16, FullValues: true})
	longText := strings.Repeat("Q", 500)

	rec := emitAndDecode(t, lg, buf, store.StoreEvent{
		Type:    "task.updated",
		TaskIDs: []uint{1},
		Changes: map[string]store.Change{
			"description": {Old: "", New: longText},
		},
	})

	got, _ := rec["change.description.new"].(string)
	if got != longText {
		t.Errorf("FullValues=true should disable truncation; got length %d, want %d",
			len(got), len(longText))
	}
	if strings.Contains(got, "[truncated") {
		t.Error("FullValues=true must not emit a truncation marker")
	}
}

func TestRedact_DefaultCapApplied(t *testing.T) {
	// ValueCap=0 should fall back to DefaultValueCap (256).
	lg, buf := captureLogger(t, Options{})

	longText := strings.Repeat("z", 300)
	rec := emitAndDecode(t, lg, buf, store.StoreEvent{
		Type:    "task.updated",
		TaskIDs: []uint{1},
		Changes: map[string]store.Change{
			"description": {Old: "", New: longText},
		},
	})

	got, _ := rec["change.description.new"].(string)
	if !strings.Contains(got, "[truncated, 44 more chars]") {
		t.Errorf("expected truncation at default 256 cap; got %q", got)
	}
}

func TestRedact_UnicodeAwareTruncation(t *testing.T) {
	// 100 emoji = 100 runes (each 4 bytes). The cap is in RUNES, not
	// bytes, so a 100-rune string with cap=64 should keep exactly 64
	// emoji and drop 36 with the marker.
	const flag = "🚩"
	longText := strings.Repeat(flag, 100)

	lg, buf := captureLogger(t, Options{ValueCap: 64})
	rec := emitAndDecode(t, lg, buf, store.StoreEvent{
		Type:    "task.updated",
		TaskIDs: []uint{1},
		Changes: map[string]store.Change{
			"description": {Old: "", New: longText},
		},
	})

	got, _ := rec["change.description.new"].(string)
	flagCount := strings.Count(got, flag)
	if flagCount != 64 {
		t.Errorf("kept %d flags, want 64 (rune-based cap)", flagCount)
	}
	if !strings.Contains(got, "[truncated, 36 more chars]") {
		t.Errorf("missing marker; got %q", got)
	}
}

func TestRedact_StructuralAttrsUntouched(t *testing.T) {
	// The non-Change attributes (operation, source, task_ids, note_ids)
	// must always pass through verbatim.
	lg, buf := captureLogger(t, Options{ValueCap: 4})

	rec := emitAndDecode(t, lg, buf, store.StoreEvent{
		Type:    "task.bulk_state_changed",
		Source:  "mcp-http",
		TaskIDs: []uint{42, 99, 7},
		NoteIDs: []uint{1, 2},
	})

	if got := rec["operation"]; got != "task.bulk_state_changed" {
		t.Errorf("operation truncated: %v", got)
	}
	if got := rec["source"]; got != "mcp-http" {
		t.Errorf("source = %v, want mcp-http", got)
	}
	// task_ids decoded as a JSON array of numbers.
	if arr, ok := rec["task_ids"].([]any); !ok || len(arr) != 3 {
		t.Errorf("task_ids = %v, want length-3 array", rec["task_ids"])
	}
	if arr, ok := rec["note_ids"].([]any); !ok || len(arr) != 2 {
		t.Errorf("note_ids = %v, want length-2 array", rec["note_ids"])
	}
}

func TestRedact_NegativeCapFallsBackToDefault(t *testing.T) {
	// A negative ValueCap is treated as "use default 256," not as
	// "disabled" — disabling requires the explicit FullValues=true.
	lg, buf := captureLogger(t, Options{ValueCap: -1})
	longText := strings.Repeat("x", 1000)

	rec := emitAndDecode(t, lg, buf, store.StoreEvent{
		Type:    "task.updated",
		TaskIDs: []uint{1},
		Changes: map[string]store.Change{
			"description": {Old: "", New: longText},
		},
	})

	got, _ := rec["change.description.new"].(string)
	if !strings.Contains(got, "[truncated, 744 more chars]") {
		t.Errorf("negative ValueCap should fall back to 256; got %q",
			got)
	}
}
