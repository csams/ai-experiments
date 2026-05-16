package synced_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/csams/todo/embed"
	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
	"github.com/csams/todo/store/synced"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// failingEmbedder fails the first N EmbedBatch calls, then succeeds.
// Tests use it to drive a "first attempt fails → mark dirty → reconciler
// retries → succeeds" cycle.
type failingEmbedder struct {
	mu         sync.Mutex
	failures   int            // remaining failures to inject
	calls      int            // total calls observed
	fallback   embed.Embedder // delegate for successful calls
	failureErr error
}

func (e *failingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	e.calls++
	if e.failures > 0 {
		e.failures--
		e.mu.Unlock()
		return nil, e.failureErr
	}
	e.mu.Unlock()
	return e.fallback.Embed(ctx, text)
}

func (e *failingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.calls++
	if e.failures > 0 {
		e.failures--
		e.mu.Unlock()
		return nil, e.failureErr
	}
	e.mu.Unlock()
	return e.fallback.EmbedBatch(ctx, texts)
}

func (e *failingEmbedder) Dimensions() int     { return e.fallback.Dimensions() }
func (e *failingEmbedder) ModelName() string { return e.fallback.ModelName() }

// newReconcilerTestSetup wires a real GormStore (sync emit) to a syncer
// backed by a failingEmbedder so the test can deterministically drive
// the failure → mark-dirty → reconciler-retry → clear cycle.
func newReconcilerTestSetup(t *testing.T, failures int) (store.Store, *failingEmbedder, *mockVectorStore, *synced.VectorSyncer) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	gs, err := gormstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	gs.SetSyncEmit(true)

	good := &mockEmbedder{}
	emb := &failingEmbedder{
		failures:   failures,
		fallback:   good,
		failureErr: errors.New("simulated embedder failure"),
	}
	vs := newMockVectorStore()
	syncer := synced.New(vs, emb, gs, slog.Default())
	gs.AddObserver(syncer)
	t.Cleanup(func() { _ = gs.Close(context.Background()) })
	return gs, emb, vs, syncer
}

// TestReconciler_MarkOnFailure asserts that a sync failure flags the
// affected task as vector_dirty in the DB.
func TestReconciler_MarkOnFailure(t *testing.T) {
	s, _, _, _ := newReconcilerTestSetup(t, 100) // fail every embed
	task, err := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The CreateTask emitted task.created, the syncer's OnEvent failed
	// the embed, and markDirty flagged the row.
	dirtyTasks, _, err := s.ListVectorDirty(context.Background(), 100)
	if err != nil {
		t.Fatalf("list dirty: %v", err)
	}
	if len(dirtyTasks) != 1 || dirtyTasks[0] != task.ID {
		t.Errorf("dirty tasks = %v, want [%d]", dirtyTasks, task.ID)
	}
}

// TestReconciler_DeleteEventDoesNotMark — deletes can't be "retried"
// (the entity is gone). The mark path must skip them so the reconciler
// doesn't tight-loop trying to embed a row that no longer exists.
func TestReconciler_DeleteEventDoesNotMark(t *testing.T) {
	s, emb, _, syncer := newReconcilerTestSetup(t, 100)
	task, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T"})
	// Clear any dirty flag from the failed CreateTask embed so this
	// test isolates the delete-event path.
	_ = s.ClearVectorDirty(context.Background(), []uint{task.ID}, nil)
	_ = emb // not used directly; failure count keeps applying

	// Synthesize a task.deleted event — the syncer's DeleteTaskDocs
	// call wouldn't normally fail on a mock vector store, but we
	// invoke the marker path directly to pin the behavior.
	// Easier: drive the failure via OnEvent with a delete event type.
	syncer.OnEvent(context.Background(), store.StoreEvent{
		Type:    "task.deleted",
		TaskIDs: []uint{task.ID},
	})

	// task.deleted does NOT call the embed path; DeleteTaskDocs on the
	// mock store always succeeds, so OnEvent's err is nil and markDirty
	// is never called. But even if it were called, the delete branch
	// short-circuits without marking.
	dirtyTasks, _, _ := s.ListVectorDirty(context.Background(), 100)
	if len(dirtyTasks) != 0 {
		t.Errorf("dirty tasks after delete = %v, want empty (deletes shouldn't mark)", dirtyTasks)
	}
}

// TestReconciler_ClearsAfterSuccessfulEmbed — once the failing embedder
// stops failing, the next OnEvent for the task should clear the dirty
// flag inside embedTasks.
func TestReconciler_ClearsAfterSuccessfulEmbed(t *testing.T) {
	s, emb, vs, syncer := newReconcilerTestSetup(t, 1) // fail only the first embed
	task, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T", Description: "d"})

	// Initial dirty.
	dirtyTasks, _, _ := s.ListVectorDirty(context.Background(), 100)
	if len(dirtyTasks) != 1 {
		t.Fatalf("setup: expected 1 dirty task, got %v", dirtyTasks)
	}

	// Re-fire the event manually — second EmbedBatch succeeds.
	syncer.OnEvent(context.Background(), store.StoreEvent{
		Type:    "task.updated",
		TaskIDs: []uint{task.ID},
	})

	if emb.calls < 2 {
		t.Errorf("expected at least 2 embedder calls, got %d", emb.calls)
	}
	dirtyTasks, _, _ = s.ListVectorDirty(context.Background(), 100)
	if len(dirtyTasks) != 0 {
		t.Errorf("dirty tasks after success = %v, want empty", dirtyTasks)
	}
	// Chunks should be present in the vector store.
	if got := vs.findTaskChunks(task.ID); len(got) == 0 {
		t.Errorf("no chunks in vector store after successful re-embed")
	}
}

// TestReconciler_RunsTickAndClears spins up the reconciler goroutine
// against a previously-failing embedder, asserts it drains the dirty
// set, and then stops cleanly.
func TestReconciler_RunsTickAndClears(t *testing.T) {
	s, _, vs, syncer := newReconcilerTestSetup(t, 1)
	task, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T", Description: "d"})

	if dirty, _, _ := s.ListVectorDirty(context.Background(), 100); len(dirty) != 1 {
		t.Fatalf("setup: expected dirty after failed create, got %v", dirty)
	}

	syncer.StartReconciler(context.Background(), 10*time.Millisecond, 100)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		syncer.StopReconciler(ctx)
	})

	// Wait up to 2s for the reconciler to drain the dirty row.
	deadline := time.Now().Add(2 * time.Second)
	for {
		dirty, _, _ := s.ListVectorDirty(context.Background(), 100)
		if len(dirty) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconciler did not clear dirty row in time; still dirty: %v", dirty)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := vs.findTaskChunks(task.ID); len(got) == 0 {
		t.Errorf("vector store empty after reconciler ran; expected chunks for task %d", task.ID)
	}
}

// TestReconciler_StopReconcilerIsIdempotent — calling Stop twice (or
// without a prior Start) must not panic or hang.
func TestReconciler_StopReconcilerIsIdempotent(t *testing.T) {
	_, _, _, syncer := newReconcilerTestSetup(t, 0)

	// No Start — Stop should no-op.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	syncer.StopReconciler(ctx)

	// Start, then Stop twice.
	syncer.StartReconciler(context.Background(), time.Second, 100)
	syncer.StopReconciler(ctx)
	syncer.StopReconciler(ctx)
}

// TestReconciler_StartIsIdempotent — calling Start while a reconciler is
// already running must be a no-op (not spawn a second goroutine).
func TestReconciler_StartIsIdempotent(t *testing.T) {
	_, _, _, syncer := newReconcilerTestSetup(t, 0)

	syncer.StartReconciler(context.Background(), 50*time.Millisecond, 100)
	syncer.StartReconciler(context.Background(), 50*time.Millisecond, 100) // second call: no-op
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		syncer.StopReconciler(ctx)
	})

	// Sleep briefly; we can't easily count goroutines here, but the
	// idempotence assertion is that Start + Stop pairs cleanly.
	time.Sleep(100 * time.Millisecond)
}

