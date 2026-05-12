package gormstore_test

import (
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
