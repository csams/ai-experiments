package gormstore_test

import (
	"fmt"
	"testing"

	"github.com/csams/todo/store"
)

const (
	defaultLimit = 200
	maxLimit     = 1000
)

// TestListNotes_DefaultCapAppliedWithoutQuery — pre-PR-19 ListNotes was
// asymmetric: capped at defaultQueryLimit only when Query was set,
// otherwise unbounded. The new policy applies the cap uniformly.
func TestListNotes_DefaultCapAppliedWithoutQuery(t *testing.T) {
	s := newTestStore(t)
	// Create defaultLimit+50 standalone notes.
	for i := 0; i < defaultLimit+50; i++ {
		if _, err := s.AddNote(ctx(), nil, fmt.Sprintf("note %d", i)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := s.ListNotes(ctx(), store.ListNotesOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != defaultLimit {
		t.Errorf("len(got) = %d, want %d (default cap should apply even without Query)",
			len(got), defaultLimit)
	}
}

func TestListNotes_ExplicitLimitClampsAtMax(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 20; i++ {
		_, _ = s.AddNote(ctx(), nil, fmt.Sprintf("note %d", i))
	}

	// Ask for 10000 — should clamp at maxLimit (1000) but the result
	// is bounded by the row count.
	got, err := s.ListNotes(ctx(), store.ListNotesOptions{Limit: 10000})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 20 {
		t.Errorf("len(got) = %d, want 20 (all rows fit under the clamp)", len(got))
	}
}

// TestListNotes_OffsetSupportsPagination — the new Offset field is the
// supported way to page past the cap.
func TestListNotes_OffsetSupportsPagination(t *testing.T) {
	s := newTestStore(t)
	const total = 250
	for i := 0; i < total; i++ {
		_, _ = s.AddNote(ctx(), nil, fmt.Sprintf("note %d", i))
	}

	page1, err := s.ListNotes(ctx(), store.ListNotesOptions{Limit: 100})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 100 {
		t.Fatalf("page1 len = %d, want 100", len(page1))
	}

	page2, err := s.ListNotes(ctx(), store.ListNotesOptions{Limit: 100, Offset: 100})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 100 {
		t.Fatalf("page2 len = %d, want 100", len(page2))
	}

	page3, err := s.ListNotes(ctx(), store.ListNotesOptions{Limit: 100, Offset: 200})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 50 {
		t.Fatalf("page3 len = %d, want 50 (remaining)", len(page3))
	}

	// Pages must not overlap. IDs are auto-incremented; check
	// successive pages don't share rows.
	seen := map[uint]bool{}
	for _, n := range page1 {
		seen[n.ID] = true
	}
	for _, n := range page2 {
		if seen[n.ID] {
			t.Errorf("note %d appears in both page1 and page2", n.ID)
		}
		seen[n.ID] = true
	}
	for _, n := range page3 {
		if seen[n.ID] {
			t.Errorf("note %d appears earlier and in page3", n.ID)
		}
	}
}

func TestListTasks_ExplicitLimitClampsAtMax(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		_, _ = s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	}

	got, err := s.ListTasks(ctx(), store.ListTasksOptions{Limit: 10000})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("len(got) = %d, want 5 (clamp doesn't lose rows that fit)", len(got))
	}
}

// TestListTasks_DefaultCapApplied was always the contract for ListTasks;
// pin it explicitly now that the unified policy makes the behavior
// load-bearing for ListNotes too.
func TestListTasks_DefaultCapApplied(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < defaultLimit+10; i++ {
		_, _ = s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	}

	got, err := s.ListTasks(ctx(), store.ListTasksOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != defaultLimit {
		t.Errorf("len(got) = %d, want %d", len(got), defaultLimit)
	}
}
