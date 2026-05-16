package gormstore_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newAsyncTestStore builds a store with async observer emission enabled —
// the production code path. The default newTestStore uses SetSyncEmit(true)
// for deterministic event-shape assertions; PR-6's drain tests need to
// exercise the goroutine-spawning emit path directly.
func newAsyncTestStore(t *testing.T) *gormstore.GormStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	// Intentionally do NOT call SetSyncEmit(true).
	t.Cleanup(func() {
		_ = s.Close(context.Background())
	})
	return s
}

// blockingObserver fires Done on its release channel before returning from
// OnEvent. Tests use it to hold an observer goroutine in-flight while
// asserting Drain/Close behavior.
type blockingObserver struct {
	release  <-chan struct{}
	received atomic.Int32 // events seen
	finished atomic.Int32 // OnEvent calls that returned
}

func (b *blockingObserver) OnEvent(_ context.Context, _ store.StoreEvent) {
	b.received.Add(1)
	<-b.release
	b.finished.Add(1)
}

func TestDrain_WaitsForInFlightObservers(t *testing.T) {
	s := newAsyncTestStore(t)
	release := make(chan struct{})
	obs := &blockingObserver{release: release}
	s.AddObserver(obs)

	// Trigger a mutation → spawns an observer goroutine that blocks.
	if _, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Wait until the observer goroutine has actually started.
	deadline := time.Now().Add(2 * time.Second)
	for obs.received.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("observer goroutine never ran")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Drain with a generous ctx must not return until the observer's
	// OnEvent has actually returned.
	drainDone := make(chan error, 1)
	go func() {
		drainDone <- s.Drain(context.Background())
	}()

	select {
	case err := <-drainDone:
		t.Fatalf("Drain returned before observer released: err=%v", err)
	case <-time.After(50 * time.Millisecond):
		// expected — drain should still be blocked
	}

	close(release)

	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("Drain returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Drain did not return after release")
	}

	if got := obs.finished.Load(); got != 1 {
		t.Errorf("observer finished count = %d, want 1", got)
	}
}

func TestDrain_RespectsContextDeadline(t *testing.T) {
	s := newAsyncTestStore(t)
	release := make(chan struct{})
	defer close(release)
	obs := &blockingObserver{release: release}
	s.AddObserver(obs)

	if _, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Wait for the observer to enter its blocking region.
	deadline := time.Now().Add(2 * time.Second)
	for obs.received.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("observer goroutine never ran")
		}
		time.Sleep(2 * time.Millisecond)
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := s.Drain(ctxTimeout)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Drain blocked %s, want ~30ms", elapsed)
	}
}

func TestClose_DrainsBeforeClosingDB(t *testing.T) {
	// Use a custom DB so we can manage shutdown ourselves; the t.Cleanup
	// store-close in newAsyncTestStore would race with what we're testing.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	release := make(chan struct{})
	obs := &blockingObserver{release: release}
	s.AddObserver(obs)

	if _, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Wait for observer entry.
	deadline := time.Now().Add(2 * time.Second)
	for obs.received.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("observer goroutine never ran")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Kick off Close; it must block on Drain until the observer returns.
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- s.Close(context.Background())
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before observer released: err=%v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after observer release")
	}
}

func TestClose_BoundedByCtxDeadline(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	release := make(chan struct{})
	defer close(release) // never released within the test
	obs := &blockingObserver{release: release}
	s.AddObserver(obs)

	if _, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for obs.received.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("observer goroutine never ran")
		}
		time.Sleep(2 * time.Millisecond)
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = s.Close(ctxTimeout)
	elapsed := time.Since(start)

	// Close logs a warning but still closes the DB and returns nil on the
	// drain-timeout path. The important assertion is that it does not
	// block past the ctx deadline.
	if err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Close blocked %s past its deadline (~30ms)", elapsed)
	}
}

func TestClose_IsIdempotent(t *testing.T) {
	// Verify Close can be called twice without erroring (e.g. a defer plus
	// an explicit shutdown-handler call). The second call drains the
	// already-empty wg and skips the underlying DB close.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestEmit_SkipsAsyncSpawnAfterClose(t *testing.T) {
	// Once Close has set closed=true, emit short-circuits and does not
	// spawn new observer goroutines. This is a safety guard against
	// callers that try to mutate the store after it's been closed (a
	// caller bug, but the store shouldn't compound the damage by racing
	// goroutines against a torn-down DB).
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// Create the task BEFORE registering the observer so it doesn't
	// already track this event.
	task, err := s.CreateTask(ctx(), store.CreateTaskOptions{Title: "T"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	release := make(chan struct{})
	defer close(release)
	obs := &blockingObserver{release: release}
	s.AddObserver(obs)

	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Try to mutate after close: emit must skip the async path. The DB
	// is closed so the mutation itself will fail; what we're asserting
	// is that no observer goroutine was spawned. Touch the same task
	// with UpdateTask — the failure is fine, we just care no observer
	// was queued.
	newTitle := "updated"
	_, _ = s.UpdateTask(ctx(), task.ID, store.UpdateTaskOptions{Title: &newTitle})

	// Drain should return immediately — there should be nothing in
	// flight because emit short-circuited.
	drainCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.Drain(drainCtx); err != nil {
		t.Fatalf("Drain after Close should return immediately, got: %v", err)
	}
	if obs.received.Load() != 0 {
		t.Errorf("observer received %d events after Close, want 0", obs.received.Load())
	}
}
