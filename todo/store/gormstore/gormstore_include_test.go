package gormstore_test

import (
	"errors"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
)

// busyFixture holds IDs from makeBusyTask for easy reference.
type busyFixture struct {
	s       *gormstore.GormStore
	taskID  uint
	childID uint
}

// makeBusyTask creates a task with description, a note, a link, a child, a
// blocker, and another task that this one blocks.
func makeBusyTask(t *testing.T) busyFixture {
	t.Helper()
	s := newTestStore(t)
	task, err := s.CreateTask(ctx(), "Task", "A description.", 5, nil, []string{"tag1"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	blocker, err := s.CreateTask(ctx(), "Blocker", "", 5, nil, nil)
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	if _, err := s.AddBlockers(ctx(), task.ID, []uint{blocker.ID}); err != nil {
		t.Fatalf("add blockers: %v", err)
	}

	// `blocked` is blocked by `task`, so `task.Blocking` should contain `blocked`.
	blocked, err := s.CreateTask(ctx(), "Blocked", "", 5, nil, nil)
	if err != nil {
		t.Fatalf("create blocked: %v", err)
	}
	if _, err := s.AddBlockers(ctx(), blocked.ID, []uint{task.ID}); err != nil {
		t.Fatalf("blocked->task: %v", err)
	}

	if _, err := s.AddNote(ctx(), &task.ID, "a note"); err != nil {
		t.Fatalf("add note: %v", err)
	}
	if _, err := s.AddLink(ctx(), task.ID, model.LinkJira, "PROJ-1", "ticket"); err != nil {
		t.Fatalf("add link: %v", err)
	}
	child, err := s.CreateSubtask(ctx(), task.ID, "Child", "", 5, nil, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	return busyFixture{s: s, taskID: task.ID, childID: child.ID}
}

func TestGetTask_DefaultMinimal(t *testing.T) {
	f := makeBusyTask(t)
	detail, err := f.s.GetTask(ctx(), f.taskID, store.GetTaskOptions{})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Description != nil {
		t.Errorf("Description = %v, want nil", detail.Description)
	}
	if detail.Notes != nil {
		t.Errorf("Notes = %v, want nil", detail.Notes)
	}
	if detail.Links != nil {
		t.Errorf("Links = %v, want nil", detail.Links)
	}
	if detail.Children != nil {
		t.Errorf("Children = %v, want nil", detail.Children)
	}
	if detail.Blockers != nil {
		t.Errorf("Blockers = %v, want nil", detail.Blockers)
	}
	if detail.Parent != nil {
		t.Errorf("Parent = %v, want nil", detail.Parent)
	}
	if detail.Blocking != nil {
		t.Errorf("Blocking = %v, want nil", detail.Blocking)
	}
	if len(detail.Tags) != 1 {
		t.Errorf("Tags = %v, want 1 tag", detail.Tags)
	}
}

func TestGetTask_IncludeNotesOnly(t *testing.T) {
	f := makeBusyTask(t)
	detail, err := f.s.GetTask(ctx(), f.taskID, store.GetTaskOptions{Include: map[string]bool{"notes": true}})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(detail.Notes) != 1 {
		t.Errorf("Notes = %v, want 1", detail.Notes)
	}
	if detail.Description != nil {
		t.Errorf("Description = %v, want nil (not opted in)", detail.Description)
	}
	if detail.Links != nil {
		t.Errorf("Links = %v, want nil", detail.Links)
	}
}

func TestGetTask_IncludeStarLoadsAll(t *testing.T) {
	f := makeBusyTask(t)
	detail, err := f.s.GetTask(ctx(), f.taskID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if d := model.DerefStr(detail.Description); d != "A description." {
		t.Errorf("Description = %q, want 'A description.'", d)
	}
	if len(detail.Notes) != 1 {
		t.Errorf("Notes = %v, want 1", detail.Notes)
	}
	if len(detail.Links) != 1 {
		t.Errorf("Links = %v, want 1", detail.Links)
	}
	if len(detail.Children) != 1 {
		t.Errorf("Children = %v, want 1", detail.Children)
	}
	if len(detail.Blockers) != 1 {
		t.Errorf("Blockers = %v, want 1", detail.Blockers)
	}
	if len(detail.Blocking) != 1 {
		t.Errorf("Blocking = %v, want 1", detail.Blocking)
	}
}

func TestGetTask_IncludeBlocking(t *testing.T) {
	f := makeBusyTask(t)

	detail, err := f.s.GetTask(ctx(), f.taskID, store.GetTaskOptions{})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Blocking != nil {
		t.Errorf("Blocking should be nil without opt-in, got %v", detail.Blocking)
	}

	detail2, err := f.s.GetTask(ctx(), f.taskID, store.GetTaskOptions{Include: map[string]bool{"blocking": true}})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(detail2.Blocking) != 1 {
		t.Errorf("Blocking with opt-in = %v, want 1", detail2.Blocking)
	}
}

func TestGetTask_IncludeParentLoadsParent(t *testing.T) {
	f := makeBusyTask(t)
	detail, err := f.s.GetTask(ctx(), f.childID, store.GetTaskOptions{Include: map[string]bool{"parent": true}})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Parent == nil {
		t.Fatal("Parent should be loaded, got nil")
	}
	if detail.Parent.Title != "Task" {
		t.Errorf("Parent.Title = %q, want Task", detail.Parent.Title)
	}

	detail2, err := f.s.GetTask(ctx(), f.childID, store.GetTaskOptions{})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail2.Parent != nil {
		t.Errorf("Parent should be nil without opt-in, got %v", detail2.Parent)
	}
}

func TestListTasks_DefaultMinimal(t *testing.T) {
	f := makeBusyTask(t)
	items, err := f.s.ListTasks(ctx(), store.ListTasksOptions{IncludeSubtasks: true})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected items")
	}
	for _, it := range items {
		if it.Description != nil {
			t.Errorf("task %d: Description = %v, want nil", it.ID, it.Description)
		}
		if it.Notes != nil {
			t.Errorf("task %d: Notes = %v, want nil", it.ID, it.Notes)
		}
		if it.Links != nil {
			t.Errorf("task %d: Links = %v, want nil", it.ID, it.Links)
		}
	}
}

func TestListTasks_IncludeDescription(t *testing.T) {
	f := makeBusyTask(t)
	items, err := f.s.ListTasks(ctx(), store.ListTasksOptions{
		IncludeSubtasks: true,
		Include:         map[string]bool{"description": true},
	})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	found := false
	for _, it := range items {
		// Other opt-in fields should remain unloaded.
		if it.Notes != nil {
			t.Errorf("task %d: Notes should be nil with description-only include, got %v", it.ID, it.Notes)
		}
		if it.Links != nil {
			t.Errorf("task %d: Links should be nil, got %v", it.ID, it.Links)
		}
		if it.Children != nil {
			t.Errorf("task %d: Children should be nil, got %v", it.ID, it.Children)
		}
		if it.Blockers != nil {
			t.Errorf("task %d: Blockers should be nil, got %v", it.ID, it.Blockers)
		}
		if model.DerefStr(it.Description) == "A description." {
			found = true
		}
	}
	if !found {
		t.Error("expected to find task with description loaded")
	}
}

func TestUpdateTask_DescriptionRoundtrip(t *testing.T) {
	s := newTestStore(t)
	task, err := s.CreateTask(ctx(), "T", "", 0, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	detail := getTaskAll(t, s, ctx(), task.ID)
	if detail.Description != nil {
		t.Errorf("initial Description should be nil, got %v", detail.Description)
	}

	newDesc := "hello"
	if _, err := s.UpdateTask(ctx(), task.ID, store.UpdateTaskOptions{Description: &newDesc}); err != nil {
		t.Fatalf("update: %v", err)
	}
	detail = getTaskAll(t, s, ctx(), task.ID)
	if d := model.DerefStr(detail.Description); d != "hello" {
		t.Errorf("Description = %q, want hello", d)
	}

	empty := ""
	if _, err := s.UpdateTask(ctx(), task.ID, store.UpdateTaskOptions{Description: &empty}); err != nil {
		t.Fatalf("update clear: %v", err)
	}
	detail = getTaskAll(t, s, ctx(), task.ID)
	if detail.Description != nil {
		t.Errorf("Description after clear should be nil, got %v", detail.Description)
	}
}

// makeThreeTasks creates three independent tasks with unique titles and
// returns the store plus their IDs in creation order.
func makeThreeTasks(t *testing.T) (*gormstore.GormStore, []uint) {
	t.Helper()
	s := newTestStore(t)
	ids := make([]uint, 3)
	titles := []string{"Alpha", "Bravo", "Charlie"}
	for i, title := range titles {
		task, err := s.CreateTask(ctx(), title, "", 5, nil, nil)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		ids[i] = task.ID
	}
	return s, ids
}

func TestGetTasks_HappyPath(t *testing.T) {
	s, ids := makeThreeTasks(t)
	result, err := s.GetTasks(ctx(), ids, store.GetTaskOptions{})
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	if len(result.Tasks) != 3 {
		t.Fatalf("Tasks len = %d, want 3", len(result.Tasks))
	}
	if len(result.NotFound) != 0 {
		t.Errorf("NotFound = %v, want empty", result.NotFound)
	}
	for i, want := range ids {
		if result.Tasks[i].ID != want {
			t.Errorf("Tasks[%d].ID = %d, want %d", i, result.Tasks[i].ID, want)
		}
	}
}

func TestGetTasks_PreservesInputOrder(t *testing.T) {
	s, ids := makeThreeTasks(t)
	// Reverse + middle-first ordering.
	input := []uint{ids[2], ids[0], ids[1]}
	result, err := s.GetTasks(ctx(), input, store.GetTaskOptions{})
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	if len(result.Tasks) != 3 {
		t.Fatalf("Tasks len = %d, want 3", len(result.Tasks))
	}
	for i, want := range input {
		if result.Tasks[i].ID != want {
			t.Errorf("Tasks[%d].ID = %d, want %d", i, result.Tasks[i].ID, want)
		}
	}
}

func TestGetTasks_DeduplicatesPreservingFirstOccurrence(t *testing.T) {
	s, ids := makeThreeTasks(t)
	input := []uint{ids[2], ids[0], ids[2], ids[1], ids[0]}
	result, err := s.GetTasks(ctx(), input, store.GetTaskOptions{})
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	want := []uint{ids[2], ids[0], ids[1]}
	if len(result.Tasks) != len(want) {
		t.Fatalf("Tasks len = %d, want %d", len(result.Tasks), len(want))
	}
	for i, w := range want {
		if result.Tasks[i].ID != w {
			t.Errorf("Tasks[%d].ID = %d, want %d", i, result.Tasks[i].ID, w)
		}
	}
}

func TestGetTasks_MissingIDsReportedInNotFound(t *testing.T) {
	s, ids := makeThreeTasks(t)
	input := []uint{ids[0], 99999, ids[1], 88888}
	result, err := s.GetTasks(ctx(), input, store.GetTaskOptions{})
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("Tasks len = %d, want 2", len(result.Tasks))
	}
	if result.Tasks[0].ID != ids[0] || result.Tasks[1].ID != ids[1] {
		t.Errorf("Tasks IDs = [%d, %d], want [%d, %d]", result.Tasks[0].ID, result.Tasks[1].ID, ids[0], ids[1])
	}
	want := []uint{99999, 88888}
	if len(result.NotFound) != len(want) {
		t.Fatalf("NotFound = %v, want %v", result.NotFound, want)
	}
	for i, w := range want {
		if result.NotFound[i] != w {
			t.Errorf("NotFound[%d] = %d, want %d", i, result.NotFound[i], w)
		}
	}
}

func TestGetTasks_IncludesApplyToEveryTask(t *testing.T) {
	f := makeBusyTask(t)
	// Second task with no notes/links to confirm includes apply uniformly.
	plain, err := f.s.CreateTask(ctx(), "Plain", "", 5, nil, nil)
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}

	result, err := f.s.GetTasks(ctx(), []uint{f.taskID, plain.ID}, store.GetTaskOptions{
		Include: map[string]bool{"notes": true, "description": true},
	})
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("Tasks len = %d, want 2", len(result.Tasks))
	}
	if len(result.Tasks[0].Notes) != 1 {
		t.Errorf("busy task Notes = %v, want 1", result.Tasks[0].Notes)
	}
	if d := model.DerefStr(result.Tasks[0].Description); d != "A description." {
		t.Errorf("busy task Description = %q, want 'A description.'", d)
	}
	if len(result.Tasks[1].Notes) != 0 {
		t.Errorf("plain task Notes = %v, want empty", result.Tasks[1].Notes)
	}
}

func TestGetTasks_BlockingMatchesSingleGetTask(t *testing.T) {
	f := makeBusyTask(t)
	single, err := f.s.GetTask(ctx(), f.taskID, store.GetTaskOptions{Include: map[string]bool{"blocking": true}})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	batch, err := f.s.GetTasks(ctx(), []uint{f.taskID}, store.GetTaskOptions{Include: map[string]bool{"blocking": true}})
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	if len(batch.Tasks) != 1 {
		t.Fatalf("Tasks len = %d, want 1", len(batch.Tasks))
	}
	if len(batch.Tasks[0].Blocking) != len(single.Blocking) {
		t.Fatalf("Blocking len = %d, want %d", len(batch.Tasks[0].Blocking), len(single.Blocking))
	}
	if len(single.Blocking) > 0 && batch.Tasks[0].Blocking[0].ID != single.Blocking[0].ID {
		t.Errorf("Blocking[0].ID mismatch: batch=%d single=%d", batch.Tasks[0].Blocking[0].ID, single.Blocking[0].ID)
	}
}

func TestGetTasks_ValidationErrors(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.GetTasks(ctx(), nil, store.GetTaskOptions{}); err == nil {
		t.Error("expected error for nil ids")
	} else {
		var ve *model.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("nil ids: error = %v, want *model.ValidationError", err)
		}
	}

	if _, err := s.GetTasks(ctx(), []uint{}, store.GetTaskOptions{}); err == nil {
		t.Error("expected error for empty ids")
	} else {
		var ve *model.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("empty ids: error = %v, want *model.ValidationError", err)
		}
	}

	tooMany := make([]uint, 101)
	for i := range tooMany {
		tooMany[i] = uint(i + 1)
	}
	if _, err := s.GetTasks(ctx(), tooMany, store.GetTaskOptions{}); err == nil {
		t.Error("expected error for 101 ids")
	} else {
		var ve *model.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("too many ids: error = %v, want *model.ValidationError", err)
		}
	}

	if _, err := s.GetTasks(ctx(), []uint{1, 0, 2}, store.GetTaskOptions{}); err == nil {
		t.Error("expected error for zero ID")
	} else {
		var ve *model.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("zero id: error = %v, want *model.ValidationError", err)
		}
	}
}

func TestGetTasks_EmptyResultStillNonNilSlices(t *testing.T) {
	s := newTestStore(t)
	// One valid ID that doesn't exist.
	result, err := s.GetTasks(ctx(), []uint{99999}, store.GetTaskOptions{})
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	if result.Tasks == nil {
		t.Error("Tasks should be non-nil (empty slice)")
	}
	if result.NotFound == nil {
		t.Error("NotFound should be non-nil (empty slice)")
	}
	if len(result.Tasks) != 0 {
		t.Errorf("Tasks len = %d, want 0", len(result.Tasks))
	}
	if len(result.NotFound) != 1 || result.NotFound[0] != 99999 {
		t.Errorf("NotFound = %v, want [99999]", result.NotFound)
	}
}
