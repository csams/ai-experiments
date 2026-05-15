package gormstore_test

import (
	"errors"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestArchive_Basic(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Task"})

	if err := s.ArchiveTask(ctx(), task.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	detail, _ := s.GetTask(ctx(), task.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if !detail.Archived {
		t.Error("expected archived = true")
	}

	// Should be excluded from default list
	tasks, _ := s.ListTasks(ctx(), store.ListTasksOptions{})
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks in default list, got %d", len(tasks))
	}

	// Should appear with IncludeArchived
	tasks, _ = s.ListTasks(ctx(), store.ListTasksOptions{IncludeArchived: true})
	if len(tasks) != 1 {
		t.Errorf("expected 1 task with IncludeArchived, got %d", len(tasks))
	}
}

func TestArchive_CascadesToSubtree(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	s.SetParent(ctx(), child.ID, &parent.ID)

	if err := s.ArchiveTask(ctx(), parent.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	childDetail, _ := s.GetTask(ctx(), child.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if !childDetail.Archived {
		t.Error("expected child to be archived")
	}
}

func TestArchive_FailsIfBlockingExternal(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), b.ID, []uint{a.ID}) // A blocks B

	err := s.ArchiveTask(ctx(), a.ID, true)
	if err == nil {
		t.Fatal("expected error: A blocks B (external)")
	}
	var be *model.BlockingExternalError
	if !errors.As(err, &be) {
		t.Errorf("expected BlockingExternalError, got %T", err)
	}
}

func TestUnarchive_CascadesToSubtree(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	s.SetParent(ctx(), child.ID, &parent.ID)
	s.ArchiveTask(ctx(), parent.ID, true)

	if err := s.ArchiveTask(ctx(), parent.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}

	parentDetail, _ := s.GetTask(ctx(), parent.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	childDetail, _ := s.GetTask(ctx(), child.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if parentDetail.Archived || childDetail.Archived {
		t.Error("expected both to be unarchived")
	}
}

func TestUnarchive_CleansUpInvalidBlockers(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "B"})
	s.AddBlockers(ctx(), a.ID, []uint{b.ID}) // B blocks A

	// Archive A (no external blocking since A doesn't block anything)
	s.ArchiveTask(ctx(), a.ID, true)

	// Complete B while A is archived
	s.SetTaskState(ctx(), b.ID, model.StateDone)

	// Unarchive A — should clean up the stale blocker (B is Done)
	if err := s.ArchiveTask(ctx(), a.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}

	detail, _ := s.GetTask(ctx(), a.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if len(detail.Blockers) != 0 {
		t.Errorf("expected stale blocker to be cleaned up, got %d blockers", len(detail.Blockers))
	}
	if detail.State == model.StateBlocked {
		t.Error("expected task to no longer be Blocked after stale blocker cleanup")
	}
}
