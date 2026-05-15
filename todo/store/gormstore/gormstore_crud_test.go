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

	task, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Test task", Description: "A description", Priority: 1})
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
	task, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Tagged task", DueAt: &due, Tags: []string{"backend", "urgent"}})
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
	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "", Description: "desc"})
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
	_, err := s.GetTask(ctx(), 999, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetTask_WithDetails(t *testing.T) {
	s := newTestStore(t)

	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent", Tags: []string{"tag1"}})
	s.AddNote(ctx(), &task.ID, "a note")
	s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-123", "")

	detail, err := s.GetTask(ctx(), task.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
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

	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), b.ID, []uint{a.ID})

	detail, err := s.GetTask(ctx(), a.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Blocking) != 1 || detail.Blocking[0].ID != b.ID {
		t.Errorf("blocking = %v, want [task %d]", detail.Blocking, b.ID)
	}
}

func TestUpdateTask_PartialUpdate(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Original", Description: "desc", Priority: 5})

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
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task", DueAt: &due})

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
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
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
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "New task"})
	task2, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Progressing"})
	s.SetTaskState(ctx(), task2.ID, model.StateProgressing)

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{States: []model.TaskState{model.StateProgressing}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != task2.ID {
		t.Errorf("expected 1 progressing task, got %d", len(tasks))
	}
}

func TestListTasks_FilterByMultipleStates(t *testing.T) {
	s := newTestStore(t)
	newTask, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "New task"})
	progressingTask, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Progressing"})
	s.SetTaskState(ctx(), progressingTask.ID, model.StateProgressing)
	doneTask, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Done"})
	s.SetTaskState(ctx(), doneTask.ID, model.StateDone)

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{
		States: []model.TaskState{model.StateNew, model.StateProgressing},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	got := map[uint]bool{tasks[0].ID: true, tasks[1].ID: true}
	if !got[newTask.ID] || !got[progressingTask.ID] || got[doneTask.ID] {
		t.Errorf("expected {New, Progressing}, got IDs %v", got)
	}
}

func TestListTasks_FilterByTags(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T1", Tags: []string{"backend", "urgent"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T2", Tags: []string{"backend"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T3", Tags: []string{"frontend"}})

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
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task", Tags: []string{"tag1"}})
	s.AddNote(ctx(), &task.ID, "note")
	s.AddLink(ctx(), task.ID, model.LinkURL, "https://example.com", "")

	// Default: orphan notes, hard-delete links/tags. Use DeleteNotes:true here to
	// preserve the original assertion (full cascade).
	if err := s.DeleteTask(ctx(), task.ID, store.DeleteTaskOptions{DeleteNotes: true}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.GetTask(ctx(), task.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

// --- Notes CRUD ---

func TestNotes_CRUD(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	// Add
	note, err := s.AddNote(ctx(), &task.ID, "first note")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	if note.ID == 0 || note.Text != "first note" {
		t.Errorf("unexpected note: %+v", note)
	}
	if note.TaskID == nil || *note.TaskID != task.ID {
		t.Errorf("note.TaskID = %v, want %d", note.TaskID, task.ID)
	}

	// Update
	newText := "updated note"
	updated, err := s.UpdateNote(ctx(), note.ID, store.UpdateNoteOptions{Text: &newText})
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	if updated.Text != "updated note" {
		t.Errorf("text = %q, want %q", updated.Text, "updated note")
	}

	// List
	notes, err := s.ListNotes(ctx(), &task.ID)
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 {
		t.Errorf("notes = %d, want 1", len(notes))
	}

	// Delete
	if err := s.DeleteNote(ctx(), note.ID); err != nil {
		t.Fatalf("delete note: %v", err)
	}
	notes, _ = s.ListNotes(ctx(), &task.ID)
	if len(notes) != 0 {
		t.Errorf("notes after delete = %d, want 0", len(notes))
	}
}

func TestDeleteNote_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteNote(ctx(), 9999)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- Links CRUD ---

func TestLinks_CRUD(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	link, err := s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-123", "auth ticket")
	if err != nil {
		t.Fatalf("add link: %v", err)
	}
	if link.Type != model.LinkJira || link.URL != "PROJ-123" || link.Description != "auth ticket" {
		t.Errorf("unexpected link: %+v", link)
	}

	links, _ := s.ListLinks(ctx(), task.ID)
	if len(links) != 1 {
		t.Errorf("links = %d, want 1", len(links))
	}
	if links[0].Description != "auth ticket" {
		t.Errorf("description not persisted via ListLinks: %q", links[0].Description)
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
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	_, err := s.AddLink(ctx(), task.ID, "invalid", "url", "")
	if err == nil {
		t.Fatal("expected error for invalid link type")
	}
}

func TestUpdateLink_PartialFields(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	link, _ := s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-123", "original")

	// description-only update leaves type and URL intact
	desc := "updated"
	updated, err := s.UpdateLink(ctx(), task.ID, link.ID, store.UpdateLinkOptions{Description: &desc})
	if err != nil {
		t.Fatalf("update description: %v", err)
	}
	if updated.Description != "updated" || updated.Type != model.LinkJira || updated.URL != "PROJ-123" {
		t.Errorf("unexpected after description-only update: %+v", updated)
	}

	// url-only update
	newURL := "PROJ-999"
	updated, err = s.UpdateLink(ctx(), task.ID, link.ID, store.UpdateLinkOptions{URL: &newURL})
	if err != nil {
		t.Fatalf("update url: %v", err)
	}
	if updated.URL != "PROJ-999" || updated.Description != "updated" || updated.Type != model.LinkJira {
		t.Errorf("unexpected after url-only update: %+v", updated)
	}

	// type-only update
	newType := model.LinkURL
	updated, err = s.UpdateLink(ctx(), task.ID, link.ID, store.UpdateLinkOptions{Type: &newType})
	if err != nil {
		t.Fatalf("update type: %v", err)
	}
	if updated.Type != model.LinkURL || updated.URL != "PROJ-999" || updated.Description != "updated" {
		t.Errorf("unexpected after type-only update: %+v", updated)
	}
}

func TestUpdateLink_AllNilOpts(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	link, _ := s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-1", "desc")

	obs := &recordingObserver{}
	s.AddObserver(obs)

	got, err := s.UpdateLink(ctx(), task.ID, link.ID, store.UpdateLinkOptions{})
	if err != nil {
		t.Fatalf("no-op update: %v", err)
	}
	if got.URL != "PROJ-1" || got.Type != model.LinkJira || got.Description != "desc" {
		t.Errorf("link mutated by no-op update: %+v", got)
	}
	if n := len(obs.events); n != 0 {
		t.Errorf("no-op UpdateLink emitted %d events; expected 0", n)
	}
}

func TestUpdateLink_ClearsDescription(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	link, _ := s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-1", "to-be-cleared")

	empty := ""
	updated, err := s.UpdateLink(ctx(), task.ID, link.ID, store.UpdateLinkOptions{Description: &empty})
	if err != nil {
		t.Fatalf("clear description: %v", err)
	}
	if updated.Description != "" {
		t.Errorf("description not cleared: %q", updated.Description)
	}
}

func TestUpdateLink_NotFound(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	desc := "x"
	// nonexistent link_id
	_, err := s.UpdateLink(ctx(), task.ID, 999, store.UpdateLinkOptions{Description: &desc})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing link, got %v", err)
	}

	// link belongs to a different task
	otherTask, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Other"})
	link, _ := s.AddLink(ctx(), otherTask.ID, model.LinkJira, "PROJ-1", "")
	_, err = s.UpdateLink(ctx(), task.ID, link.ID, store.UpdateLinkOptions{Description: &desc})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound for cross-task link, got %v", err)
	}
}

func TestUpdateLink_InvalidType(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	link, _ := s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-1", "")

	bad := model.LinkType("nope")
	_, err := s.UpdateLink(ctx(), task.ID, link.ID, store.UpdateLinkOptions{Type: &bad})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v", err)
	}
}

func TestUpdateLink_EmptyURLRejected(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	link, _ := s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-1", "")

	empty := ""
	_, err := s.UpdateLink(ctx(), task.ID, link.ID, store.UpdateLinkOptions{URL: &empty})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError for empty URL, got %v", err)
	}
}

// --- Tags ---

func TestTags_AddRemoveIdempotent(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	if err := s.AddTags(ctx(), task.ID, []string{"a", "b"}); err != nil {
		t.Fatalf("add tags: %v", err)
	}
	// Idempotent: adding again should not error
	if err := s.AddTags(ctx(), task.ID, []string{"a"}); err != nil {
		t.Fatalf("add tags again: %v", err)
	}

	detail, _ := s.GetTask(ctx(), task.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Tags) != 2 {
		t.Errorf("tags = %d, want 2", len(detail.Tags))
	}

	if err := s.RemoveTags(ctx(), task.ID, []string{"a"}); err != nil {
		t.Fatalf("remove tags: %v", err)
	}
	detail, _ = s.GetTask(ctx(), task.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Fix login bug", Description: "auth token expires"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Update docs", Description: "readme changes"})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "100% complete"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Normal task"})

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
		s.CreateTask(ctx(), store.CreateTaskOptions{Title: fmt.Sprintf("Task %d", i)})
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
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})
	s.AddNote(ctx(), &task.ID, "checked auth token expiry")
	s.AddNote(ctx(), &task.ID, "unrelated note")

	results, err := s.SearchNotes(ctx(), "auth", store.SearchNotesOptions{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results = %d, want 1", len(results))
	}
}

func TestSearchNotes_ExcludesArchivedByDefault(t *testing.T) {
	s := newTestStore(t)
	live, _ := s.AddNote(ctx(), nil, "auth token rotation plan")
	archived, _ := s.AddNote(ctx(), nil, "auth migration scratchpad")
	if err := s.ArchiveNote(ctx(), archived.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	results, err := s.SearchNotes(ctx(), "auth", store.SearchNotesOptions{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].ID != live.ID {
		t.Errorf("default search returned %d notes, want only the live one (id %d)", len(results), live.ID)
	}

	all, err := s.SearchNotes(ctx(), "auth", store.SearchNotesOptions{IncludeArchived: true})
	if err != nil {
		t.Fatalf("search include_archived: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("include_archived returned %d notes, want 2", len(all))
	}
}

func TestSearchNotes_TaskIDFilter(t *testing.T) {
	s := newTestStore(t)
	taskA, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	taskB, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddNote(ctx(), &taskA.ID, "auth notes for A")
	s.AddNote(ctx(), &taskB.ID, "auth notes for B")
	s.AddNote(ctx(), nil, "auth standalone")

	results, err := s.SearchNotes(ctx(), "auth", store.SearchNotesOptions{TaskID: &taskA.ID})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].TaskID == nil || *results[0].TaskID != taskA.ID {
		t.Errorf("result task_id = %v, want %d", results[0].TaskID, taskA.ID)
	}
}

func TestListTasks_Query_TitleMatch(t *testing.T) {
	s := newTestStore(t)
	hit, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Fix login flow"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Update docs"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Refactor auth helpers"})

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{Query: "login"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != hit.ID {
		t.Errorf("got %d tasks, want only id %d", len(tasks), hit.ID)
	}
}

func TestListTasks_Query_DescriptionMatch(t *testing.T) {
	s := newTestStore(t)
	hit, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Some task", Description: "this body mentions Mongoose configuration"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Other", Description: "unrelated body"})

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{Query: "mongoose", Include: map[string]bool{"description": true}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != hit.ID {
		t.Errorf("got %d tasks, want only id %d", len(tasks), hit.ID)
	}
}

func TestListTasks_Query_LinkDescriptionMatch(t *testing.T) {
	s := newTestStore(t)
	hit, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Investigate"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Other", Description: "no links"})
	if _, err := s.AddLink(ctx(), hit.ID, model.LinkURL, "https://example.com/runbook", "Sentinel runbook details"); err != nil {
		t.Fatalf("add link: %v", err)
	}

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{Query: "sentinel"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != hit.ID {
		t.Errorf("got %d tasks, want only id %d", len(tasks), hit.ID)
	}
}

func TestListTasks_Query_ComposesWithFilters(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Root project", Tags: []string{"work"}})
	hit, _ := s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &parent.ID, Title: "Wire foo into bar", Tags: []string{"work"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &parent.ID, Title: "Wire baz into qux", Tags: []string{"work"}})       // wrong query
	s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &parent.ID, Title: "Wire foo elsewhere", Tags: []string{"work", "x"}}) // tag set wrong
	if _, err := s.SetTaskState(ctx(), hit.ID, model.StateProgressing); err != nil {
		t.Fatalf("set state: %v", err)
	}

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{
		Query:        "foo",
		ParentID:     &parent.ID,
		TagsSubsetOf: []string{"work"},
		States:       []model.TaskState{model.StateProgressing},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != hit.ID {
		t.Errorf("got %d tasks, want only id %d (composed filters)", len(tasks), hit.ID)
	}
}

func TestListTasks_Query_LIKEWildcardEscaping(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "100% complete"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Normal task"})

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{Query: "%"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("query=%% got %d tasks, want 1 (must not act as wildcard)", len(tasks))
	}

	tasks, _ = s.ListTasks(ctx(), store.ListTasksOptions{Query: "_"})
	if len(tasks) != 0 {
		t.Errorf("query=_ got %d tasks, want 0", len(tasks))
	}
}

func TestListTasks_Query_EmptyMeansNoFilter(t *testing.T) {
	s := newTestStore(t)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})

	withEmpty, err := s.ListTasks(ctx(), store.ListTasksOptions{Query: ""})
	if err != nil {
		t.Fatalf("list empty query: %v", err)
	}
	without, err := s.ListTasks(ctx(), store.ListTasksOptions{})
	if err != nil {
		t.Fatalf("list no opts: %v", err)
	}
	if len(withEmpty) != len(without) {
		t.Errorf("empty Query returned %d tasks, no Query returned %d (must match)", len(withEmpty), len(without))
	}
	if len(withEmpty) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(withEmpty))
	}
}

func TestListTasks_Query_UnicodeNormalization(t *testing.T) {
	// Stored as NFC "café" (single composed e-acute).
	want := "café"
	// Query as NFD "café" (e + combining acute) — sanitize must normalize
	// before the LIKE pattern is built, otherwise the substring won't match.
	queryNFD := "café"

	s := newTestStore(t)
	hit, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: want + " launch"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "unrelated"})

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{Query: queryNFD})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != hit.ID {
		t.Errorf("got %d tasks, want only id %d (NFD query must match NFC stored title)", len(tasks), hit.ID)
	}
}

func intPtr(i int) *int    { return &i }
func boolPtr(b bool) *bool { return &b }

func TestListTasks_HasDueDate(t *testing.T) {
	s := newTestStore(t)
	due := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "With due", DueAt: &due})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Without due"})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Jan", DueAt: &jan})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Mar", DueAt: &mar})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Jun", DueAt: &jun})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "NoDue"})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Jan", DueAt: &jan})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Jun", DueAt: &jun})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "NoDue"})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Morning", DueAt: &morning})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Evening", DueAt: &evening})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "DayBefore", DueAt: &dayBefore})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "DayAfter", DueAt: &dayAfter})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "NoDue"})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Jan", DueAt: &jan})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Mar", DueAt: &mar})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Jun", DueAt: &jun})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "NoDue"})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "P-1", Priority: -1})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "P0"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "P1", Priority: 1})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "P5", Priority: 5})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "AB", Tags: []string{"a", "b"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A", Tags: []string{"a"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C", Tags: []string{"c"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Empty"})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "AB", Tags: []string{"a", "b"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A", Tags: []string{"a"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "ABC", Tags: []string{"a", "b", "c"}})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Empty"})

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
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "With due", DueAt: &due})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Without due"})

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

// --- Inline links via CreateTaskOptions.Links ---

func TestCreateTask_WithLinks(t *testing.T) {
	s := newTestStore(t)
	obs := &recordingObserver{}
	s.AddObserver(obs)

	task, err := s.CreateTask(ctx(), store.CreateTaskOptions{
		Title: "Demo",
		Links: []model.LinkInput{
			{Type: model.LinkPR, URL: "https://github.com/foo/bar/pull/1", Description: "initial PR"},
			{Type: model.LinkJira, URL: "https://example.atlassian.net/browse/ABC-1"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Returned task must carry the links without a follow-up GetTask round-trip.
	if len(task.Links) != 2 {
		t.Fatalf("returned task.Links = %d, want 2", len(task.Links))
	}
	if task.Links[0].URL != "https://github.com/foo/bar/pull/1" || task.Links[0].Description != "initial PR" {
		t.Errorf("link[0] = %+v", task.Links[0])
	}
	if task.Links[1].Type != model.LinkJira {
		t.Errorf("link[1] type = %q, want jira", task.Links[1].Type)
	}
	// Exactly one task.created event; zero link.created events.
	taskCreated, linkCreated := 0, 0
	for _, e := range obs.events {
		switch e.Type {
		case "task.created":
			taskCreated++
		case "link.created":
			linkCreated++
		}
	}
	if taskCreated != 1 {
		t.Errorf("task.created events = %d, want 1", taskCreated)
	}
	if linkCreated != 0 {
		t.Errorf("link.created events = %d, want 0 (suppressed for inline links)", linkCreated)
	}
}

func TestCreateTask_WithLinks_InvalidType(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{
		Title: "Demo",
		Links: []model.LinkInput{{Type: model.LinkType("bogus"), URL: "https://example.com"}},
	})
	if err == nil {
		t.Fatal("expected error for invalid link type")
	}
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T", err)
	}
	// Task must not exist — rollback.
	tasks, _ := s.ListTasks(ctx(), store.ListTasksOptions{})
	if len(tasks) != 0 {
		t.Errorf("rollback failed: %d tasks exist after error", len(tasks))
	}
}

func TestCreateTask_WithLinks_InvalidURL(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{
		Title: "Demo",
		Links: []model.LinkInput{{Type: model.LinkPR, URL: ""}},
	})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	tasks, _ := s.ListTasks(ctx(), store.ListTasksOptions{})
	if len(tasks) != 0 {
		t.Errorf("rollback failed: %d tasks exist", len(tasks))
	}
}

func TestCreateTask_WithLinks_TooLongURL(t *testing.T) {
	s := newTestStore(t)
	buf := make([]byte, 2010)
	for i := range buf {
		buf[i] = 'a'
	}
	long := "https://" + string(buf)
	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{
		Title: "Demo",
		Links: []model.LinkInput{{Type: model.LinkURL, URL: long}},
	})
	if err == nil {
		t.Fatal("expected error for too-long URL")
	}
	tasks, _ := s.ListTasks(ctx(), store.ListTasksOptions{})
	if len(tasks) != 0 {
		t.Errorf("rollback failed: %d tasks exist", len(tasks))
	}
}

func TestCreateTask_WithSubtaskAndLinks(t *testing.T) {
	s := newTestStore(t)
	parent, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	child, err := s.CreateTask(ctx(), store.CreateTaskOptions{
		ParentID: &parent.ID,
		Title:    "Child",
		Links:    []model.LinkInput{{Type: model.LinkURL, URL: "https://example.com/doc"}},
	})
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("parent linkage missing or wrong: got %v, want %d", child.ParentID, parent.ID)
	}
	if len(child.Links) != 1 {
		t.Errorf("links = %d, want 1", len(child.Links))
	}
}

func TestCreateTask_NilLinks_Backcompat(t *testing.T) {
	s := newTestStore(t)
	t1, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Nil"})
	if err != nil {
		t.Fatalf("nil links: %v", err)
	}
	if len(t1.Links) != 0 {
		t.Errorf("nil links should yield empty Links, got %d", len(t1.Links))
	}
	t2, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Empty", Links: []model.LinkInput{}})
	if err != nil {
		t.Fatalf("empty links: %v", err)
	}
	if len(t2.Links) != 0 {
		t.Errorf("empty links should yield empty Links, got %d", len(t2.Links))
	}
}
