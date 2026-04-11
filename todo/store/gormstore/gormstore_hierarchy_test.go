package gormstore_test

import (
	"errors"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestSetParent_Basic(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask("Parent", "", 0, nil, nil)
	child, _ := s.CreateTask("Child", "", 0, nil, nil)

	if err := s.SetParent(child.ID, &parent.ID); err != nil {
		t.Fatalf("set parent: %v", err)
	}

	detail, _ := s.GetTask(parent.ID)
	if len(detail.Children) != 1 || detail.Children[0].ID != child.ID {
		t.Errorf("children = %v, want [%d]", detail.Children, child.ID)
	}
}

func TestSetParent_Unparent(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask("Parent", "", 0, nil, nil)
	child, _ := s.CreateTask("Child", "", 0, nil, nil)
	s.SetParent(child.ID, &parent.ID)

	if err := s.SetParent(child.ID, nil); err != nil {
		t.Fatalf("unparent: %v", err)
	}

	detail, _ := s.GetTask(child.ID)
	if detail.ParentID != nil {
		t.Error("expected nil parent_id")
	}
}

func TestSetParent_SelfParent(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask("Task", "", 0, nil, nil)

	err := s.SetParent(task.ID, &task.ID)
	if err == nil {
		t.Fatal("expected error for self-parent")
	}
}

func TestSetParent_CycleDetection(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask("A", "", 0, nil, nil)
	b, _ := s.CreateTask("B", "", 0, nil, nil)
	c, _ := s.CreateTask("C", "", 0, nil, nil)

	s.SetParent(b.ID, &a.ID) // A -> B
	s.SetParent(c.ID, &b.ID) // B -> C

	// Try to make A a child of C (cycle: A -> B -> C -> A)
	err := s.SetParent(a.ID, &c.ID)
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
	parent, _ := s.CreateTask("Parent", "", 0, nil, nil)
	child, _ := s.CreateTask("Child", "", 0, nil, nil)
	s.SetParent(child.ID, &parent.ID)

	if err := s.DeleteTask(parent.ID, false); err != nil {
		t.Fatalf("delete: %v", err)
	}

	detail, _ := s.GetTask(child.ID)
	if detail.ParentID != nil {
		t.Error("expected child to be promoted to top-level")
	}
}

func TestDeleteTask_Recursive(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask("Parent", "", 0, nil, nil)
	child, _ := s.CreateTask("Child", "", 0, nil, nil)
	grandchild, _ := s.CreateTask("Grandchild", "", 0, nil, nil)
	s.SetParent(child.ID, &parent.ID)
	s.SetParent(grandchild.ID, &child.ID)

	if err := s.DeleteTask(parent.ID, true); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}

	for _, id := range []uint{parent.ID, child.ID, grandchild.ID} {
		_, err := s.GetTask(id)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("task %d should be deleted", id)
		}
	}
}

func TestDeleteTask_RecursiveBlocksExternal(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask("Parent", "", 0, nil, nil)
	child, _ := s.CreateTask("Child", "", 0, nil, nil)
	external, _ := s.CreateTask("External", "", 0, nil, nil)
	s.SetParent(child.ID, &parent.ID)
	s.AddBlockers(external.ID, []uint{child.ID}) // child blocks external

	err := s.DeleteTask(parent.ID, true)
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
	parent, _ := s.CreateTask("Parent", "", 0, nil, nil)
	child1, _ := s.CreateTask("Child1", "", 0, nil, nil)
	child2, _ := s.CreateTask("Child2", "", 0, nil, nil)
	s.SetParent(child1.ID, &parent.ID)
	s.SetParent(child2.ID, &parent.ID)
	s.AddBlockers(child2.ID, []uint{child1.ID}) // child1 blocks child2 (both in set)

	// Should succeed since both are in the deletion set
	if err := s.DeleteTask(parent.ID, true); err != nil {
		t.Fatalf("recursive delete should succeed: %v", err)
	}
}

func TestListTasks_ParentFilter(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.CreateTask("Root", "", 0, nil, nil)
	child, _ := s.CreateTask("Child", "", 0, nil, nil)
	grandchild, _ := s.CreateTask("Grandchild", "", 0, nil, nil)
	s.CreateTask("Other", "", 0, nil, nil) // not in subtree

	s.SetParent(child.ID, &root.ID)
	s.SetParent(grandchild.ID, &child.ID)

	tasks, err := s.ListTasks(store.ListTasksOptions{ParentID: &root.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Should include root, child, grandchild (not "Other")
	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks in subtree, got %d", len(tasks))
	}
}
