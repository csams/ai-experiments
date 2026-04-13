package gormstore_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestCreateTask_Basic(t *testing.T) {
	s := newTestStore(t)

	task, err := s.CreateTask(ctx(), "Test task", "A description", 1, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if task.Title != "Test task" {
		t.Errorf("title = %q, want %q", task.Title, "Test task")
	}
	if task.State != model.StateNew {
		t.Errorf("state = %q, want %q", task.State, model.StateNew)
	}
	if task.Priority != 1 {
		t.Errorf("priority = %d, want 1", task.Priority)
	}
}

func TestCreateTask_WithTagsAndDueDate(t *testing.T) {
	s := newTestStore(t)

	due := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	task, err := s.CreateTask(ctx(), "Tagged task", "", 0, &due, []string{"backend", "urgent"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(task.Tags) != 2 {
		t.Errorf("tags count = %d, want 2", len(task.Tags))
	}
	if task.DueAt == nil || !task.DueAt.Equal(due) {
		t.Errorf("due_at = %v, want %v", task.DueAt, due)
	}
}

func TestCreateTask_EmptyTitle(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateTask(ctx(), "", "desc", 0, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T", err)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetTask(ctx(), 999)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetTask_WithDetails(t *testing.T) {
	s := newTestStore(t)

	task, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, []string{"tag1"})
	s.AddNote(ctx(), task.ID, "a note")
	s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-123")

	detail, err := s.GetTask(ctx(), task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Notes) != 1 {
		t.Errorf("notes = %d, want 1", len(detail.Notes))
	}
	if len(detail.Links) != 1 {
		t.Errorf("links = %d, want 1", len(detail.Links))
	}
	if len(detail.Tags) != 1 {
		t.Errorf("tags = %d, want 1", len(detail.Tags))
	}
}

func TestGetTask_ComputesBlockingList(t *testing.T) {
	s := newTestStore(t)

	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	detail, err := s.GetTask(ctx(), a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Blocking) != 1 || detail.Blocking[0].ID != b.ID {
		t.Errorf("blocking = %v, want [task %d]", detail.Blocking, b.ID)
	}
}

func TestUpdateTask_PartialUpdate(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Original", "desc", 5, nil, nil)

	newTitle := "Updated"
	updated, err := s.UpdateTask(ctx(), task.ID, store.UpdateTaskOptions{Title: &newTitle})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "Updated" {
		t.Errorf("title = %q, want %q", updated.Title, "Updated")
	}
	if updated.Priority != 5 {
		t.Errorf("priority should be unchanged, got %d", updated.Priority)
	}
}

func TestUpdateTask_DueDate(t *testing.T) {
	s := newTestStore(t)
	due := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, &due, nil)

	// Clear due date
	updated, err := s.UpdateTask(ctx(), task.ID, store.UpdateTaskOptions{ClearDueAt: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DueAt != nil {
		t.Error("expected DueAt to be nil")
	}

	// Set due date
	newDue := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	updated, err = s.UpdateTask(ctx(), task.ID, store.UpdateTaskOptions{DueAt: &newDue})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DueAt == nil || !updated.DueAt.Round(time.Second).Equal(newDue) {
		t.Errorf("due_at = %v, want %v", updated.DueAt, newDue)
	}
}

func TestListTasks_TopLevelOnly(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	s.CreateTask(ctx(), "Child", "", 0, nil, nil)
	childID := uint(2)
	s.SetParent(ctx(), childID, &parent.ID)

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != parent.ID {
		t.Errorf("expected only parent task, got %d tasks", len(tasks))
	}
}

func TestListTasks_WithSubtasks(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	s.CreateTask(ctx(), "Child", "", 0, nil, nil)
	childID := uint(2)
	s.SetParent(ctx(), childID, &parent.ID)

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{IncludeSubtasks: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestListTasks_FilterByState(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "New task", "", 0, nil, nil)
	task2, _ := s.CreateTask(ctx(), "Progressing", "", 0, nil, nil)
	s.SetTaskState(ctx(), task2.ID, model.StateProgressing)

	state := model.StateProgressing
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{State: &state})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != task2.ID {
		t.Errorf("expected 1 progressing task, got %d", len(tasks))
	}
}

func TestListTasks_FilterByTags(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "T1", "", 0, nil, []string{"backend", "urgent"})
	s.CreateTask(ctx(), "T2", "", 0, nil, []string{"backend"})
	s.CreateTask(ctx(), "T3", "", 0, nil, []string{"frontend"})

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{Tags: []string{"backend", "urgent"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task with both tags, got %d", len(tasks))
	}
}

func TestDeleteTask_CascadesNotesLinksTagsBlockers(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, []string{"tag1"})
	s.AddNote(ctx(), task.ID, "note")
	s.AddLink(ctx(), task.ID, model.LinkURL, "https://example.com")

	if err := s.DeleteTask(ctx(), task.ID, false); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.GetTask(ctx(), task.ID)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

// --- Notes CRUD ---

func TestNotes_CRUD(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	// Add
	note, err := s.AddNote(ctx(), task.ID, "first note")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	if note.ID == 0 || note.Text != "first note" {
		t.Errorf("unexpected note: %+v", note)
	}

	// Update
	updated, err := s.UpdateNote(ctx(), task.ID, note.ID, "updated note")
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	if updated.Text != "updated note" {
		t.Errorf("text = %q, want %q", updated.Text, "updated note")
	}

	// List
	notes, err := s.ListNotes(ctx(), task.ID)
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 {
		t.Errorf("notes = %d, want 1", len(notes))
	}

	// Delete
	if err := s.DeleteNote(ctx(), task.ID, note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}
	notes, _ = s.ListNotes(ctx(), task.ID)
	if len(notes) != 0 {
		t.Errorf("notes after delete = %d, want 0", len(notes))
	}
}

func TestDeleteNote_WrongTask(t *testing.T) {
	s := newTestStore(t)
	t1, _ := s.CreateTask(ctx(), "T1", "", 0, nil, nil)
	t2, _ := s.CreateTask(ctx(), "T2", "", 0, nil, nil)
	note, _ := s.AddNote(ctx(), t1.ID, "note for t1")

	err := s.DeleteNote(ctx(), t2.ID, note.ID)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Links CRUD ---

func TestLinks_CRUD(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	link, err := s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-123")
	if err != nil {
		t.Fatalf("add link: %v", err)
	}
	if link.Type != model.LinkJira || link.URL != "PROJ-123" {
		t.Errorf("unexpected link: %+v", link)
	}

	links, _ := s.ListLinks(ctx(), task.ID)
	if len(links) != 1 {
		t.Errorf("links = %d, want 1", len(links))
	}

	if err := s.DeleteLink(ctx(), task.ID, link.ID); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	links, _ = s.ListLinks(ctx(), task.ID)
	if len(links) != 0 {
		t.Errorf("links after delete = %d, want 0", len(links))
	}
}

func TestAddLink_InvalidType(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	_, err := s.AddLink(ctx(), task.ID, "invalid", "url")
	if err == nil {
		t.Fatal("expected error for invalid link type")
	}
}

// --- Tags ---

func TestTags_AddRemoveIdempotent(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	if err := s.AddTags(ctx(), task.ID, []string{"a", "b"}); err != nil {
		t.Fatalf("add tags: %v", err)
	}
	// Idempotent: adding again should not error
	if err := s.AddTags(ctx(), task.ID, []string{"a"}); err != nil {
		t.Fatalf("add tags again: %v", err)
	}

	detail, _ := s.GetTask(ctx(), task.ID)
	if len(detail.Tags) != 2 {
		t.Errorf("tags = %d, want 2", len(detail.Tags))
	}

	if err := s.RemoveTags(ctx(), task.ID, []string{"a"}); err != nil {
		t.Fatalf("remove tags: %v", err)
	}
	detail, _ = s.GetTask(ctx(), task.ID)
	if len(detail.Tags) != 1 {
		t.Errorf("tags after remove = %d, want 1", len(detail.Tags))
	}

	// Remove nonexistent tag: no error
	if err := s.RemoveTags(ctx(), task.ID, []string{"nonexistent"}); err != nil {
		t.Fatalf("remove nonexistent: %v", err)
	}
}

// --- Search ---

func TestSearchTasks(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "Fix login bug", "auth token expires", 0, nil, nil)
	s.CreateTask(ctx(), "Update docs", "readme changes", 0, nil, nil)

	results, err := s.SearchTasks(ctx(), "login")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results = %d, want 1", len(results))
	}

	results, _ = s.SearchTasks(ctx(), "auth")
	if len(results) != 1 {
		t.Errorf("results by desc = %d, want 1", len(results))
	}
}

func TestSearchTasks_LIKEWildcardEscaping(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "100% complete", "", 0, nil, nil)
	s.CreateTask(ctx(), "Normal task", "", 0, nil, nil)

	// A search for "%" should only match the task with literal %
	results, err := s.SearchTasks(ctx(), "%")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results = %d, want 1 (should not match all)", len(results))
	}

	// A search for "_" should match nothing (no task has literal _)
	results, _ = s.SearchTasks(ctx(), "_")
	if len(results) != 0 {
		t.Errorf("underscore results = %d, want 0", len(results))
	}
}

func TestListTasks_Pagination(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		s.CreateTask(ctx(), fmt.Sprintf("Task %d", i), "", 0, nil, nil)
	}

	// Limit
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{Limit: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("limit=2 got %d tasks, want 2", len(tasks))
	}

	// Offset
	all, _ := s.ListTasks(ctx(), store.ListTasksOptions{Limit: 10})
	offset, _ := s.ListTasks(ctx(), store.ListTasksOptions{Limit: 10, Offset: 2})
	if len(offset) != len(all)-2 {
		t.Errorf("offset=2 got %d tasks, want %d", len(offset), len(all)-2)
	}
}

func TestSearchNotes(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	s.AddNote(ctx(), task.ID, "checked auth token expiry")
	s.AddNote(ctx(), task.ID, "unrelated note")

	results, err := s.SearchNotes(ctx(), "auth")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results = %d, want 1", len(results))
	}
}

func intPtr(i int) *int   { return &i }
func boolPtr(b bool) *bool { return &b }

func TestListTasks_HasDueDate(t *testing.T) {
	s := newTestStore(t)
	due := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	s.CreateTask(ctx(), "With due", "", 0, &due, nil)
	s.CreateTask(ctx(), "Without due", "", 0, nil, nil)

	// has_due_date = true
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{HasDueDate: boolPtr(true)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "With due" {
		t.Errorf("has_due_date=true: got %d tasks, want 1 with due date", len(tasks))
	}

	// has_due_date = false
	tasks, err = s.ListTasks(ctx(), store.ListTasksOptions{HasDueDate: boolPtr(false)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Without due" {
		t.Errorf("has_due_date=false: got %d tasks, want 1 without due date", len(tasks))
	}

	// has_due_date = nil (no filter)
	tasks, err = s.ListTasks(ctx(), store.ListTasksOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("has_due_date=nil: got %d tasks, want 2", len(tasks))
	}
}

func TestListTasks_DueBefore(t *testing.T) {
	s := newTestStore(t)
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	jun := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	s.CreateTask(ctx(), "Jan", "", 0, &jan, nil)
	s.CreateTask(ctx(), "Mar", "", 0, &mar, nil)
	s.CreateTask(ctx(), "Jun", "", 0, &jun, nil)
	s.CreateTask(ctx(), "NoDue", "", 0, nil, nil)

	cutoff := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{DueBefore: &cutoff})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("due_before Apr 1: got %d tasks, want 2 (Jan, Mar)", len(tasks))
	}

	// Exclusive: exact boundary should not match
	tasks, err = s.ListTasks(ctx(), store.ListTasksOptions{DueBefore: &mar})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("due_before Mar 15: got %d tasks, want 1 (Jan only)", len(tasks))
	}
}

func TestListTasks_DueAfter(t *testing.T) {
	s := newTestStore(t)
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	jun := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	s.CreateTask(ctx(), "Jan", "", 0, &jan, nil)
	s.CreateTask(ctx(), "Jun", "", 0, &jun, nil)
	s.CreateTask(ctx(), "NoDue", "", 0, nil, nil)

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{DueAfter: &cutoff})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Jun" {
		t.Errorf("due_after Mar 1: got %d tasks, want 1 (Jun)", len(tasks))
	}

	// Exclusive: exact boundary should not match
	tasks, err = s.ListTasks(ctx(), store.ListTasksOptions{DueAfter: &jun})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("due_after Jun 15: got %d tasks, want 0", len(tasks))
	}
}

func TestListTasks_DueOn(t *testing.T) {
	s := newTestStore(t)
	// Two tasks on the same day at different times
	morning := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	evening := time.Date(2026, 5, 10, 22, 30, 0, 0, time.UTC)
	dayBefore := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	dayAfter := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	s.CreateTask(ctx(), "Morning", "", 0, &morning, nil)
	s.CreateTask(ctx(), "Evening", "", 0, &evening, nil)
	s.CreateTask(ctx(), "DayBefore", "", 0, &dayBefore, nil)
	s.CreateTask(ctx(), "DayAfter", "", 0, &dayAfter, nil)
	s.CreateTask(ctx(), "NoDue", "", 0, nil, nil)

	target := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{DueOn: &target})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("due_on May 10: got %d tasks, want 2 (Morning, Evening)", len(tasks))
	}
}

func TestListTasks_DueRange(t *testing.T) {
	s := newTestStore(t)
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	jun := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	s.CreateTask(ctx(), "Jan", "", 0, &jan, nil)
	s.CreateTask(ctx(), "Mar", "", 0, &mar, nil)
	s.CreateTask(ctx(), "Jun", "", 0, &jun, nil)
	s.CreateTask(ctx(), "NoDue", "", 0, nil, nil)

	// Combined range: after Feb 1 AND before May 1 → only Mar
	after := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{DueAfter: &after, DueBefore: &before})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Mar" {
		t.Errorf("due range Feb-May: got %d tasks, want 1 (Mar)", len(tasks))
	}
}

func TestListTasks_PriorityRange(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "P-1", "", -1, nil, nil)
	s.CreateTask(ctx(), "P0", "", 0, nil, nil)
	s.CreateTask(ctx(), "P1", "", 1, nil, nil)
	s.CreateTask(ctx(), "P5", "", 5, nil, nil)

	// Min only
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{PriorityMin: intPtr(1)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("priority_min=1: got %d tasks, want 2 (P1, P5)", len(tasks))
	}

	// Max only
	tasks, err = s.ListTasks(ctx(), store.ListTasksOptions{PriorityMax: intPtr(0)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("priority_max=0: got %d tasks, want 2 (P-1, P0)", len(tasks))
	}

	// Exact match (min = max)
	tasks, err = s.ListTasks(ctx(), store.ListTasksOptions{PriorityMin: intPtr(0), PriorityMax: intPtr(0)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "P0" {
		t.Errorf("priority 0..0: got %d tasks, want 1 (P0)", len(tasks))
	}

	// Range
	tasks, err = s.ListTasks(ctx(), store.ListTasksOptions{PriorityMin: intPtr(0), PriorityMax: intPtr(5)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("priority 0..5: got %d tasks, want 3 (P0, P1, P5)", len(tasks))
	}

	// Negative boundary: min=-1 should include P-1 itself
	tasks, err = s.ListTasks(ctx(), store.ListTasksOptions{PriorityMin: intPtr(-1)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 4 {
		t.Errorf("priority_min=-1: got %d tasks, want 4 (all)", len(tasks))
	}
}

func TestListTasks_TagsSubsetOf(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "AB", "", 0, nil, []string{"a", "b"})
	s.CreateTask(ctx(), "A", "", 0, nil, []string{"a"})
	s.CreateTask(ctx(), "C", "", 0, nil, []string{"c"})
	s.CreateTask(ctx(), "Empty", "", 0, nil, nil)

	// subset_of {a, b}: AB({a,b}⊆{a,b}), A({a}⊆{a,b}), Empty(∅⊆{a,b}) match; C does not
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{TagsSubsetOf: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("tags_subset_of {a,b}: got %d tasks, want 3 (AB, A, Empty)", len(tasks))
	}
	for _, task := range tasks {
		if task.Title == "C" {
			t.Errorf("tags_subset_of {a,b}: should not include task C with tag {c}")
		}
	}
}

func TestListTasks_TagsSubsetOfCombined(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), "AB", "", 0, nil, []string{"a", "b"})
	s.CreateTask(ctx(), "A", "", 0, nil, []string{"a"})
	s.CreateTask(ctx(), "ABC", "", 0, nil, []string{"a", "b", "c"})
	s.CreateTask(ctx(), "Empty", "", 0, nil, nil)

	// Exact match: Tags (superset) = {a, b} AND TagsSubsetOf = {a, b}
	// Should match only AB: has at least {a,b} AND has only tags from {a,b}
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{
		Tags:         []string{"a", "b"},
		TagsSubsetOf: []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "AB" {
		t.Errorf("exact {a,b}: got %d tasks, want 1 (AB)", len(tasks))
	}
}

func TestListTasks_ConflictingFilters(t *testing.T) {
	s := newTestStore(t)
	due := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	s.CreateTask(ctx(), "With due", "", 0, &due, nil)
	s.CreateTask(ctx(), "Without due", "", 0, nil, nil)

	// has_due_date=false AND due_before=X → contradictory, should return 0
	cutoff := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{
		HasDueDate: boolPtr(false),
		DueBefore:  &cutoff,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("conflicting filters: got %d tasks, want 0", len(tasks))
	}
}
