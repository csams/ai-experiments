package gormstore_test

import (
	"testing"

	"github.com/csams/todo/store"
)

// --- Vector-sync dirty-flag accessors ---

func TestVectorDirty_MarkAndList(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "P"})
	n1, _ := s.AddNote(ctx(), &parent.ID, "n1")
	n2, _ := s.AddNote(ctx(), nil, "n2")

	if err := s.MarkVectorDirty(ctx(), []uint{a.ID, b.ID}, []uint{n1.ID, n2.ID}); err != nil {
		t.Fatalf("mark: %v", err)
	}

	taskIDs, noteIDs, err := s.ListVectorDirty(ctx(), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(taskIDs) != 2 {
		t.Errorf("tasks = %v, want 2", taskIDs)
	}
	if len(noteIDs) != 2 {
		t.Errorf("notes = %v, want 2", noteIDs)
	}
}

// TestVectorDirty_ListReturnsBothSlicesNonNil pins the contract that the
// return slices are always non-nil (so callers can iterate without a
// nil-guard).
func TestVectorDirty_ListReturnsBothSlicesNonNil(t *testing.T) {
	s := newTestStore(t)
	taskIDs, noteIDs, err := s.ListVectorDirty(ctx(), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if taskIDs == nil {
		t.Error("taskIDs is nil, want empty non-nil slice")
	}
	if noteIDs == nil {
		t.Error("noteIDs is nil, want empty non-nil slice")
	}
}

func TestVectorDirty_Clear(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})

	if err := s.MarkVectorDirty(ctx(), []uint{a.ID, b.ID}, nil); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := s.ClearVectorDirty(ctx(), []uint{a.ID}, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}

	taskIDs, _, _ := s.ListVectorDirty(ctx(), 100)
	if len(taskIDs) != 1 || taskIDs[0] != b.ID {
		t.Errorf("post-clear dirty tasks = %v, want [%d]", taskIDs, b.ID)
	}
}

func TestVectorDirty_LimitCapsResult(t *testing.T) {
	s := newTestStore(t)
	var ids []uint
	for i := 0; i < 10; i++ {
		task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
		ids = append(ids, task.ID)
	}
	if err := s.MarkVectorDirty(ctx(), ids, nil); err != nil {
		t.Fatalf("mark: %v", err)
	}

	got, _, err := s.ListVectorDirty(ctx(), 3)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("list len = %d, want 3 (limit applied)", len(got))
	}
}

// TestVectorDirty_EmptyMarkAndClearAreNoops asserts the no-input path
// short-circuits without DB work and without erroring.
func TestVectorDirty_EmptyMarkAndClearAreNoops(t *testing.T) {
	s := newTestStore(t)
	if err := s.MarkVectorDirty(ctx(), nil, nil); err != nil {
		t.Errorf("mark empty: %v", err)
	}
	if err := s.ClearVectorDirty(ctx(), nil, nil); err != nil {
		t.Errorf("clear empty: %v", err)
	}
}

// TestVectorDirty_ClearNonexistentIDsIsNoop confirms that clearing IDs
// that no longer exist in the DB (e.g. tasks deleted between mark and
// clear) doesn't error and doesn't somehow leak rows.
func TestVectorDirty_ClearNonexistentIDsIsNoop(t *testing.T) {
	s := newTestStore(t)
	if err := s.ClearVectorDirty(ctx(), []uint{99999}, []uint{88888}); err != nil {
		t.Errorf("clear nonexistent: %v", err)
	}
}
