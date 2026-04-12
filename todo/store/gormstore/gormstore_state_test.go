package gormstore_test

import (
	"errors"
	"testing"

	"github.com/csams/todo/model"
)

func TestSetTaskState_Progressing(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	updated, err := s.SetTaskState(ctx(), task.ID, model.StateProgressing)
	if err != nil {
		t.Fatalf("set state: %v", err)
	}
	if updated.State != model.StateProgressing {
		t.Errorf("state = %q, want %q", updated.State, model.StateProgressing)
	}
}

func TestSetTaskState_BlockedReturnsError(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	_, err := s.SetTaskState(ctx(), task.ID, model.StateBlocked)
	if !errors.Is(err, model.ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got %v", err)
	}
}

func TestSetTaskState_ArchivedReturnsError(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	s.ArchiveTask(ctx(), task.ID, true)

	_, err := s.SetTaskState(ctx(), task.ID, model.StateProgressing)
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("expected ErrArchived, got %v", err)
	}
}

func TestSetTaskState_ClearsBlockerEntries(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	// Manually set B to Progressing — should clear its blocker entries
	_, err := s.SetTaskState(ctx(), b.ID, model.StateProgressing)
	if err != nil {
		t.Fatalf("set state: %v", err)
	}

	detail, _ := s.GetTask(ctx(), b.ID)
	if len(detail.Blockers) != 0 {
		t.Errorf("blockers after state change = %d, want 0", len(detail.Blockers))
	}
}

func TestSetTaskState_DoneCascadeUnblocks(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	// Complete A — should unblock B
	_, err := s.SetTaskState(ctx(), a.ID, model.StateDone)
	if err != nil {
		t.Fatalf("set done: %v", err)
	}

	detail, _ := s.GetTask(ctx(), b.ID)
	if detail.State != model.StateUnblocked {
		t.Errorf("B state = %q, want %q", detail.State, model.StateUnblocked)
	}
	if len(detail.Blockers) != 0 {
		t.Errorf("B blockers = %d, want 0", len(detail.Blockers))
	}
}

func TestAddBlockers_Basic(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)

	result, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	if err != nil {
		t.Fatalf("add blockers: %v", err)
	}
	if result.State != model.StateBlocked {
		t.Errorf("state = %q, want %q", result.State, model.StateBlocked)
	}
	if len(result.Blockers) != 1 {
		t.Errorf("blockers = %d, want 1", len(result.Blockers))
	}
}

func TestAddBlockers_MultipleBlockers(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)
	c, _ := s.CreateTask(ctx(), "C", "", 0, nil, nil)

	s.AddBlockers(ctx(), c.ID, []uint{a.ID, b.ID})

	// Complete A — C should still be Blocked (B still blocks it)
	s.SetTaskState(ctx(), a.ID, model.StateDone)

	detail, _ := s.GetTask(ctx(), c.ID)
	if detail.State != model.StateBlocked {
		t.Errorf("C state = %q, want %q (B still blocks)", detail.State, model.StateBlocked)
	}

	// Complete B — C should now be Unblocked
	s.SetTaskState(ctx(), b.ID, model.StateDone)

	detail, _ = s.GetTask(ctx(), c.ID)
	if detail.State != model.StateUnblocked {
		t.Errorf("C state = %q, want %q", detail.State, model.StateUnblocked)
	}
}

func TestAddBlockers_SelfBlock(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)

	_, err := s.AddBlockers(ctx(), a.ID, []uint{a.ID})
	if err == nil {
		t.Fatal("expected error for self-block")
	}
}

func TestAddBlockers_BlockByDone(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)
	s.SetTaskState(ctx(), a.ID, model.StateDone)

	_, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	if err == nil {
		t.Fatal("expected error for block-by-Done")
	}
}

func TestAddBlockers_BlockByArchived(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)
	s.ArchiveTask(ctx(), a.ID, true)

	_, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	if err == nil {
		t.Fatal("expected error for block-by-archived")
	}
}

func TestAddBlockers_CycleDetection(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)

	// A blocks B
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	// B blocks A — should detect cycle
	_, err := s.AddBlockers(ctx(), a.ID, []uint{b.ID})
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
	var ce *model.CycleDetectedError
	if !errors.As(err, &ce) {
		t.Errorf("expected CycleDetectedError, got %T: %v", err, err)
	}
}

func TestAddBlockers_Idempotent(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)

	s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	// Adding again should not error
	_, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	if err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
}

func TestRemoveBlockers_AutoUnblocks(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	result, err := s.RemoveBlockers(ctx(), b.ID, []uint{a.ID})
	if err != nil {
		t.Fatalf("remove blockers: %v", err)
	}
	if result.State != model.StateUnblocked {
		t.Errorf("state = %q, want %q", result.State, model.StateUnblocked)
	}
}

func TestRemoveBlockers_NonBlockedIsNoop(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)

	_, err := s.RemoveBlockers(ctx(), a.ID, []uint{999})
	if err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
}
