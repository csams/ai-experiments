package gormstore_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

// --- BulkUpdateState ---

func TestBulkUpdateState_Done(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task B"})

	results, err := s.BulkUpdateState(ctx(), []uint{a.ID, b.ID}, model.StateDone, store.SetTaskStateOptions{})
	if err != nil {
		t.Fatalf("bulk update state: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.State != model.StateDone {
			t.Errorf("task %d: expected Done, got %s", r.ID, r.State)
		}
	}
}

func TestBulkUpdateState_DoneCascadeUnblocks(t *testing.T) {
	s := newTestStore(t)
	blocker, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Blocker"})
	blocked, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Blocked"})
	s.AddBlockers(ctx(), blocked.ID, []uint{blocker.ID})

	// Complete the blocker via bulk
	_, err := s.BulkUpdateState(ctx(), []uint{blocker.ID}, model.StateDone, store.SetTaskStateOptions{})
	if err != nil {
		t.Fatalf("bulk update state: %v", err)
	}

	detail, _ := s.GetTask(ctx(), blocked.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.State != model.StateUnblocked {
		t.Errorf("expected blocked task to be Unblocked, got %s", detail.State)
	}
}

func TestBulkUpdateState_RejectsBlocked(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	_, err := s.BulkUpdateState(ctx(), []uint{1}, model.StateBlocked, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got %v", err)
	}
}

// TestBulkUpdateState_RejectsUnblocked — bulk-path mirror of
// TestSetTaskState_UnblockedReturnsError: Unblocked is an auto-transition,
// not a settable target.
func TestBulkUpdateState_RejectsUnblocked(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	_, err := s.BulkUpdateState(ctx(), []uint{1}, model.StateUnblocked, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got %v", err)
	}
}

func TestBulkUpdateState_RejectsArchived(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	s.ArchiveTask(ctx(), task.ID, true)

	_, err := s.BulkUpdateState(ctx(), []uint{task.ID}, model.StateDone, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("expected ErrArchived, got %v", err)
	}
}

func TestBulkUpdateState_RejectsNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.BulkUpdateState(ctx(), []uint{999}, model.StateDone, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBulkUpdateState_RejectsExceedsLimit(t *testing.T) {
	s := newTestStore(t)

	ids := make([]uint, 101)
	for i := range ids {
		ids[i] = uint(i + 1)
	}
	_, err := s.BulkUpdateState(ctx(), ids, model.StateDone, store.SetTaskStateOptions{})
	if err == nil {
		t.Fatal("expected error for exceeding bulk limit")
	}
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

// TestBulkUpdateState_RejectsBlockedToProgressingByDefault — mirror of the
// singular-path test in gormstore_state_test.go, but for the bulk path
// (which is the path MCP set_task_state actually takes).
func TestBulkUpdateState_RejectsBlockedToProgressingByDefault(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	_, err := s.BulkUpdateState(ctx(), []uint{b.ID}, model.StateProgressing, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}

	detail, _ := s.GetTask(ctx(), b.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.State != model.StateBlocked {
		t.Errorf("state = %q, want Blocked (rejection should leave state untouched)", detail.State)
	}
	if len(detail.Blockers) != 1 {
		t.Errorf("blockers = %d, want 1 (rejection must preserve dependency rows)", len(detail.Blockers))
	}
}

// TestBulkUpdateState_ForceClearBlockersAtomicAcrossArray — when force is
// passed, the cleared rows are committed alongside the rest of the batch.
// Confirms the side-effect path runs inside the same transaction.
func TestBulkUpdateState_ForceClearBlockersAtomicAcrossArray(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b1, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B1"})
	b2, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B2"})
	s.AddBlockers(ctx(), b1.ID, []uint{a.ID})
	s.AddBlockers(ctx(), b2.ID, []uint{a.ID})

	results, err := s.BulkUpdateState(ctx(), []uint{b1.ID, b2.ID}, model.StateProgressing,
		store.SetTaskStateOptions{ForceClearBlockers: true})
	if err != nil {
		t.Fatalf("bulk update with force: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, r := range results {
		if r.State != model.StateProgressing {
			t.Errorf("task %d: state = %q, want Progressing", r.ID, r.State)
		}
	}

	for _, id := range []uint{b1.ID, b2.ID} {
		detail, _ := s.GetTask(ctx(), id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if len(detail.Blockers) != 0 {
			t.Errorf("task %d: blockers = %d, want 0 (force should clear)", id, len(detail.Blockers))
		}
	}
}

// --- BulkUpdatePriority ---

func TestBulkUpdatePriority_Basic(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task A", Priority: 5})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task B", Priority: 5})

	results, err := s.BulkUpdatePriority(ctx(), []uint{a.ID, b.ID}, 1)
	if err != nil {
		t.Fatalf("bulk update priority: %v", err)
	}
	for _, r := range results {
		if r.Priority != 1 {
			t.Errorf("task %d: expected priority 1, got %d", r.ID, r.Priority)
		}
	}
}

func TestBulkUpdatePriority_ClampsByBlockedTask(t *testing.T) {
	s := newTestStore(t)
	blocker, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Blocker"})
	blocked, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Blocked", Priority: 1})
	s.AddBlockers(ctx(), blocked.ID, []uint{blocker.ID})

	// Try to set blocker priority worse than blocked task
	results, err := s.BulkUpdatePriority(ctx(), []uint{blocker.ID}, 5)
	if err != nil {
		t.Fatalf("bulk update priority: %v", err)
	}
	// Blocker priority should be clamped to blocked task's priority (1)
	if results[0].Priority != 1 {
		t.Errorf("expected clamped priority 1, got %d", results[0].Priority)
	}
}

func TestBulkUpdatePriority_PropagatesUp(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A", Priority: 5})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B", Priority: 5})

	// B is blocked by A → AddBlockers(B, [A])
	if _, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID}); err != nil {
		t.Fatalf("add blockers: %v", err)
	}

	// Update B priority to 0 — should propagate to A (B's blocker)
	_, err := s.BulkUpdatePriority(ctx(), []uint{b.ID}, 0)
	if err != nil {
		t.Fatalf("bulk update priority: %v", err)
	}

	detail, _ := s.GetTask(ctx(), a.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.Priority != 0 {
		t.Errorf("expected blocker priority to be promoted to 0, got %d", detail.Priority)
	}
}

// --- BulkAddTags ---

func TestBulkAddTags_Basic(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task B"})

	err := s.BulkAddTags(ctx(), []uint{a.ID, b.ID}, []string{"urgent", "backend"})
	if err != nil {
		t.Fatalf("bulk add tags: %v", err)
	}

	detailA, _ := s.GetTask(ctx(), a.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	detailB, _ := s.GetTask(ctx(), b.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detailA.Tags) != 2 {
		t.Errorf("task A: expected 2 tags, got %d", len(detailA.Tags))
	}
	if len(detailB.Tags) != 2 {
		t.Errorf("task B: expected 2 tags, got %d", len(detailB.Tags))
	}
}

func TestBulkAddTags_Idempotent(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task", Tags: []string{"existing"}})

	// Adding the same tag again should not error or duplicate
	err := s.BulkAddTags(ctx(), []uint{1}, []string{"existing"})
	if err != nil {
		t.Fatalf("bulk add tags (idempotent): %v", err)
	}

	detail, _ := s.GetTask(ctx(), 1, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Tags) != 1 {
		t.Errorf("expected 1 tag after idempotent add, got %d", len(detail.Tags))
	}
}

func TestBulkAddTags_ExceedsTagLimit(t *testing.T) {
	s := newTestStore(t)
	// Create task with 49 tags
	tags := make([]string, 49)
	for i := range tags {
		tags[i] = fmt.Sprintf("tag%d", i)
	}
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task", Tags: tags})

	// Adding 2 more should exceed the 50-tag limit
	err := s.BulkAddTags(ctx(), []uint{1}, []string{"extra1", "extra2"})
	if err == nil {
		t.Fatal("expected error for exceeding tag limit")
	}
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestBulkAddTags_InvalidTag(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	err := s.BulkAddTags(ctx(), []uint{1}, []string{"invalid tag!"})
	if err == nil {
		t.Fatal("expected error for invalid tag characters")
	}
}

// --- BulkRemoveTags ---

func TestBulkRemoveTags_Basic(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task A", Tags: []string{"remove-me", "keep"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task B", Tags: []string{"remove-me", "keep"}})

	err := s.BulkRemoveTags(ctx(), []uint{1, 2}, []string{"remove-me"})
	if err != nil {
		t.Fatalf("bulk remove tags: %v", err)
	}

	detailA, _ := s.GetTask(ctx(), 1, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	detailB, _ := s.GetTask(ctx(), 2, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	for _, d := range []model.Task{detailA.Task, detailB.Task} {
		if len(d.Tags) != 1 {
			t.Errorf("task %d: expected 1 tag remaining, got %d", d.ID, len(d.Tags))
		}
		if d.Tags[0].Tag != "keep" {
			t.Errorf("task %d: expected remaining tag 'keep', got %q", d.ID, d.Tags[0].Tag)
		}
	}
}

func TestBulkRemoveTags_NonexistentTagSucceeds(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	err := s.BulkRemoveTags(ctx(), []uint{1}, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("expected no error removing nonexistent tag, got %v", err)
	}
}

// --- PR-13/14: input-order events, no caller-slice mutation ---

// TestBulk_DoesNotMutateCallerSlice pins the no-mutation invariant across
// every bulk method. BulkUpdateState's internal sort previously reached
// into the caller's slice and reordered it; that's now done on a private
// copy. The other bulk paths never sorted but used to share their slice
// with the emitted event — that's also fixed (see EventCopy tests).
func TestBulk_DoesNotMutateCallerSlice(t *testing.T) {
	s := newTestStore(t)
	// Three tasks so a sort actually reorders something.
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C"})

	assertUnchanged := func(name string, got, want []uint) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: len changed %d -> %d", name, len(want), len(got))
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: ids[%d] changed %d -> %d (caller slice mutated)", name, i, want[i], got[i])
			}
		}
	}

	run := func(name string, op func(ids []uint) error) {
		ids := []uint{c.ID, a.ID, b.ID} // intentionally non-sorted
		original := append([]uint(nil), ids...)
		if err := op(ids); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertUnchanged(name, ids, original)
	}

	run("BulkUpdateState", func(ids []uint) error {
		_, err := s.BulkUpdateState(ctx(), ids, model.StateProgressing, store.SetTaskStateOptions{})
		return err
	})
	run("BulkUpdatePriority", func(ids []uint) error {
		_, err := s.BulkUpdatePriority(ctx(), ids, 1)
		return err
	})
	run("BulkAddTags", func(ids []uint) error {
		return s.BulkAddTags(ctx(), ids, []string{"x", "y"})
	})
	run("BulkRemoveTags", func(ids []uint) error {
		return s.BulkRemoveTags(ctx(), ids, []string{"x", "y"})
	})
}

// TestBulkUpdateState_EmitsInputOrderPlusExtras pins the audit-event
// convention: the emitted TaskIDs match the caller's input order, with
// any side-effect IDs (auto-Unblocked dependents from a Done cascade)
// appended after.
func TestBulkUpdateState_EmitsInputOrderPlusExtras(t *testing.T) {
	s := newTestStore(t)
	obs := &recordingObserver{}
	s.AddObserver(obs)

	// Inputs in non-ascending order so a sort would reshuffle them.
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C"})
	input := []uint{c.ID, a.ID, b.ID}

	if _, err := s.BulkUpdateState(ctx(), input, model.StateProgressing, store.SetTaskStateOptions{}); err != nil {
		t.Fatalf("bulk update: %v", err)
	}

	// Find the bulk_state_changed event.
	var got []uint
	for _, e := range obs.events {
		if e.Type == "task.bulk_state_changed" {
			got = e.TaskIDs
			break
		}
	}
	if got == nil {
		t.Fatal("task.bulk_state_changed event not emitted")
	}
	if len(got) != len(input) {
		t.Fatalf("event len = %d, want %d (no extras expected on Progressing transition)", len(got), len(input))
	}
	for i := range got {
		if got[i] != input[i] {
			t.Errorf("event TaskIDs[%d] = %d, want %d (input order preserved)", i, got[i], input[i])
		}
	}
}

// TestBulkUpdateState_EventCopyIndependentOfCallerSlice — the emitted
// event must hold its own copy, so a caller that mutates `ids` after
// the call can't reach into the audit log.
func TestBulkUpdateState_EventCopyIndependentOfCallerSlice(t *testing.T) {
	s := newTestStore(t)
	obs := &recordingObserver{}
	s.AddObserver(obs)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C"})

	// Non-sorted input so any accidental aliasing through
	// `inputIDs := ids[:0:0]` + append (which would share storage when
	// the allocator can extend in place) would surface in the assertion.
	ids := []uint{c.ID, a.ID, b.ID}
	if _, err := s.BulkUpdateState(ctx(), ids, model.StateProgressing, store.SetTaskStateOptions{}); err != nil {
		t.Fatalf("bulk update: %v", err)
	}

	// Mutate ids post-call.
	ids[0] = 99999

	var evt store.StoreEvent
	for _, e := range obs.events {
		if e.Type == "task.bulk_state_changed" {
			evt = e
		}
	}
	if evt.TaskIDs[0] == 99999 {
		t.Error("event TaskIDs shares storage with caller slice (mutation visible in audit log)")
	}
}

// TestBulkUpdateState_DoneCascadeEventOrder pins the "extras appended
// after" half of the input-order convention: a Done transition on a
// blocker auto-Unblocks its dependent. The emitted event must carry
// the input IDs verbatim (caller order) followed by the dependent's
// ID at the end.
func TestBulkUpdateState_DoneCascadeEventOrder(t *testing.T) {
	s := newTestStore(t)

	// Three blockers, intentionally created in non-ascending order so
	// the input slice is unsorted and a leaked internal sort would be
	// detectable in the assertion.
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C-blocker"})
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A-blocker"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B-blocker"})

	// Dependent task blocked by ALL three. Completing all three at once
	// triggers a single auto-Unblocked transition for the dependent.
	dep, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "dependent"})
	if _, err := s.AddBlockers(ctx(), dep.ID, []uint{a.ID, b.ID, c.ID}); err != nil {
		t.Fatalf("setup AddBlockers: %v", err)
	}

	// Attach observer AFTER setup so the event slice only contains the
	// bulk call's emissions.
	obs := &recordingObserver{}
	s.AddObserver(obs)

	input := []uint{c.ID, a.ID, b.ID}
	if _, err := s.BulkUpdateState(ctx(), input, model.StateDone, store.SetTaskStateOptions{}); err != nil {
		t.Fatalf("bulk done: %v", err)
	}

	// Find the bulk_state_changed event.
	var got []uint
	for _, e := range obs.events {
		if e.Type == "task.bulk_state_changed" {
			got = e.TaskIDs
		}
	}
	if got == nil {
		t.Fatal("task.bulk_state_changed not emitted")
	}

	// Leading prefix matches caller order verbatim.
	if len(got) < len(input) {
		t.Fatalf("event len = %d, want >= %d (input + extras)", len(got), len(input))
	}
	for i := range input {
		if got[i] != input[i] {
			t.Errorf("event TaskIDs[%d] = %d, want %d (input prefix must match caller order verbatim)", i, got[i], input[i])
		}
	}

	// Trailing extras contain the dependent's ID.
	extras := got[len(input):]
	foundDep := false
	for _, id := range extras {
		if id == dep.ID {
			foundDep = true
		}
	}
	if !foundDep {
		t.Errorf("auto-Unblocked dependent %d not in event extras: %v", dep.ID, extras)
	}
}

// TestBulkUpdatePriority_EmitsInputOrder mirrors the input-order check
// for the priority path.
func TestBulkUpdatePriority_EmitsInputOrder(t *testing.T) {
	s := newTestStore(t)
	obs := &recordingObserver{}
	s.AddObserver(obs)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C"})
	input := []uint{c.ID, a.ID, b.ID}

	if _, err := s.BulkUpdatePriority(ctx(), input, 2); err != nil {
		t.Fatalf("bulk priority: %v", err)
	}

	var got []uint
	for _, e := range obs.events {
		if e.Type == "task.bulk_priority_changed" {
			got = e.TaskIDs
		}
	}
	if got == nil {
		t.Fatal("task.bulk_priority_changed not emitted")
	}
	for i := range got {
		if got[i] != input[i] {
			t.Errorf("event TaskIDs[%d] = %d, want %d (input order)", i, got[i], input[i])
		}
	}
}

// TestBulkAddTags_EmitsInputOrder and the Remove twin — confirm both
// tag-bulk paths use input order too.
func TestBulkAddTags_EmitsInputOrder(t *testing.T) {
	s := newTestStore(t)
	obs := &recordingObserver{}
	s.AddObserver(obs)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C"})
	input := []uint{c.ID, a.ID, b.ID}

	if err := s.BulkAddTags(ctx(), input, []string{"x"}); err != nil {
		t.Fatalf("bulk add tags: %v", err)
	}

	var got []uint
	for _, e := range obs.events {
		if e.Type == "task.tags_changed" {
			got = e.TaskIDs
		}
	}
	if got == nil {
		t.Fatal("task.tags_changed not emitted")
	}
	for i := range got {
		if got[i] != input[i] {
			t.Errorf("event TaskIDs[%d] = %d, want %d (input order)", i, got[i], input[i])
		}
	}
}

func TestBulkRemoveTags_EmitsInputOrder(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A", Tags: []string{"x"}})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B", Tags: []string{"x"}})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C", Tags: []string{"x"}})

	obs := &recordingObserver{}
	s.AddObserver(obs)
	input := []uint{c.ID, a.ID, b.ID}

	if err := s.BulkRemoveTags(ctx(), input, []string{"x"}); err != nil {
		t.Fatalf("bulk remove tags: %v", err)
	}

	var got []uint
	for _, e := range obs.events {
		if e.Type == "task.tags_changed" {
			got = e.TaskIDs
		}
	}
	if got == nil {
		t.Fatal("task.tags_changed not emitted")
	}
	for i := range got {
		if got[i] != input[i] {
			t.Errorf("event TaskIDs[%d] = %d, want %d (input order)", i, got[i], input[i])
		}
	}
}
