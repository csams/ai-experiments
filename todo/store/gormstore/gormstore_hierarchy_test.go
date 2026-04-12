package gormstore_test

import (
	"errors"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestCreateSubtask(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)

	child, err := s.CreateSubtask(ctx(), parent.ID, "Child", "desc", 1, nil, []string{"tag1"})
	if err != nil {
		t.Fatalf("create subtask: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("parent_id = %v, want %d", child.ParentID, parent.ID)
	}
	if child.Title != "Child" {
		t.Errorf("title = %q, want %q", child.Title, "Child")
	}
	if child.Description != "desc" {
		t.Errorf("description = %q, want %q", child.Description, "desc")
	}
	if child.Priority != 1 {
		t.Errorf("priority = %d, want 1", child.Priority)
	}
	if len(child.Tags) != 1 || child.Tags[0].Tag != "tag1" {
		t.Errorf("tags = %v, want [tag1]", child.Tags)
	}

	detail, _ := s.GetTask(ctx(), parent.ID)
	if len(detail.Children) != 1 || detail.Children[0].ID != child.ID {
		t.Errorf("parent children = %v, want [%d]", detail.Children, child.ID)
	}
}

func TestCreateSubtask_NonexistentParent(t *testing.T) {
	s := newTestStore(t)

	_, err := s.CreateSubtask(ctx(), 9999, "Child", "", 0, nil, nil)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateSubtask_InvalidParentID(t *testing.T) {
	s := newTestStore(t)

	_, err := s.CreateSubtask(ctx(), 0, "Child", "", 0, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestCreateSubtask_EmptyTitle(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)

	_, err := s.CreateSubtask(ctx(), parent.ID, "", "", 0, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestCreateSubtask_ArchivedParent(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	s.ArchiveTask(ctx(), parent.ID, true)

	_, err := s.CreateSubtask(ctx(), parent.ID, "Child", "", 0, nil, nil)
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("expected ErrArchived, got %v", err)
	}
}

func TestCreateSubtask_InvalidTags(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)

	_, err := s.CreateSubtask(ctx(), parent.ID, "Child", "", 0, nil, []string{"invalid tag"})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestSetParent_Basic(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	child, _ := s.CreateTask(ctx(), "Child", "", 0, nil, nil)

	if err := s.SetParent(ctx(), child.ID, &parent.ID); err != nil {
		t.Fatalf("set parent: %v", err)
	}

	detail, _ := s.GetTask(ctx(), parent.ID)
	if len(detail.Children) != 1 || detail.Children[0].ID != child.ID {
		t.Errorf("children = %v, want [%d]", detail.Children, child.ID)
	}
}

func TestSetParent_Unparent(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	child, _ := s.CreateTask(ctx(), "Child", "", 0, nil, nil)
	s.SetParent(ctx(), child.ID, &parent.ID)

	if err := s.SetParent(ctx(), child.ID, nil); err != nil {
		t.Fatalf("unparent: %v", err)
	}

	detail, _ := s.GetTask(ctx(), child.ID)
	if detail.ParentID != nil {
		t.Error("expected nil parent_id")
	}
}

func TestSetParent_SelfParent(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	err := s.SetParent(ctx(), task.ID, &task.ID)
	if err == nil {
		t.Fatal("expected error for self-parent")
	}
}

func TestSetParent_CycleDetection(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), "A", "", 0, nil, nil)
	b, _ := s.CreateTask(ctx(), "B", "", 0, nil, nil)
	c, _ := s.CreateTask(ctx(), "C", "", 0, nil, nil)

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
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	child, _ := s.CreateTask(ctx(), "Child", "", 0, nil, nil)
	s.SetParent(ctx(), child.ID, &parent.ID)

	if err := s.DeleteTask(ctx(), parent.ID, false); err != nil {
		t.Fatalf("delete: %v", err)
	}

	detail, _ := s.GetTask(ctx(), child.ID)
	if detail.ParentID != nil {
		t.Error("expected child to be promoted to top-level")
	}
}

func TestDeleteTask_Recursive(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	child, _ := s.CreateTask(ctx(), "Child", "", 0, nil, nil)
	grandchild, _ := s.CreateTask(ctx(), "Grandchild", "", 0, nil, nil)
	s.SetParent(ctx(), child.ID, &parent.ID)
	s.SetParent(ctx(), grandchild.ID, &child.ID)

	if err := s.DeleteTask(ctx(), parent.ID, true); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}

	for _, id := range []uint{parent.ID, child.ID, grandchild.ID} {
		_, err := s.GetTask(ctx(), id)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("task %d should be deleted", id)
		}
	}
}

func TestDeleteTask_RecursiveBlocksExternal(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	child, _ := s.CreateTask(ctx(), "Child", "", 0, nil, nil)
	external, _ := s.CreateTask(ctx(), "External", "", 0, nil, nil)
	s.SetParent(ctx(), child.ID, &parent.ID)
	s.AddBlockers(ctx(), external.ID, []uint{child.ID}) // child blocks external

	err := s.DeleteTask(ctx(), parent.ID, true)
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
	parent, _ := s.CreateTask(ctx(), "Parent", "", 0, nil, nil)
	child1, _ := s.CreateTask(ctx(), "Child1", "", 0, nil, nil)
	child2, _ := s.CreateTask(ctx(), "Child2", "", 0, nil, nil)
	s.SetParent(ctx(), child1.ID, &parent.ID)
	s.SetParent(ctx(), child2.ID, &parent.ID)
	s.AddBlockers(ctx(), child2.ID, []uint{child1.ID}) // child1 blocks child2 (both in set)

	// Should succeed since both are in the deletion set
	if err := s.DeleteTask(ctx(), parent.ID, true); err != nil {
		t.Fatalf("recursive delete should succeed: %v", err)
	}
}

func TestListTasks_ParentFilter(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.CreateTask(ctx(), "Root", "", 0, nil, nil)
	child, _ := s.CreateTask(ctx(), "Child", "", 0, nil, nil)
	grandchild, _ := s.CreateTask(ctx(), "Grandchild", "", 0, nil, nil)
	s.CreateTask(ctx(), "Other", "", 0, nil, nil) // not in subtree

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
