package gormstore_test

import (
	"errors"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestSetTaskState_Progressing(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	updated, err := s.SetTaskState(ctx(), task.ID, model.StateProgressing, store.SetTaskStateOptions{})
	if err != nil {
		t.Fatalf("set state: %v", err)
	}
	if updated.State != model.StateProgressing {
		t.Errorf("state = %q, want %q", updated.State, model.StateProgressing)
	}
}

func TestSetTaskState_BlockedReturnsError(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	_, err := s.SetTaskState(ctx(), task.ID, model.StateBlocked, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got %v", err)
	}
}

// TestSetTaskState_UnblockedReturnsError — Unblocked is an auto-transition
// fired when a Blocked task's last blocker is removed. It is not a settable
// target; the store rejects it regardless of the current state.
func TestSetTaskState_UnblockedReturnsError(t *testing.T) {
	s := newTestStore(t)
	// Task in default New state: Unblocked target must still be rejected.
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	_, err := s.SetTaskState(ctx(), a.ID, model.StateUnblocked, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Errorf("New -> Unblocked: expected ErrInvalidState, got %v", err)
	}

	// Even a task that *is* currently Blocked can't be directly set Unblocked:
	// the user should remove blockers (which auto-Unblocks) or pass through
	// the force_clear_blockers path on Progressing/New.
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	blocker, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "blocker"})
	if _, err := s.AddBlockers(ctx(), b.ID, []uint{blocker.ID}); err != nil {
		t.Fatalf("setup AddBlockers: %v", err)
	}
	_, err = s.SetTaskState(ctx(), b.ID, model.StateUnblocked,
		store.SetTaskStateOptions{ForceClearBlockers: true})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Errorf("Blocked -> Unblocked (even with force): expected ErrInvalidState, got %v", err)
	}
}

func TestSetTaskState_ArchivedReturnsError(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	s.ArchiveTask(ctx(), task.ID, true)

	_, err := s.SetTaskState(ctx(), task.ID, model.StateProgressing, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("expected ErrArchived, got %v", err)
	}
}

// TestSetTaskState_RejectsBlockedToProgressingByDefault pins the
// pre-PR-1-removed footgun: a Blocked task transitioning to a non-Done
// state used to silently delete its blocker rows. The new default is to
// reject the call so callers don't lose dependency information by
// accident.
func TestSetTaskState_RejectsBlockedToProgressingByDefault(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	_, err := s.SetTaskState(ctx(), b.ID, model.StateProgressing, store.SetTaskStateOptions{})
	if !errors.Is(err, model.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}

	// Blocker row must still be present after the rejected call.
	detail, _ := s.GetTask(ctx(), b.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.State != model.StateBlocked {
		t.Errorf("state = %q, want Blocked (rejection should not change state)", detail.State)
	}
	if len(detail.Blockers) != 1 {
		t.Errorf("blockers = %d, want 1 (rejection must preserve dependency rows)", len(detail.Blockers))
	}
}

// TestSetTaskState_ForceClearsBlockersOnNonDoneTransition is the opt-in
// path: with ForceClearBlockers=true the caller explicitly accepts the
// data loss and the call proceeds.
func TestSetTaskState_ForceClearsBlockersOnNonDoneTransition(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	updated, err := s.SetTaskState(ctx(), b.ID, model.StateProgressing,
		store.SetTaskStateOptions{ForceClearBlockers: true})
	if err != nil {
		t.Fatalf("set state with force: %v", err)
	}
	if updated.State != model.StateProgressing {
		t.Errorf("state = %q, want Progressing", updated.State)
	}

	detail, _ := s.GetTask(ctx(), b.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Blockers) != 0 {
		t.Errorf("blockers after forced clear = %d, want 0", len(detail.Blockers))
	}
}

// TestSetTaskState_DoneClearsBlockersUnconditionally — Done is terminal
// and clearing blocker rows on the way out is expected behavior, not a
// hidden side effect. force is not required.
func TestSetTaskState_DoneClearsBlockersUnconditionally(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	updated, err := s.SetTaskState(ctx(), b.ID, model.StateDone, store.SetTaskStateOptions{})
	if err != nil {
		t.Fatalf("set done: %v", err)
	}
	if updated.State != model.StateDone {
		t.Errorf("state = %q, want Done", updated.State)
	}

	detail, _ := s.GetTask(ctx(), b.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Blockers) != 0 {
		t.Errorf("blockers after Done = %d, want 0", len(detail.Blockers))
	}
}

// TestSetTaskState_NonBlockedTransitionLeavesNoSideEffect — a New or
// Progressing task transitioning between non-Done states never touches
// the blocker tables (there shouldn't be any rows to touch).
func TestSetTaskState_NonBlockedTransitionLeavesNoSideEffect(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})

	if _, err := s.SetTaskState(ctx(), a.ID, model.StateProgressing, store.SetTaskStateOptions{}); err != nil {
		t.Fatalf("New -> Progressing: %v", err)
	}
	if _, err := s.SetTaskState(ctx(), a.ID, model.StateNew, store.SetTaskStateOptions{}); err != nil {
		t.Fatalf("Progressing -> New: %v", err)
	}
}

func TestSetTaskState_DoneCascadeUnblocks(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	// Complete A — should unblock B
	_, err := s.SetTaskState(ctx(), a.ID, model.StateDone, store.SetTaskStateOptions{})
	if err != nil {
		t.Fatalf("set done: %v", err)
	}

	detail, _ := s.GetTask(ctx(), b.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.State != model.StateUnblocked {
		t.Errorf("B state = %q, want %q", detail.State, model.StateUnblocked)
	}
	if len(detail.Blockers) != 0 {
		t.Errorf("B blockers = %d, want 0", len(detail.Blockers))
	}
}

func TestAddBlockers_Basic(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})

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
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C"})

	s.AddBlockers(ctx(), c.ID, []uint{a.ID, b.ID})

	// Complete A — C should still be Blocked (B still blocks it)
	s.SetTaskState(ctx(), a.ID, model.StateDone, store.SetTaskStateOptions{})

	detail, _ := s.GetTask(ctx(), c.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.State != model.StateBlocked {
		t.Errorf("C state = %q, want %q (B still blocks)", detail.State, model.StateBlocked)
	}

	// Complete B — C should now be Unblocked
	s.SetTaskState(ctx(), b.ID, model.StateDone, store.SetTaskStateOptions{})

	detail, _ = s.GetTask(ctx(), c.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.State != model.StateUnblocked {
		t.Errorf("C state = %q, want %q", detail.State, model.StateUnblocked)
	}
}

func TestAddBlockers_SelfBlock(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})

	_, err := s.AddBlockers(ctx(), a.ID, []uint{a.ID})
	if err == nil {
		t.Fatal("expected error for self-block")
	}
}

func TestAddBlockers_BlockByDone(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.SetTaskState(ctx(), a.ID, model.StateDone, store.SetTaskStateOptions{})

	_, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	if err == nil {
		t.Fatal("expected error for block-by-Done")
	}
}

func TestAddBlockers_BlockByArchived(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.ArchiveTask(ctx(), a.ID, true)

	_, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	if err == nil {
		t.Fatal("expected error for block-by-archived")
	}
}

func TestAddBlockers_CycleDetection(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})

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
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})

	s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	// Adding again should not error
	_, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID})
	if err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
}

func TestRemoveBlockers_AutoUnblocks(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
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
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})

	_, err := s.RemoveBlockers(ctx(), a.ID, []uint{999})
	if err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
}

func TestUpdateBlockers_AddAndRemoveInOneTxn(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C"})
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	if _, err := s.AddBlockers(ctx(), target.ID, []uint{a.ID, b.ID}); err != nil {
		t.Fatalf("setup AddBlockers: %v", err)
	}

	// Swap A out for C in a single transaction.
	result, err := s.UpdateBlockers(ctx(), target.ID, []uint{c.ID}, []uint{a.ID})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if result.State != model.StateBlocked {
		t.Errorf("state = %q, want Blocked", result.State)
	}
	got := map[uint]bool{}
	for _, blocker := range result.Blockers {
		got[blocker.ID] = true
	}
	if got[a.ID] || !got[b.ID] || !got[c.ID] {
		t.Errorf("blockers after swap: %+v; want {B,C} only", got)
	}
}

func TestUpdateBlockers_RemovalOnlyAutoUnblocks(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	s.AddBlockers(ctx(), target.ID, []uint{a.ID})

	result, err := s.UpdateBlockers(ctx(), target.ID, nil, []uint{a.ID})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if result.State != model.StateUnblocked {
		t.Errorf("state = %q, want Unblocked", result.State)
	}
}

func TestUpdateBlockers_AddOnlyEntersBlocked(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})

	result, err := s.UpdateBlockers(ctx(), target.ID, []uint{a.ID}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if result.State != model.StateBlocked {
		t.Errorf("state = %q, want Blocked", result.State)
	}
}

func TestUpdateBlockers_EmptyBothRejected(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})

	_, err := s.UpdateBlockers(ctx(), a.ID, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError for empty add+remove, got %v", err)
	}
}

func TestUpdateBlockers_CycleRejected(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	// A blocks B
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	// Try to make B block A → cycle.
	_, err := s.UpdateBlockers(ctx(), a.ID, []uint{b.ID}, nil)
	var ce *model.CycleDetectedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CycleDetectedError, got %v", err)
	}
}

func TestUpdateBlockers_PromotesBlockerPriority(t *testing.T) {
	s := newTestStore(t)
	low, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "low", Priority: 10})
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "target", Priority: 1})

	if _, err := s.UpdateBlockers(ctx(), target.ID, []uint{low.ID}, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	detail, _ := s.GetTask(ctx(), low.ID, store.GetTaskOptions{})
	if detail.Priority > 1 {
		t.Errorf("blocker priority should be promoted to ≤ 1, got %d", detail.Priority)
	}
}

func TestUpdateBlockers_ArchivedTargetRejected(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	if err := s.ArchiveTask(ctx(), target.ID, true); err != nil {
		t.Fatalf("archive setup: %v", err)
	}

	_, err := s.UpdateBlockers(ctx(), target.ID, []uint{a.ID}, nil)
	if !errors.Is(err, model.ErrArchived) {
		t.Fatalf("expected ErrArchived, got %v", err)
	}
}

func TestUpdateBlockers_DoneBlockerRejected(t *testing.T) {
	s := newTestStore(t)
	done, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "done"})
	if _, err := s.SetTaskState(ctx(), done.ID, model.StateDone, store.SetTaskStateOptions{}); err != nil {
		t.Fatalf("set done: %v", err)
	}
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})

	_, err := s.UpdateBlockers(ctx(), target.ID, []uint{done.ID}, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError for Done blocker, got %v", err)
	}
}

func TestUpdateBlockers_ArchivedBlockerRejected(t *testing.T) {
	s := newTestStore(t)
	archived, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "archived"})
	if err := s.ArchiveTask(ctx(), archived.ID, true); err != nil {
		t.Fatalf("archive setup: %v", err)
	}
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})

	_, err := s.UpdateBlockers(ctx(), target.ID, []uint{archived.ID}, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError for archived blocker, got %v", err)
	}
}

func TestUpdateBlockers_SelfBlockRejected(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})

	_, err := s.UpdateBlockers(ctx(), a.ID, []uint{a.ID}, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError for self-block, got %v", err)
	}
}

func TestUpdateBlockers_BlockerNotFound(t *testing.T) {
	s := newTestStore(t)
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})

	_, err := s.UpdateBlockers(ctx(), target.ID, []uint{999999}, nil)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing blocker, got %v", err)
	}
}

// TestUpdateBlockers_OverlappingAddAndRemoveAddWins pins the documented
// ordering semantic: when the same ID appears in both `add` and `remove`,
// removals are processed first so the add re-creates the row. The blocker
// ends up present after the call.
func TestUpdateBlockers_OverlappingAddAndRemoveAddWins(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	if _, err := s.AddBlockers(ctx(), target.ID, []uint{a.ID}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	result, err := s.UpdateBlockers(ctx(), target.ID, []uint{a.ID}, []uint{a.ID})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if result.State != model.StateBlocked {
		t.Errorf("state = %q, want Blocked (add wins for overlapping IDs)", result.State)
	}
	var got []uint
	for _, blocker := range result.Blockers {
		got = append(got, blocker.ID)
	}
	if len(got) != 1 || got[0] != a.ID {
		t.Errorf("blockers = %v, want [%d] (add wins for overlapping IDs)", got, a.ID)
	}
}

// TestUpdateBlockers_StructuralCycleStillRejected confirms that the
// removals-first ordering doesn't bypass legitimate cycle detection. If
// the proposed add would form a cycle independent of any removal, the
// transaction must still abort.
func TestUpdateBlockers_StructuralCycleStillRejected(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	target, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	if _, err := s.AddBlockers(ctx(), target.ID, []uint{a.ID}); err != nil {
		t.Fatalf("setup A→T: %v", err)
	}
	// Make B blocked by T so adding B as T's blocker would close B→T→B.
	if _, err := s.AddBlockers(ctx(), b.ID, []uint{target.ID}); err != nil {
		t.Fatalf("setup T→B: %v", err)
	}

	// Swap T's blockers: drop A, add B. B→T→B cycle is structural — not
	// transient — so removing A first does not help.
	_, err := s.UpdateBlockers(ctx(), target.ID, []uint{b.ID}, []uint{a.ID})
	var ce *model.CycleDetectedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CycleDetectedError for structural cycle, got %v", err)
	}
}

// TestAddBlockers_CycleDetectionDeepChain — pins PR-15's recursive-CTE
// rewrite. The prior BFS did one query per traversed node; the CTE does
// one round-trip total. Walking a deep chain of N tasks each blocked by
// the previous one exercises the recursion's path-tracking column and
// confirms it terminates correctly without revisiting nodes.
func TestAddBlockers_CycleDetectionDeepChain(t *testing.T) {
	s := newTestStore(t)
	const N = 50
	tasks := make([]uint, N)
	for i := 0; i < N; i++ {
		task, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		tasks[i] = task.ID
	}
	// Chain: task[i] is blocked by task[i-1]. So task[N-1] is
	// transitively blocked by task[0] through N-1 hops.
	for i := 1; i < N; i++ {
		if _, err := s.AddBlockers(ctx(), tasks[i], []uint{tasks[i-1]}); err != nil {
			t.Fatalf("add blocker at %d: %v", i, err)
		}
	}

	// Adding "task[0] is blocked by task[N-1]" closes the cycle.
	_, err := s.AddBlockers(ctx(), tasks[0], []uint{tasks[N-1]})
	var ce *model.CycleDetectedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CycleDetectedError on N=%d-deep chain, got %v", N, err)
	}
	// The path should report at least the endpoints — sanity check
	// without pinning the exact intermediate order (the CTE doesn't
	// guarantee BFS shortest-path order).
	if len(ce.Path) < 2 {
		t.Errorf("cycle path = %v, want at least taskID and blockerID", ce.Path)
	}
	if ce.Path[0] != tasks[0] {
		t.Errorf("cycle path should start with taskID=%d, got %d", tasks[0], ce.Path[0])
	}
	if ce.Path[len(ce.Path)-1] != tasks[0] {
		t.Errorf("cycle path should end with taskID=%d (closing the loop), got %d",
			tasks[0], ce.Path[len(ce.Path)-1])
	}
}

// TestAddBlockers_NonCycleUpstreamDoesNotFalsePositive — task X has a
// long upstream chain, but the chain doesn't reach Y. Adding "Y is
// blocked by X" must not trigger cycle detection (X's upstream is
// disjoint from Y).
func TestAddBlockers_NonCycleUpstreamDoesNotFalsePositive(t *testing.T) {
	s := newTestStore(t)
	x, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "X"})
	if err != nil {
		t.Fatalf("create X: %v", err)
	}
	y, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Y"})
	if err != nil {
		t.Fatalf("create Y: %v", err)
	}
	upstream := make([]uint, 5)
	for i := range upstream {
		task, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "up"})
		if err != nil {
			t.Fatalf("create upstream %d: %v", i, err)
		}
		upstream[i] = task.ID
	}
	// Build a chain: x ← upstream[0] ← upstream[1] ← ... ← upstream[4].
	if _, err := s.AddBlockers(ctx(), x.ID, []uint{upstream[0]}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i := 1; i < len(upstream); i++ {
		if _, err := s.AddBlockers(ctx(), upstream[i-1], []uint{upstream[i]}); err != nil {
			t.Fatalf("chain %d: %v", i, err)
		}
	}

	// Y has no relationship with the chain. "Y blocked by X" must succeed.
	if _, err := s.AddBlockers(ctx(), y.ID, []uint{x.ID}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
