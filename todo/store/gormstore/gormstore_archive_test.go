package gormstore_test

import (
	"errors"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestArchive_Basic(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	if err := s.ArchiveTask(ctx(), task.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	detail, _ := s.GetTask(ctx(), task.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if !detail.Archived {
		t.Error("expected archived = true")
	}

	// Should be excluded from default list
	tasks, _ := s.ListTasks(ctx(), store.ListTasksOptions{})
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks in default list, got %d", len(tasks))
	}

	// Should appear with IncludeArchived
	tasks, _ = s.ListTasks(ctx(), store.ListTasksOptions{IncludeArchived: true})
	if len(tasks) != 1 {
		t.Errorf("expected 1 task with IncludeArchived, got %d", len(tasks))
	}
}

func TestArchive_CascadesToSubtree(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	s.SetParent(ctx(), child.ID, &parent.ID)

	if err := s.ArchiveTask(ctx(), parent.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	childDetail, _ := s.GetTask(ctx(), child.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if !childDetail.Archived {
		t.Error("expected child to be archived")
	}
}

func TestArchive_FailsIfBlockingExternal(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), b.ID, []uint{a.ID}) // A blocks B

	err := s.ArchiveTask(ctx(), a.ID, true)
	if err == nil {
		t.Fatal("expected error: A blocks B (external)")
	}
	var be *model.BlockingExternalError
	if !errors.As(err, &be) {
		t.Errorf("expected BlockingExternalError, got %T", err)
	}
}

func TestUnarchive_CascadesToSubtree(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	s.SetParent(ctx(), child.ID, &parent.ID)
	s.ArchiveTask(ctx(), parent.ID, true)

	if err := s.ArchiveTask(ctx(), parent.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}

	parentDetail, _ := s.GetTask(ctx(), parent.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	childDetail, _ := s.GetTask(ctx(), child.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if parentDetail.Archived || childDetail.Archived {
		t.Error("expected both to be unarchived")
	}
}

// TestBulkSetArchived_HappyPath archives three unrelated subtrees in one
// call. Each parent's child is also flagged via the subtree cascade.
func TestBulkSetArchived_HappyPath(t *testing.T) {
	s := newTestStore(t)
	p1, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "P1"})
	c1, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C1"})
	s.SetParent(ctx(), c1.ID, &p1.ID)
	p2, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "P2"})
	p3, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "P3"})

	details, err := s.BulkSetArchived(ctx(), []uint{p1.ID, p2.ID, p3.ID}, true)
	if err != nil {
		t.Fatalf("bulk archive: %v", err)
	}
	if len(details) != 3 {
		t.Fatalf("details len = %d, want 3", len(details))
	}
	// Detail order must match input order.
	wantOrder := []uint{p1.ID, p2.ID, p3.ID}
	for i, d := range details {
		if d.ID != wantOrder[i] {
			t.Errorf("details[%d].ID = %d, want %d", i, d.ID, wantOrder[i])
		}
		if !d.Archived {
			t.Errorf("details[%d] (id=%d) should be Archived", i, d.ID)
		}
	}
	// Subtree cascade — c1 archived too.
	c1Detail, _ := s.GetTask(ctx(), c1.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if !c1Detail.Archived {
		t.Errorf("c1 (child of p1) should be archived via subtree cascade")
	}
}

// TestBulkSetArchived_CrossInputBlockersPermitted — when A blocks B and both
// IDs are in the call, the union-based external check treats this as
// internal. The per-ID loop (pre-PR-3) would have rejected the call.
func TestBulkSetArchived_CrossInputBlockersPermitted(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	if _, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID}); err != nil {
		t.Fatalf("setup AddBlockers: %v", err)
	}

	details, err := s.BulkSetArchived(ctx(), []uint{a.ID, b.ID}, true)
	if err != nil {
		t.Fatalf("bulk archive of A+B (A blocks B): %v", err)
	}
	if len(details) != 2 {
		t.Fatalf("details len = %d, want 2", len(details))
	}
	for _, d := range details {
		if !d.Archived {
			t.Errorf("task %d should be archived", d.ID)
		}
	}
}

// TestBulkSetArchived_RollbackOnMissingID — atomicity: if any one ID in the
// batch fails (here, ID #999999 doesn't exist), the entire transaction
// rolls back. Neither A nor B in this fixture should end up archived.
func TestBulkSetArchived_RollbackOnMissingID(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})

	_, err := s.BulkSetArchived(ctx(), []uint{a.ID, 999999, b.ID}, true)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing ID, got %v", err)
	}

	for _, id := range []uint{a.ID, b.ID} {
		detail, _ := s.GetTask(ctx(), id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if detail.Archived {
			t.Errorf("task %d should not be archived after rollback (got Archived=true)", id)
		}
	}
}

// TestBulkSetArchived_RollbackOnExternalBlocker — when archiving fails the
// external-blocker check, the entire batch rolls back.
func TestBulkSetArchived_RollbackOnExternalBlocker(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	external, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "External"})
	// A blocks `external`; archiving A alongside B (with `external` not in
	// the batch) must fail and leave everything untouched.
	if _, err := s.AddBlockers(ctx(), external.ID, []uint{a.ID}); err != nil {
		t.Fatalf("setup AddBlockers: %v", err)
	}

	_, err := s.BulkSetArchived(ctx(), []uint{a.ID, b.ID}, true)
	var be *model.BlockingExternalError
	if !errors.As(err, &be) {
		t.Fatalf("expected BlockingExternalError, got %v", err)
	}
	for _, id := range []uint{a.ID, b.ID} {
		detail, _ := s.GetTask(ctx(), id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if detail.Archived {
			t.Errorf("task %d should not be archived after rollback (got Archived=true)", id)
		}
	}
}

// TestBulkSetArchived_UnarchiveCrossInputPreservesBlockers — regression test
// for the cross-input cleanup case. Archiving and then symmetrically
// unarchiving an A-blocks-B pair must leave the blocker row intact. (The
// naive cleanup loop would see A.Archived=true during B's iteration and
// drop the row before the trailing Update flips A back to unarchived.)
func TestBulkSetArchived_UnarchiveCrossInputPreservesBlockers(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	if _, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID}); err != nil {
		t.Fatalf("setup AddBlockers: %v", err)
	}
	if _, err := s.BulkSetArchived(ctx(), []uint{a.ID, b.ID}, true); err != nil {
		t.Fatalf("bulk archive: %v", err)
	}

	if _, err := s.BulkSetArchived(ctx(), []uint{a.ID, b.ID}, false); err != nil {
		t.Fatalf("bulk unarchive: %v", err)
	}

	detail, _ := s.GetTask(ctx(), b.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Blockers) != 1 {
		t.Fatalf("B should still be blocked by A after symmetric unarchive; got %d blockers", len(detail.Blockers))
	}
	if detail.Blockers[0].ID != a.ID {
		t.Errorf("B's blocker should be A (id=%d), got id=%d", a.ID, detail.Blockers[0].ID)
	}
	if detail.State != model.StateBlocked {
		t.Errorf("B state = %q, want Blocked", detail.State)
	}
}

// TestBulkSetArchived_Unarchive — bulk unarchive across multiple subtrees
// runs the stale-blocker cleanup loop per task and re-Unblocks any
// previously-Blocked tasks whose blockers are now Done.
func TestBulkSetArchived_Unarchive(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	if err := s.ArchiveTask(ctx(), a.ID, true); err != nil {
		t.Fatalf("archive A: %v", err)
	}
	if err := s.ArchiveTask(ctx(), b.ID, true); err != nil {
		t.Fatalf("archive B: %v", err)
	}

	details, err := s.BulkSetArchived(ctx(), []uint{a.ID, b.ID}, false)
	if err != nil {
		t.Fatalf("bulk unarchive: %v", err)
	}
	if len(details) != 2 {
		t.Fatalf("details len = %d, want 2", len(details))
	}
	for _, d := range details {
		if d.Archived {
			t.Errorf("task %d should be unarchived", d.ID)
		}
	}
}

// TestBulkSetArchived_DedupCollapsesDuplicates — duplicate input IDs collapse
// to first occurrence in the response (matches GetTasks/BulkUpdateState
// dedup semantics).
func TestBulkSetArchived_DedupCollapsesDuplicates(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})

	details, err := s.BulkSetArchived(ctx(), []uint{a.ID, a.ID, a.ID}, true)
	if err != nil {
		t.Fatalf("bulk archive: %v", err)
	}
	if len(details) != 1 {
		t.Errorf("details len = %d, want 1 (duplicates collapsed)", len(details))
	}
}

// TestBulkSetArchived_EmptyRejected — empty ids array is a client-side error.
func TestBulkSetArchived_EmptyRejected(t *testing.T) {
	s := newTestStore(t)
	_, err := s.BulkSetArchived(ctx(), nil, true)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for empty ids, got %v", err)
	}
}

// TestBulkSetArchived_TooManyRejected — > 100 IDs rejected at the boundary.
func TestBulkSetArchived_TooManyRejected(t *testing.T) {
	s := newTestStore(t)
	ids := make([]uint, 101)
	for i := range ids {
		ids[i] = uint(i + 1)
	}
	_, err := s.BulkSetArchived(ctx(), ids, true)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for >100 ids, got %v", err)
	}
}

func TestUnarchive_CleansUpInvalidBlockers(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), a.ID, []uint{b.ID}) // B blocks A

	// Archive A (no external blocking since A doesn't block anything)
	s.ArchiveTask(ctx(), a.ID, true)

	// Complete B while A is archived
	s.SetTaskState(ctx(), b.ID, model.StateDone, store.SetTaskStateOptions{})

	// Unarchive A — should clean up the stale blocker (B is Done)
	if err := s.ArchiveTask(ctx(), a.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}

	detail, _ := s.GetTask(ctx(), a.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Blockers) != 0 {
		t.Errorf("expected stale blocker to be cleaned up, got %d blockers", len(detail.Blockers))
	}
	if detail.State == model.StateBlocked {
		t.Error("expected task to no longer be Blocked after stale blocker cleanup")
	}
}
