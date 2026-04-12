package gormstore_test

import (
	"testing"

	"github.com/csams/todo/store"
)

func TestPriority_BlockerAdjustedWhenBlocking(t *testing.T) {
	s := newTestStore(t)
	// A (priority 5) blocks B (priority 1) → A should be adjusted to 1
	a, _ := s.CreateTask(ctx(), "A", "", 5, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 1, nil, nil)

	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	detail, _ := s.GetTask(ctx(), a.ID)
	if detail.Priority != 1 {
		t.Errorf("A priority = %d, want 1 (adjusted to match B)", detail.Priority)
	}
}

func TestPriority_PropagatesUpChain(t *testing.T) {
	s := newTestStore(t)
	// C (priority 5) blocks B (priority 3) blocks A (priority 1)
	// When A's priority is set, B and C should cascade
	a, _ := s.CreateTask(ctx(), "A", "", 5, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 5, nil, nil)
	c, _ := s.CreateTask(ctx(), "C", "", 5, nil, nil)

	s.AddBlockers(ctx(), b.ID, []uint{c.ID}) // C blocks B
	s.AddBlockers(ctx(), a.ID, []uint{b.ID}) // B blocks A

	// Update A to priority 1 — should propagate to B and C
	p := 1
	s.UpdateTask(ctx(), a.ID, store.UpdateTaskOptions{Priority: &p})

	detailB, _ := s.GetTask(ctx(), b.ID)
	detailC, _ := s.GetTask(ctx(), c.ID)

	if detailB.Priority != 1 {
		t.Errorf("B priority = %d, want 1", detailB.Priority)
	}
	if detailC.Priority != 1 {
		t.Errorf("C priority = %d, want 1", detailC.Priority)
	}
}

func TestPriority_BlockerCantBeDemotedBelowBlocked(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 1, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 1, nil, nil)
	s.AddBlockers(ctx(), b.ID, []uint{a.ID}) // A blocks B (priority 1)

	// Try to demote A to priority 10 — should be clamped to 1
	p := 10
	updated, err := s.UpdateTask(ctx(), a.ID, store.UpdateTaskOptions{Priority: &p})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Priority != 1 {
		t.Errorf("A priority = %d, want 1 (clamped)", updated.Priority)
	}
}

func TestPriority_NegativePrioritiesWork(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Urgent", "", -5, nil, nil)
	if task.Priority != -5 {
		t.Errorf("priority = %d, want -5", task.Priority)
	}
}
