package gormstore_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func TestCheckpoint_SetAndGet(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)

	cp, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{
		Recap:       "explored model layer",
		NextSteps:   "extend store interface",
		OpenThreads: "decide CHK column placement",
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if cp.ID == 0 || cp.TaskID != task.ID {
		t.Errorf("checkpoint not persisted: %+v", cp)
	}
	if cp.Recap != "explored model layer" || cp.NextSteps != "extend store interface" || cp.OpenThreads != "decide CHK column placement" {
		t.Errorf("fields not saved: %+v", cp)
	}
	if cp.CreatedAt.IsZero() || cp.UpdatedAt.IsZero() {
		t.Errorf("timestamps zero: %+v", cp)
	}

	got, err := s.GetCheckpoint(ctx(), task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != cp.ID {
		t.Errorf("get returned different row: %d vs %d", got.ID, cp.ID)
	}
}

func TestCheckpoint_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)

	_, err := s.GetCheckpoint(ctx(), task.ID)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCheckpoint_GetUnknownTask(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetCheckpoint(ctx(), 999)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown task, got %v", err)
	}
}

func TestCheckpoint_SetIsUpsert(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)

	first, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{
		Recap: "r1", NextSteps: "n1",
	})
	if err != nil {
		t.Fatalf("first set: %v", err)
	}
	// Sleep so UpdatedAt strictly advances on the upsert. Without this, SQLite's
	// truncated-to-millisecond DATETIME would let same-tick writes pass even if
	// updated_at weren't being persisted at all.
	time.Sleep(10 * time.Millisecond)
	second, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{
		Recap: "r2", NextSteps: "n2", OpenThreads: "o2",
	})
	if err != nil {
		t.Fatalf("second set: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("upsert created a new row: %d vs %d", second.ID, first.ID)
	}
	if second.Recap != "r2" || second.NextSteps != "n2" || second.OpenThreads != "o2" {
		t.Errorf("fields not updated: %+v", second)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: %v -> %v", first.UpdatedAt, second.UpdatedAt)
	}
	// CreatedAt must not change on update.
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt changed on update: %v -> %v", first.CreatedAt, second.CreatedAt)
	}

	// Singleton guarantee: only one row exists for this task.
	var count int64
	s.DB().Model(&model.Checkpoint{}).Where("task_id = ?", task.ID).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 checkpoint row, got %d", count)
	}
}

func TestCheckpoint_Delete(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)
	_, _ = s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: "n"})

	if err := s.DeleteCheckpoint(ctx(), task.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetCheckpoint(ctx(), task.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
	// Deleting again returns ErrNotFound.
	if err := s.DeleteCheckpoint(ctx(), task.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound on re-delete, got %v", err)
	}
}

func TestCheckpoint_CascadeOnTaskDelete(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)
	_, _ = s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: "n"})

	if err := s.DeleteTask(ctx(), task.ID, store.DeleteTaskOptions{}); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	var count int64
	s.DB().Model(&model.Checkpoint{}).Where("task_id = ?", task.ID).Count(&count)
	if count != 0 {
		t.Errorf("expected checkpoint to be deleted with task, got %d rows", count)
	}
}

func TestCheckpoint_RejectsEmptyRecapOrNext(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)

	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "", NextSteps: "n"}); err == nil {
		t.Errorf("expected validation error for empty recap")
	}
	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: ""}); err == nil {
		t.Errorf("expected validation error for empty next_steps")
	}
}

func TestCheckpoint_RejectsOversizedField(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)

	big := strings.Repeat("a", 10001)
	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: big, NextSteps: "n"}); err == nil {
		t.Errorf("expected validation error for oversized recap")
	}
	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: big}); err == nil {
		t.Errorf("expected validation error for oversized next_steps")
	}
	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: "n", OpenThreads: big}); err == nil {
		t.Errorf("expected validation error for oversized open_threads")
	}
}

func TestCheckpoint_SetRejectsArchivedTask(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)
	if err := s.ArchiveTask(ctx(), task.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	_, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: "n"})
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("expected ErrArchived, got %v", err)
	}
}

func TestCheckpoint_GetAndDeleteAllowArchivedTask(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)
	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: "n"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.ArchiveTask(ctx(), task.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := s.GetCheckpoint(ctx(), task.ID); err != nil {
		t.Errorf("get on archived task should succeed: %v", err)
	}
	if err := s.DeleteCheckpoint(ctx(), task.ID); err != nil {
		t.Errorf("delete on archived task should succeed: %v", err)
	}
}

func TestCheckpoint_GetTaskIncludesCheckpoint(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)
	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{
		Recap: "r", NextSteps: "n", OpenThreads: "o",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	detail, err := s.GetTask(ctx(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Checkpoint == nil {
		t.Fatalf("expected checkpoint preloaded; got nil")
	}
	if detail.Checkpoint.Recap != "r" || detail.Checkpoint.NextSteps != "n" || detail.Checkpoint.OpenThreads != "o" {
		t.Errorf("checkpoint fields wrong: %+v", detail.Checkpoint)
	}
}

func TestCheckpoint_ListTasksHasCheckpointFlag(t *testing.T) {
	s := newTestStore(t)
	t1, _ := s.CreateTask(ctx(), "T1", "", 0, nil, nil)
	t2, _ := s.CreateTask(ctx(), "T2", "", 0, nil, nil)
	if _, err := s.SetCheckpoint(ctx(), t1.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: "n"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	items, err := s.ListTasks(ctx(), store.ListTasksOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(items))
	}
	byID := map[uint]model.TaskListItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if !byID[t1.ID].HasCheckpoint {
		t.Errorf("t1 should have HasCheckpoint=true")
	}
	if byID[t2.ID].HasCheckpoint {
		t.Errorf("t2 should have HasCheckpoint=false")
	}
}

func TestCheckpoint_EmitsCreatedThenUpdatedEvents(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)

	obs := &recordingObserver{}
	s.AddObserver(obs)

	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r1", NextSteps: "n1"}); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if _, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r2", NextSteps: "n1"}); err != nil {
		t.Fatalf("second set: %v", err)
	}

	var sawCreated, sawUpdated bool
	for _, e := range obs.events {
		switch e.Type {
		case "checkpoint.created":
			sawCreated = true
			if len(e.TaskIDs) != 1 || e.TaskIDs[0] != task.ID {
				t.Errorf("created TaskIDs = %v, want [%d]", e.TaskIDs, task.ID)
			}
		case "checkpoint.updated":
			sawUpdated = true
			if c, ok := e.Changes["recap"]; !ok {
				t.Errorf("expected 'recap' in Changes, got %v", e.Changes)
			} else if c.Old != "r1" || c.New != "r2" {
				t.Errorf("recap change wrong: %+v", c)
			}
			if _, ok := e.Changes["next_steps"]; ok {
				t.Errorf("next_steps did not change but is in Changes: %+v", e.Changes)
			}
		}
	}
	if !sawCreated {
		t.Errorf("missing checkpoint.created event; got %+v", obs.events)
	}
	if !sawUpdated {
		t.Errorf("missing checkpoint.updated event; got %+v", obs.events)
	}
}

func TestCheckpoint_CascadeOnRecursiveSubtreeDelete(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), "P", "", 0, nil, nil)
	child1, _ := s.CreateSubtask(ctx(), parent.ID, "C1", "", 0, nil, nil)
	child2, _ := s.CreateSubtask(ctx(), parent.ID, "C2", "", 0, nil, nil)
	for _, id := range []uint{parent.ID, child1.ID, child2.ID} {
		if _, err := s.SetCheckpoint(ctx(), id, store.SetCheckpointOptions{Recap: "r", NextSteps: "n"}); err != nil {
			t.Fatalf("set on %d: %v", id, err)
		}
	}

	if err := s.DeleteTask(ctx(), parent.ID, store.DeleteTaskOptions{Recursive: true}); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}

	var count int64
	s.DB().Model(&model.Checkpoint{}).
		Where("task_id IN ?", []uint{parent.ID, child1.ID, child2.ID}).
		Count(&count)
	if count != 0 {
		t.Errorf("expected all 3 checkpoints to be deleted, got %d remaining", count)
	}
}

func TestCheckpoint_NoOpUpdateSkipsEvent(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)
	first, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{
		Recap: "r", NextSteps: "n", OpenThreads: "o",
	})
	if err != nil {
		t.Fatalf("first set: %v", err)
	}

	obs := &recordingObserver{}
	s.AddObserver(obs)

	// Call SetCheckpoint again with identical contents — should be a no-op.
	second, err := s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{
		Recap: "r", NextSteps: "n", OpenThreads: "o",
	})
	if err != nil {
		t.Fatalf("no-op set: %v", err)
	}
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("no-op set bumped UpdatedAt: %v -> %v", first.UpdatedAt, second.UpdatedAt)
	}
	if len(obs.events) != 0 {
		t.Errorf("no-op set emitted events: %+v", obs.events)
	}
}

func TestCheckpoint_EmitsDeletedEvent(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "T", "", 0, nil, nil)
	_, _ = s.SetCheckpoint(ctx(), task.ID, store.SetCheckpointOptions{Recap: "r", NextSteps: "n"})

	obs := &recordingObserver{}
	s.AddObserver(obs)

	if err := s.DeleteCheckpoint(ctx(), task.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var saw bool
	for _, e := range obs.events {
		if e.Type == "checkpoint.deleted" {
			saw = true
			if len(e.TaskIDs) != 1 || e.TaskIDs[0] != task.ID {
				t.Errorf("deleted TaskIDs = %v, want [%d]", e.TaskIDs, task.ID)
			}
		}
	}
	if !saw {
		t.Errorf("missing checkpoint.deleted event; got %+v", obs.events)
	}
}
