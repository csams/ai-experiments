package gormstore_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/csams/todo/model"
)

// --- BulkUpdateState ---

func TestBulkUpdateState_Done(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "Task A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "Task B", "", 0, nil, nil)

	results, err := s.BulkUpdateState(ctx(), []uint{a.ID, b.ID}, model.StateDone)
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
	blocker, _ := s.CreateTask(ctx(), "Blocker", "", 0, nil, nil)
	blocked, _ := s.CreateTask(ctx(), "Blocked", "", 0, nil, nil)
	s.AddBlockers(ctx(), blocked.ID, []uint{blocker.ID})

	// Complete the blocker via bulk
	_, err := s.BulkUpdateState(ctx(), []uint{blocker.ID}, model.StateDone)
	if err != nil {
		t.Fatalf("bulk update state: %v", err)
	}

	detail, _ := s.GetTask(ctx(), blocked.ID)
	if detail.State != model.StateUnblocked {
		t.Errorf("expected blocked task to be Unblocked, got %s", detail.State)
	}
}

func TestBulkUpdateState_RejectsBlocked(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	_, err := s.BulkUpdateState(ctx(), []uint{1}, model.StateBlocked)
	if !errors.Is(err, model.ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got %v", err)
	}
}

func TestBulkUpdateState_RejectsArchived(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	s.ArchiveTask(ctx(), task.ID, true)

	_, err := s.BulkUpdateState(ctx(), []uint{task.ID}, model.StateDone)
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("expected ErrArchived, got %v", err)
	}
}

func TestBulkUpdateState_RejectsNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.BulkUpdateState(ctx(), []uint{999}, model.StateDone)
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
	_, err := s.BulkUpdateState(ctx(), ids, model.StateDone)
	if err == nil {
		t.Fatal("expected error for exceeding bulk limit")
	}
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

// --- BulkUpdatePriority ---

func TestBulkUpdatePriority_Basic(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "Task A", "", 5, nil, nil)
	b, _ := s.CreateTask(ctx(), "Task B", "", 5, nil, nil)

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
	blocker, _ := s.CreateTask(ctx(), "Blocker", "", 0, nil, nil)
	blocked, _ := s.CreateTask(ctx(), "Blocked", "", 1, nil, nil)
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
	a, _ := s.CreateTask(ctx(), "A", "", 5, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 5, nil, nil)

	// B is blocked by A → AddBlockers(B, [A])
	if _, err := s.AddBlockers(ctx(), b.ID, []uint{a.ID}); err != nil {
		t.Fatalf("add blockers: %v", err)
	}

	// Update B priority to 0 — should propagate to A (B's blocker)
	_, err := s.BulkUpdatePriority(ctx(), []uint{b.ID}, 0)
	if err != nil {
		t.Fatalf("bulk update priority: %v", err)
	}

	detail, _ := s.GetTask(ctx(), a.ID)
	if detail.Priority != 0 {
		t.Errorf("expected blocker priority to be promoted to 0, got %d", detail.Priority)
	}
}

// --- BulkAddTags ---

func TestBulkAddTags_Basic(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "Task A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "Task B", "", 0, nil, nil)

	err := s.BulkAddTags(ctx(), []uint{a.ID, b.ID}, []string{"urgent", "backend"})
	if err != nil {
		t.Fatalf("bulk add tags: %v", err)
	}

	detailA, _ := s.GetTask(ctx(), a.ID)
	detailB, _ := s.GetTask(ctx(), b.ID)
	if len(detailA.Tags) != 2 {
		t.Errorf("task A: expected 2 tags, got %d", len(detailA.Tags))
	}
	if len(detailB.Tags) != 2 {
		t.Errorf("task B: expected 2 tags, got %d", len(detailB.Tags))
	}
}

func TestBulkAddTags_Idempotent(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "Task", "", 0, nil, []string{"existing"})

	// Adding the same tag again should not error or duplicate
	err := s.BulkAddTags(ctx(), []uint{1}, []string{"existing"})
	if err != nil {
		t.Fatalf("bulk add tags (idempotent): %v", err)
	}

	detail, _ := s.GetTask(ctx(), 1)
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
	s.CreateTask(ctx(), "Task", "", 0, nil, tags)

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
	s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	err := s.BulkAddTags(ctx(), []uint{1}, []string{"invalid tag!"})
	if err == nil {
		t.Fatal("expected error for invalid tag characters")
	}
}

// --- BulkRemoveTags ---

func TestBulkRemoveTags_Basic(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "Task A", "", 0, nil, []string{"remove-me", "keep"})
	s.CreateTask(ctx(), "Task B", "", 0, nil, []string{"remove-me", "keep"})

	err := s.BulkRemoveTags(ctx(), []uint{1, 2}, []string{"remove-me"})
	if err != nil {
		t.Fatalf("bulk remove tags: %v", err)
	}

	detailA, _ := s.GetTask(ctx(), 1)
	detailB, _ := s.GetTask(ctx(), 2)
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
	s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	err := s.BulkRemoveTags(ctx(), []uint{1}, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("expected no error removing nonexistent tag, got %v", err)
	}
}
