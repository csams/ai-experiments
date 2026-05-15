package gormstore_test

import (
	"errors"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestCreateSubtask(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})

	child, err := s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &parent.ID, Title: "Child", Description: "desc", Priority: 1, Tags: []string{"tag1"}})
	if err != nil {
		t.Fatalf("create subtask: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("parent_id = %v, want %d", child.ParentID, parent.ID)
	}
	if child.Title != "Child" {
		t.Errorf("title = %q, want %q", child.Title, "Child")
	}
	if d := model.DerefStr(child.Description); d != "desc" {
		t.Errorf("description = %q, want %q", d, "desc")
	}
	if child.Priority != 1 {
		t.Errorf("priority = %d, want 1", child.Priority)
	}
	if len(child.Tags) != 1 || child.Tags[0].Tag != "tag1" {
		t.Errorf("tags = %v, want [tag1]", child.Tags)
	}

	detail, _ := s.GetTask(ctx(), parent.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Children) != 1 || detail.Children[0].ID != child.ID {
		t.Errorf("parent children = %v, want [%d]", detail.Children, child.ID)
	}
}

func TestCreateSubtask_NonexistentParent(t *testing.T) {
	s := newTestStore(t)

	_pid := uint(9999)
	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &_pid, Title: "Child"})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateSubtask_InvalidParentID(t *testing.T) {
	s := newTestStore(t)

	_pid := uint(0)
	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &_pid, Title: "Child"})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestCreateSubtask_EmptyTitle(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})

	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &parent.ID, Title: ""})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestCreateSubtask_ArchivedParent(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	s.ArchiveTask(ctx(), parent.ID, true)

	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &parent.ID, Title: "Child"})
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("expected ErrArchived, got %v", err)
	}
}

func TestCreateSubtask_InvalidTags(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})

	_, err := s.CreateTask(ctx(), store.CreateTaskOptions{ParentID: &parent.ID, Title: "Child", Tags: []string{"invalid tag"}})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestSetParent_Basic(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})

	if err := s.SetParent(ctx(), child.ID, &parent.ID); err != nil {
		t.Fatalf("set parent: %v", err)
	}

	detail, _ := s.GetTask(ctx(), parent.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Children) != 1 || detail.Children[0].ID != child.ID {
		t.Errorf("children = %v, want [%d]", detail.Children, child.ID)
	}
}

func TestSetParent_Unparent(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	s.SetParent(ctx(), child.ID, &parent.ID)

	if err := s.SetParent(ctx(), child.ID, nil); err != nil {
		t.Fatalf("unparent: %v", err)
	}

	detail, _ := s.GetTask(ctx(), child.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.ParentID != nil {
		t.Error("expected nil parent_id")
	}
}

func TestSetParent_SelfParent(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	err := s.SetParent(ctx(), task.ID, &task.ID)
	if err == nil {
		t.Fatal("expected error for self-parent")
	}
}

func TestSetParent_CycleDetection(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	c, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "C"})

	s.SetParent(ctx(), b.ID, &a.ID) // A -> B
	s.SetParent(ctx(), c.ID, &b.ID) // B -> C

	// Try to make A a child of C (cycle: A -> B -> C -> A)
	err := s.SetParent(ctx(), a.ID, &c.ID)
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
	var ce *model.CycleDetectedError
	if !errors.As(err, &ce) {
		t.Errorf("expected CycleDetectedError, got %T: %v", err, err)
	}
}

func TestDeleteTask_PromotesSubtasks(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	s.SetParent(ctx(), child.ID, &parent.ID)

	if err := s.DeleteTask(ctx(), parent.ID, store.DeleteTaskOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	detail, _ := s.GetTask(ctx(), child.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if detail.ParentID != nil {
		t.Error("expected child to be promoted to top-level")
	}
}

func TestDeleteTask_Recursive(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	grandchild, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Grandchild"})
	s.SetParent(ctx(), child.ID, &parent.ID)
	s.SetParent(ctx(), grandchild.ID, &child.ID)

	if err := s.DeleteTask(ctx(), parent.ID, store.DeleteTaskOptions{Recursive: true}); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}

	for _, id := range []uint{parent.ID, child.ID, grandchild.ID} {
		_, err := s.GetTask(ctx(), id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("task %d should be deleted", id)
		}
	}
}

func TestDeleteTask_RecursiveBlocksExternal(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	external, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "External"})
	s.SetParent(ctx(), child.ID, &parent.ID)
	s.AddBlockers(ctx(), external.ID, []uint{child.ID}) // child blocks external

	err := s.DeleteTask(ctx(), parent.ID, store.DeleteTaskOptions{Recursive: true})
	if err == nil {
		t.Fatal("expected error: child blocks external task")
	}
	var be *model.BlockingExternalError
	if !errors.As(err, &be) {
		t.Errorf("expected BlockingExternalError, got %T: %v", err, err)
	}
}

func TestDeleteTask_RecursiveBlocksWithinSetOK(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child1, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child1"})
	child2, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child2"})
	s.SetParent(ctx(), child1.ID, &parent.ID)
	s.SetParent(ctx(), child2.ID, &parent.ID)
	s.AddBlockers(ctx(), child2.ID, []uint{child1.ID}) // child1 blocks child2 (both in set)

	// Should succeed since both are in the deletion set
	if err := s.DeleteTask(ctx(), parent.ID, store.DeleteTaskOptions{Recursive: true}); err != nil {
		t.Fatalf("recursive delete should succeed: %v", err)
	}
}

func TestListTasks_ParentFilter(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Root"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	grandchild, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Grandchild"})
	s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Other"}) // not in subtree

	s.SetParent(ctx(), child.ID, &root.ID)
	s.SetParent(ctx(), grandchild.ID, &child.ID)

	tasks, err := s.ListTasks(ctx(), store.ListTasksOptions{ParentID: &root.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Should include root, child, grandchild (not "Other")
	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks in subtree, got %d", len(tasks))
	}
}
