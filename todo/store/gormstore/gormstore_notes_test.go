package gormstore_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

func uintPtr(v uint) *uint   { return &v }
func strPtr(s string) *string { return &s }

func TestNotes_StandaloneCRUD(t *testing.T) {
	s := newTestStore(t)

	// Add a standalone note (taskID nil).
	note, err := s.AddNote(ctx(), nil, "standalone capture")
	if err != nil {
		t.Fatalf("add standalone: %v", err)
	}
	if note.TaskID != nil {
		t.Errorf("standalone note has TaskID = %v, want nil", note.TaskID)
	}

	// Scope=standalone → orphan-only.
	standalone, err := s.ListNotes(ctx(), store.ListNotesOptions{Scope: store.NoteScopeStandalone})
	if err != nil {
		t.Fatalf("list standalone: %v", err)
	}
	if len(standalone) != 1 || standalone[0].ID != note.ID {
		t.Errorf("standalone listing got %d notes; want 1", len(standalone))
	}

	// Update text via new API.
	updated, err := s.UpdateNote(ctx(), note.ID, store.UpdateNoteOptions{Text: strPtr("updated")})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Text != "updated" {
		t.Errorf("text not updated: %q", updated.Text)
	}

	// Delete by note id alone.
	if err := s.DeleteNote(ctx(), note.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	standalone, _ = s.ListNotes(ctx(), store.ListNotesOptions{Scope: store.NoteScopeStandalone})
	if len(standalone) != 0 {
		t.Errorf("after delete, standalone count = %d, want 0", len(standalone))
	}
}

func TestNotes_AddNoteNilTaskID_NoTaskCheck(t *testing.T) {
	s := newTestStore(t)
	// No tasks exist; standalone note should still succeed.
	if _, err := s.AddNote(ctx(), nil, "no task"); err != nil {
		t.Fatalf("standalone add should not require any task: %v", err)
	}
}

func TestNotes_Reparent(t *testing.T) {
	s := newTestStore(t)
	t1, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T1"})
	t2, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T2"})
	note, _ := s.AddNote(ctx(), &t1.ID, "n")

	updated, err := s.UpdateNote(ctx(), note.ID, store.UpdateNoteOptions{
		SetTaskID: true,
		TaskID:    &t2.ID,
	})
	if err != nil {
		t.Fatalf("reparent: %v", err)
	}
	if updated.TaskID == nil || *updated.TaskID != t2.ID {
		t.Errorf("reparented TaskID = %v, want %d", updated.TaskID, t2.ID)
	}

	t1Notes, _ := s.ListNotes(ctx(), store.ListNotesOptions{TaskID: &t1.ID})
	t2Notes, _ := s.ListNotes(ctx(), store.ListNotesOptions{TaskID: &t2.ID})
	if len(t1Notes) != 0 || len(t2Notes) != 1 {
		t.Errorf("expected note to move from t1 (now %d) to t2 (now %d)", len(t1Notes), len(t2Notes))
	}
}

func TestNotes_OrphanByClearingTaskID(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	note, _ := s.AddNote(ctx(), &task.ID, "n")

	updated, err := s.UpdateNote(ctx(), note.ID, store.UpdateNoteOptions{
		SetTaskID: true,
		TaskID:    nil,
	})
	if err != nil {
		t.Fatalf("clear task: %v", err)
	}
	if updated.TaskID != nil {
		t.Errorf("expected TaskID to be cleared, got %v", updated.TaskID)
	}
	standalone, _ := s.ListNotes(ctx(), store.ListNotesOptions{Scope: store.NoteScopeStandalone})
	if len(standalone) != 1 {
		t.Errorf("standalone count after clear = %d, want 1", len(standalone))
	}
}

func TestNotes_ArchiveStandalone(t *testing.T) {
	s := newTestStore(t)
	note, _ := s.AddNote(ctx(), nil, "old")

	if err := s.ArchiveNote(ctx(), note.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	all, _ := s.ListNotes(ctx(), store.ListNotesOptions{IncludeArchived: true})
	if len(all) != 1 || !all[0].Archived {
		t.Errorf("archive flag not persisted: %+v", all)
	}

	if err := s.ArchiveNote(ctx(), note.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	all, _ = s.ListNotes(ctx(), store.ListNotesOptions{IncludeArchived: true})
	if all[0].Archived {
		t.Errorf("unarchive failed: %+v", all)
	}
}

func TestNotes_ListAll(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	s.AddNote(ctx(), &task.ID, "attached")
	s.AddNote(ctx(), nil, "standalone")

	all, err := s.ListNotes(ctx(), store.ListNotesOptions{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("default scope returned %d, want 2", len(all))
	}
}

func TestNotes_ListScopeAttached(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	s.AddNote(ctx(), &task.ID, "attached")
	s.AddNote(ctx(), nil, "standalone")

	attached, err := s.ListNotes(ctx(), store.ListNotesOptions{Scope: store.NoteScopeAttached})
	if err != nil {
		t.Fatalf("list attached: %v", err)
	}
	if len(attached) != 1 || attached[0].TaskID == nil {
		t.Errorf("attached scope got %+v", attached)
	}
}

func TestNotes_ListQueryFilter(t *testing.T) {
	s := newTestStore(t)
	s.AddNote(ctx(), nil, "alpha bravo")
	s.AddNote(ctx(), nil, "charlie delta")

	got, err := s.ListNotes(ctx(), store.ListNotesOptions{Query: "alpha"})
	if err != nil {
		t.Fatalf("list query: %v", err)
	}
	if len(got) != 1 || got[0].Text != "alpha bravo" {
		t.Errorf("query filter got %+v", got)
	}
}

func TestNotes_TaskIDOverridesScope(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	attached, _ := s.AddNote(ctx(), &task.ID, "attached")
	s.AddNote(ctx(), nil, "orphan")

	// TaskID is set together with Scope=Standalone; TaskID must win.
	got, err := s.ListNotes(ctx(), store.ListNotesOptions{
		TaskID: &task.ID,
		Scope:  store.NoteScopeStandalone,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != attached.ID {
		t.Errorf("TaskID should override Scope; got %+v", got)
	}
}

func TestNotes_AttachedExcludesArchivedByDefault(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	live, _ := s.AddNote(ctx(), &task.ID, "live attached")
	archived, _ := s.AddNote(ctx(), &task.ID, "archived attached")
	if err := s.ArchiveNote(ctx(), archived.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	got, err := s.ListNotes(ctx(), store.ListNotesOptions{Scope: store.NoteScopeAttached})
	if err != nil {
		t.Fatalf("list attached: %v", err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Errorf("attached+default should exclude archived; got %+v", got)
	}

	withArchived, err := s.ListNotes(ctx(), store.ListNotesOptions{
		Scope:           store.NoteScopeAttached,
		IncludeArchived: true,
	})
	if err != nil {
		t.Fatalf("list attached include_archived: %v", err)
	}
	if len(withArchived) != 2 {
		t.Errorf("attached+include_archived count = %d, want 2", len(withArchived))
	}
}

func TestNotes_InvalidScopeRejected(t *testing.T) {
	s := newTestStore(t)
	_, err := s.ListNotes(ctx(), store.ListNotesOptions{Scope: store.NoteScope("bogus")})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError for invalid scope, got %v", err)
	}
	if ve.Field != "scope" {
		t.Errorf("ValidationError.Field = %q, want %q", ve.Field, "scope")
	}
}

func TestNotes_QueryAppliesDefaultLimit(t *testing.T) {
	s := newTestStore(t)
	// Create enough matching notes to exceed defaultQueryLimit (200).
	for i := 0; i < 205; i++ {
		if _, err := s.AddNote(ctx(), nil, "needle"); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	got, err := s.ListNotes(ctx(), store.ListNotesOptions{Query: "needle"})
	if err != nil {
		t.Fatalf("list query: %v", err)
	}
	if len(got) != 200 {
		t.Errorf("query without explicit Limit should default-cap at 200; got %d", len(got))
	}

	// Explicit Limit > 0 overrides the default cap.
	got, err = s.ListNotes(ctx(), store.ListNotesOptions{Query: "needle", Limit: 5})
	if err != nil {
		t.Fatalf("list query limit=5: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("explicit Limit=5 returned %d", len(got))
	}
}

func TestNotes_UpdateNote_NoFields_Errors(t *testing.T) {
	s := newTestStore(t)
	note, _ := s.AddNote(ctx(), nil, "n")

	_, err := s.UpdateNote(ctx(), note.ID, store.UpdateNoteOptions{})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for empty opts, got %v", err)
	}
}

func TestNotes_UpdateNote_ReparentToArchivedFails(t *testing.T) {
	s := newTestStore(t)
	archived, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Archived"})
	if err := s.ArchiveTask(ctx(), archived.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	note, _ := s.AddNote(ctx(), nil, "n")

	_, err := s.UpdateNote(ctx(), note.ID, store.UpdateNoteOptions{
		SetTaskID: true,
		TaskID:    &archived.ID,
	})
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("expected ErrArchived reparent target, got %v", err)
	}
}

func TestNote_JSON_OmitsTaskIDWhenNil(t *testing.T) {
	s := newTestStore(t)
	note, _ := s.AddNote(ctx(), nil, "n")

	b, err := json.Marshal(note)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "task_id") {
		t.Errorf("standalone note JSON should omit task_id, got: %s", b)
	}

	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	attached, _ := s.AddNote(ctx(), &task.ID, "n2")
	b2, _ := json.Marshal(attached)
	if !strings.Contains(string(b2), `"task_id"`) {
		t.Errorf("attached note JSON should include task_id, got: %s", b2)
	}
}

func TestDeleteTask_OrphansNotesByDefault(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	note, _ := s.AddNote(ctx(), &task.ID, "n")

	if err := s.DeleteTask(ctx(), task.ID, store.DeleteTaskOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, _ := s.ListNotes(ctx(), store.ListNotesOptions{IncludeArchived: true})
	if len(all) != 1 || all[0].ID != note.ID || all[0].TaskID != nil {
		t.Errorf("expected note to be orphaned, got %+v", all)
	}
}

func TestDeleteTask_DeleteNotesFlag(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	s.AddNote(ctx(), &task.ID, "n")

	if err := s.DeleteTask(ctx(), task.ID, store.DeleteTaskOptions{DeleteNotes: true}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, _ := s.ListNotes(ctx(), store.ListNotesOptions{IncludeArchived: true})
	if len(all) != 0 {
		t.Errorf("expected notes hard-deleted, got %d", len(all))
	}
}

func TestDeleteTask_RecursiveOrphansAllSubtreeNotes(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "Child"})
	s.SetParent(ctx(), child.ID, &parent.ID)
	s.AddNote(ctx(), &parent.ID, "p-note")
	s.AddNote(ctx(), &child.ID, "c-note")

	if err := s.DeleteTask(ctx(), parent.ID, store.DeleteTaskOptions{Recursive: true}); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}
	all, _ := s.ListNotes(ctx(), store.ListNotesOptions{IncludeArchived: true})
	if len(all) != 2 {
		t.Errorf("expected both notes to survive as standalone, got %d", len(all))
	}
	for _, n := range all {
		if n.TaskID != nil {
			t.Errorf("note %d still has TaskID %v", n.ID, n.TaskID)
		}
	}
}

func TestDeleteTask_OrphanEvent_Emitted(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	note, _ := s.AddNote(ctx(), &task.ID, "n")

	obs := &recordingObserver{}
	s.AddObserver(obs)

	if err := s.DeleteTask(ctx(), task.ID, store.DeleteTaskOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var sawNoteUpdated, sawTaskDeleted bool
	for _, e := range obs.events {
		switch e.Type {
		case "note.updated":
			sawNoteUpdated = true
			if len(e.NoteIDs) != 1 || e.NoteIDs[0] != note.ID {
				t.Errorf("note.updated NoteIDs = %v, want [%d]", e.NoteIDs, note.ID)
			}
		case "task.deleted":
			sawTaskDeleted = true
		}
	}
	if !sawNoteUpdated {
		t.Errorf("expected note.updated event for orphaning")
	}
	if !sawTaskDeleted {
		t.Errorf("expected task.deleted event")
	}
}

func TestDeleteTask_TransactionRollback_NoOrphanEvents(t *testing.T) {
	s := newTestStore(t)
	external, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "external"})
	parent, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "parent"})
	child, _ := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "child"})
	s.SetParent(ctx(), child.ID, &parent.ID)
	s.AddNote(ctx(), &child.ID, "n")
	// child blocks an external task -> recursive delete must abort.
	if _, err := s.AddBlockers(ctx(), external.ID, []uint{child.ID}); err != nil {
		t.Fatalf("setup blockers: %v", err)
	}

	obs := &recordingObserver{}
	s.AddObserver(obs)

	err := s.DeleteTask(ctx(), parent.ID, store.DeleteTaskOptions{Recursive: true})
	if err == nil {
		t.Fatal("expected BlockingExternalError")
	}
	for _, e := range obs.events {
		if e.Type == "note.updated" || e.Type == "note.deleted" || e.Type == "task.deleted" {
			t.Errorf("rollback should not emit %s; got %+v", e.Type, e)
		}
	}
	all, _ := s.ListNotes(ctx(), store.ListNotesOptions{IncludeArchived: true})
	if len(all) != 1 || all[0].TaskID == nil || *all[0].TaskID != child.ID {
		t.Errorf("note should still belong to child after rollback, got %+v", all)
	}
}

func TestUpdateNote_ExplicitArchivedFalsePersists(t *testing.T) {
	s := newTestStore(t)
	note, _ := s.AddNote(ctx(), nil, "n")
	// Archive then explicitly unarchive via UpdateNote (not ArchiveNote) to
	// verify the *bool option detects "false provided" vs "field omitted".
	if err := s.ArchiveNote(ctx(), note.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	f := false
	updated, err := s.UpdateNote(ctx(), note.ID, store.UpdateNoteOptions{Archived: &f})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Archived {
		t.Errorf("explicit archived=false did not persist: %+v", updated)
	}
}
