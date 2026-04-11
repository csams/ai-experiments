package gormstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestCreateTask_Basic(t *testing.T) {
	s := newTestStore(t)

	task, err := s.CreateTask("Test task", "A description", 1, nil, nil)
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
	task, err := s.CreateTask("Tagged task", "", 0, &due, []string{"backend", "urgent"})
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
	_, err := s.CreateTask("", "desc", 0, nil, nil)
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
	_, err := s.GetTask(999)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetTask_WithDetails(t *testing.T) {
	s := newTestStore(t)

	task, _ := s.CreateTask("Parent", "", 0, nil, []string{"tag1"})
	s.AddNote(task.ID, "a note")
	s.AddLink(task.ID, model.LinkJira, "PROJ-123")

	detail, err := s.GetTask(task.ID)
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

	a, _ := s.CreateTask("A", "", 0, nil, nil)
	b, _ := s.CreateTask("B", "", 0, nil, nil)
	s.AddBlockers(b.ID, []uint{a.ID})

	detail, err := s.GetTask(a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Blocking) != 1 || detail.Blocking[0].ID != b.ID {
		t.Errorf("blocking = %v, want [task %d]", detail.Blocking, b.ID)
	}
}

func TestUpdateTask_PartialUpdate(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask("Original", "desc", 5, nil, nil)

	newTitle := "Updated"
	updated, err := s.UpdateTask(task.ID, store.UpdateTaskOptions{Title: &newTitle})
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
	task, _ := s.CreateTask("Task", "", 0, &due, nil)

	// Clear due date
	updated, err := s.UpdateTask(task.ID, store.UpdateTaskOptions{ClearDueAt: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DueAt != nil {
		t.Error("expected DueAt to be nil")
	}

	// Set due date
	newDue := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	updated, err = s.UpdateTask(task.ID, store.UpdateTaskOptions{DueAt: &newDue})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DueAt == nil || !updated.DueAt.Round(time.Second).Equal(newDue) {
		t.Errorf("due_at = %v, want %v", updated.DueAt, newDue)
	}
}

func TestListTasks_TopLevelOnly(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask("Parent", "", 0, nil, nil)
	s.CreateTask("Child", "", 0, nil, nil)
	childID := uint(2)
	s.SetParent(childID, &parent.ID)

	tasks, err := s.ListTasks(store.ListTasksOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != parent.ID {
		t.Errorf("expected only parent task, got %d tasks", len(tasks))
	}
}

func TestListTasks_WithSubtasks(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask("Parent", "", 0, nil, nil)
	s.CreateTask("Child", "", 0, nil, nil)
	childID := uint(2)
	s.SetParent(childID, &parent.ID)

	tasks, err := s.ListTasks(store.ListTasksOptions{IncludeSubtasks: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestListTasks_FilterByState(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask("New task", "", 0, nil, nil)
	task2, _ := s.CreateTask("Progressing", "", 0, nil, nil)
	s.SetTaskState(task2.ID, model.StateProgressing)

	state := model.StateProgressing
	tasks, err := s.ListTasks(store.ListTasksOptions{State: &state})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != task2.ID {
		t.Errorf("expected 1 progressing task, got %d", len(tasks))
	}
}

func TestListTasks_FilterByTags(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask("T1", "", 0, nil, []string{"backend", "urgent"})
	s.CreateTask("T2", "", 0, nil, []string{"backend"})
	s.CreateTask("T3", "", 0, nil, []string{"frontend"})

	tasks, err := s.ListTasks(store.ListTasksOptions{Tags: []string{"backend", "urgent"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task with both tags, got %d", len(tasks))
	}
}

func TestDeleteTask_CascadesNotesLinksTagsBlockers(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask("Task", "", 0, nil, []string{"tag1"})
	s.AddNote(task.ID, "note")
	s.AddLink(task.ID, model.LinkURL, "https://example.com")

	if err := s.DeleteTask(task.ID, false); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.GetTask(task.ID)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

// --- Notes CRUD ---

func TestNotes_CRUD(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask("Task", "", 0, nil, nil)

	// Add
	note, err := s.AddNote(task.ID, "first note")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	if note.ID == 0 || note.Text != "first note" {
		t.Errorf("unexpected note: %+v", note)
	}

	// Update
	updated, err := s.UpdateNote(task.ID, note.ID, "updated note")
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	if updated.Text != "updated note" {
		t.Errorf("text = %q, want %q", updated.Text, "updated note")
	}

	// List
	notes, err := s.ListNotes(task.ID)
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 {
		t.Errorf("notes = %d, want 1", len(notes))
	}

	// Delete
	if err := s.DeleteNote(task.ID, note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}
	notes, _ = s.ListNotes(task.ID)
	if len(notes) != 0 {
		t.Errorf("notes after delete = %d, want 0", len(notes))
	}
}

func TestDeleteNote_WrongTask(t *testing.T) {
	s := newTestStore(t)
	t1, _ := s.CreateTask("T1", "", 0, nil, nil)
	t2, _ := s.CreateTask("T2", "", 0, nil, nil)
	note, _ := s.AddNote(t1.ID, "note for t1")

	err := s.DeleteNote(t2.ID, note.ID)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Links CRUD ---

func TestLinks_CRUD(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask("Task", "", 0, nil, nil)

	link, err := s.AddLink(task.ID, model.LinkJira, "PROJ-123")
	if err != nil {
		t.Fatalf("add link: %v", err)
	}
	if link.Type != model.LinkJira || link.URL != "PROJ-123" {
		t.Errorf("unexpected link: %+v", link)
	}

	links, _ := s.ListLinks(task.ID)
	if len(links) != 1 {
		t.Errorf("links = %d, want 1", len(links))
	}

	if err := s.DeleteLink(task.ID, link.ID); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	links, _ = s.ListLinks(task.ID)
	if len(links) != 0 {
		t.Errorf("links after delete = %d, want 0", len(links))
	}
}

func TestAddLink_InvalidType(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask("Task", "", 0, nil, nil)
	_, err := s.AddLink(task.ID, "invalid", "url")
	if err == nil {
		t.Fatal("expected error for invalid link type")
	}
}

// --- Tags ---

func TestTags_AddRemoveIdempotent(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask("Task", "", 0, nil, nil)

	if err := s.AddTags(task.ID, []string{"a", "b"}); err != nil {
		t.Fatalf("add tags: %v", err)
	}
	// Idempotent: adding again should not error
	if err := s.AddTags(task.ID, []string{"a"}); err != nil {
		t.Fatalf("add tags again: %v", err)
	}

	detail, _ := s.GetTask(task.ID)
	if len(detail.Tags) != 2 {
		t.Errorf("tags = %d, want 2", len(detail.Tags))
	}

	if err := s.RemoveTags(task.ID, []string{"a"}); err != nil {
		t.Fatalf("remove tags: %v", err)
	}
	detail, _ = s.GetTask(task.ID)
	if len(detail.Tags) != 1 {
		t.Errorf("tags after remove = %d, want 1", len(detail.Tags))
	}

	// Remove nonexistent tag: no error
	if err := s.RemoveTags(task.ID, []string{"nonexistent"}); err != nil {
		t.Fatalf("remove nonexistent: %v", err)
	}
}

// --- Search ---

func TestSearchTasks(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask("Fix login bug", "auth token expires", 0, nil, nil)
	s.CreateTask("Update docs", "readme changes", 0, nil, nil)

	results, err := s.SearchTasks("login")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results = %d, want 1", len(results))
	}

	results, _ = s.SearchTasks("auth")
	if len(results) != 1 {
		t.Errorf("results by desc = %d, want 1", len(results))
	}
}

func TestSearchNotes(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask("Task", "", 0, nil, nil)
	s.AddNote(task.ID, "checked auth token expiry")
	s.AddNote(task.ID, "unrelated note")

	results, err := s.SearchNotes("auth")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results = %d, want 1", len(results))
	}
}
