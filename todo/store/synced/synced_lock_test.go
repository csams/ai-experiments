package synced_test

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
	"github.com/csams/todo/store/synced"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// concurrencyEmbedder is a mockEmbedder variant that tracks in-flight
// EmbedBatch / Embed calls. Tests assert maxInFlight to confirm the
// PR-7 keyed lock serializes / does not serialize work as expected.
type concurrencyEmbedder struct {
	mu          sync.Mutex
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
	dwell       time.Duration // how long each call holds the in-flight counter
}

func (e *concurrencyEmbedder) recordEntry() {
	n := e.inFlight.Add(1)
	for {
		cur := e.maxInFlight.Load()
		if n <= cur || e.maxInFlight.CompareAndSwap(cur, n) {
			break
		}
	}
	time.Sleep(e.dwell)
}

func (e *concurrencyEmbedder) recordExit() {
	e.inFlight.Add(-1)
}

func (e *concurrencyEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.recordEntry()
	defer e.recordExit()
	return hashVec(text), nil
}

func (e *concurrencyEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.recordEntry()
	defer e.recordExit()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = hashVec(t)
	}
	return out, nil
}

func (e *concurrencyEmbedder) Dimensions() int     { return 4 }
func (e *concurrencyEmbedder) ModelName() string { return "concurrency/test" }

// newConcurrencyTestSetup is like newTestSetup but injects a
// concurrencyEmbedder with the given per-call dwell, and returns the
// embedder and mock vector store so tests can read their counters / state.
func newConcurrencyTestSetup(t *testing.T, dwell time.Duration) (store.Store, *concurrencyEmbedder, *mockVectorStore, *synced.VectorSyncer) {
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
		t.Fatalf("enable foreign keys: %v", err)
	}
	gs, err := gormstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	gs.SetSyncEmit(true)

	emb := &concurrencyEmbedder{dwell: dwell}
	vs := newMockVectorStore()
	syncer := synced.New(vs, emb, gs, slog.Default())
	gs.AddObserver(syncer)
	t.Cleanup(func() { _ = gs.Close(context.Background()) })
	return gs, emb, vs, syncer
}

// TestSyncerLock_SerializesSameTask — N concurrent OnEvent calls targeting
// the same task must run their EmbedBatch one at a time. We assert
// maxInFlight == 1 across the run.
func TestSyncerLock_SerializesSameTask(t *testing.T) {
	s, emb, _, syncer := newConcurrencyTestSetup(t, 10*time.Millisecond)
	task, err := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T", Description: "d"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Reset the counter — CreateTask emitted its own task.created event
	// which fired the syncer synchronously (SetSyncEmit=true) and bumped
	// the embedder. Clear so the assertion below only sees the test work.
	emb.maxInFlight.Store(0)

	const N = 8
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncer.OnEvent(context.Background(), store.StoreEvent{
				Type:    "task.updated",
				TaskIDs: []uint{task.ID},
			})
		}()
	}
	wg.Wait()

	if got := emb.maxInFlight.Load(); got != 1 {
		t.Errorf("maxInFlight = %d, want 1 (same-task work must serialize)", got)
	}
}

// TestSyncerLock_AllowsParallelDifferentTasks — concurrent OnEvent calls
// for *distinct* tasks should be able to run in parallel. We assert
// maxInFlight >= 2 (and aim for ~N) to confirm the lock isn't an
// over-serializing global mutex.
func TestSyncerLock_AllowsParallelDifferentTasks(t *testing.T) {
	s, emb, _, syncer := newConcurrencyTestSetup(t, 50*time.Millisecond)
	// Create N distinct tasks.
	const N = 4
	ids := make([]uint, N)
	for i := 0; i < N; i++ {
		task, err := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		ids[i] = task.ID
	}
	emb.maxInFlight.Store(0)

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		tid := id
		go func() {
			defer wg.Done()
			syncer.OnEvent(context.Background(), store.StoreEvent{
				Type:    "task.updated",
				TaskIDs: []uint{tid},
			})
		}()
	}
	wg.Wait()

	got := emb.maxInFlight.Load()
	if got < 2 {
		t.Errorf("maxInFlight = %d, want >= 2 (distinct-task work should parallelize)", got)
	}
}

// TestSyncerLock_NoteEntityKeyIsolated — task locks (key "task:<id>") and
// note locks (key "note:<id>") share the same numeric ID space but the
// string-prefixed keys live in disjoint regions of the entityLocks map.
// Concurrent OnEvent on a task and a note must therefore parallelize.
func TestSyncerLock_NoteEntityKeyIsolated(t *testing.T) {
	s, emb, _, syncer := newConcurrencyTestSetup(t, 50*time.Millisecond)
	task, err := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	note, err := s.AddNote(context.Background(), &task.ID, "hello")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	emb.maxInFlight.Store(0)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		syncer.OnEvent(context.Background(), store.StoreEvent{
			Type:    "task.updated",
			TaskIDs: []uint{task.ID},
		})
	}()
	go func() {
		defer wg.Done()
		syncer.OnEvent(context.Background(), store.StoreEvent{
			Type:    "note.updated",
			NoteIDs: []uint{note.ID},
		})
	}()
	wg.Wait()

	if got := emb.maxInFlight.Load(); got < 2 {
		t.Errorf("maxInFlight = %d, want >= 2 (task and note locks must not contend)", got)
	}
}

// TestSyncerLock_NoRaceStress — many concurrent OnEvent calls across
// random tasks. Useful primarily under -race; asserts that the final
// state is non-empty (we wrote *something*) without making timing-
// dependent claims.
func TestSyncerLock_NoRaceStress(t *testing.T) {
	s, emb, _, syncer := newConcurrencyTestSetup(t, 0) // no dwell, just thrash
	const tasks = 8
	const fires = 50

	ids := make([]uint, tasks)
	for i := 0; i < tasks; i++ {
		task, err := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		ids[i] = task.ID
	}
	emb.maxInFlight.Store(0)

	var wg sync.WaitGroup
	for i := 0; i < fires; i++ {
		wg.Add(1)
		tid := ids[i%tasks]
		go func() {
			defer wg.Done()
			syncer.OnEvent(context.Background(), store.StoreEvent{
				Type:    "task.updated",
				TaskIDs: []uint{tid},
			})
		}()
	}
	wg.Wait()

	// No crashes / data races at this point is the main assertion.
	// Final maxInFlight should be at least 1 (we did some work).
	if got := emb.maxInFlight.Load(); got < 1 {
		t.Errorf("maxInFlight = %d, want >= 1 (some work should have run)", got)
	}
}

// TestSyncerLock_DeleteWaitsForInFlightTaskEmbed — regression test for the
// delete-branch lock added alongside PR-7's embed-branch lock. Without it,
// a slow task.updated embedding for task N could complete its Upsert
// AFTER a concurrent task.deleted ran its DeleteTaskDocs, resurrecting
// the deleted task's chunks. With the lock, task.deleted waits for the
// in-flight embed to finish, then deletes, leaving zero chunks.
func TestSyncerLock_DeleteWaitsForInFlightTaskEmbed(t *testing.T) {
	s, emb, vs, syncer := newConcurrencyTestSetup(t, 40*time.Millisecond)
	task, err := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T", Description: "d"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	emb.maxInFlight.Store(0)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		syncer.OnEvent(context.Background(), store.StoreEvent{
			Type:    "task.updated",
			TaskIDs: []uint{task.ID},
		})
	}()
	// Let the embed goroutine grab the lock and enter the dwelling
	// embedder.
	time.Sleep(5 * time.Millisecond)
	go func() {
		defer wg.Done()
		syncer.OnEvent(context.Background(), store.StoreEvent{
			Type:    "task.deleted",
			TaskIDs: []uint{task.ID},
		})
	}()
	wg.Wait()

	// After both calls complete, the delete must have run AFTER the
	// upsert, so the vector store has no chunks for this task.
	if got := vs.findTaskChunks(task.ID); len(got) != 0 {
		t.Errorf("found %d chunks for deleted task; want 0 (embed must lose to delete)", len(got))
	}
}

// TestSyncerLock_DeleteWaitsForInFlightNoteEmbed — note.deleted mirror of
// the task delete-race regression test.
func TestSyncerLock_DeleteWaitsForInFlightNoteEmbed(t *testing.T) {
	s, emb, vs, syncer := newConcurrencyTestSetup(t, 40*time.Millisecond)
	task, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T"})
	note, err := s.AddNote(context.Background(), &task.ID, "hello world")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	emb.maxInFlight.Store(0)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		syncer.OnEvent(context.Background(), store.StoreEvent{
			Type:    "note.updated",
			NoteIDs: []uint{note.ID},
		})
	}()
	time.Sleep(5 * time.Millisecond)
	go func() {
		defer wg.Done()
		syncer.OnEvent(context.Background(), store.StoreEvent{
			Type:    "note.deleted",
			NoteIDs: []uint{note.ID},
		})
	}()
	wg.Wait()

	if got := vs.findNoteChunks(note.ID); len(got) != 0 {
		t.Errorf("found %d chunks for deleted note; want 0 (embed must lose to delete)", len(got))
	}
}

// TestSyncerLock_BulkEventLocksAllIDs — a single OnEvent with multiple
// TaskIDs must hold all per-task locks for the duration of the call,
// blocking a concurrent same-task event until the bulk call completes.
// We check this indirectly: with a bulk call dwelling in the embedder
// and a per-task event firing for one of the bulk's IDs, max-in-flight
// must remain 1 (the second call waits behind the bulk's lock on that
// shared ID).
func TestSyncerLock_BulkEventLocksAllIDs(t *testing.T) {
	s, emb, _, syncer := newConcurrencyTestSetup(t, 40*time.Millisecond)
	a, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "B"})
	emb.maxInFlight.Store(0)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		syncer.OnEvent(context.Background(), store.StoreEvent{
			Type:    "task.bulk_state_changed",
			TaskIDs: []uint{a.ID, b.ID},
		})
	}()
	// Slight delay so the bulk goroutine acquires the locks first.
	time.Sleep(5 * time.Millisecond)
	go func() {
		defer wg.Done()
		syncer.OnEvent(context.Background(), store.StoreEvent{
			Type:    "task.updated",
			TaskIDs: []uint{a.ID},
		})
	}()
	wg.Wait()

	if got := emb.maxInFlight.Load(); got != 1 {
		t.Errorf("maxInFlight = %d, want 1 (bulk's lock on A blocks the concurrent task.updated for A)", got)
	}
}
