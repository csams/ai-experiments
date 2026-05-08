package gormstore_test

import (
	"context"
	"testing"

	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// recordingObserver captures every StoreEvent it receives. Tests use it to
// assert that mutations emit (or do not emit) events.
type recordingObserver struct {
	events []store.StoreEvent
}

func (r *recordingObserver) OnEvent(_ context.Context, e store.StoreEvent) {
	r.events = append(r.events, e)
}

// newTestStore creates a fresh in-memory SQLite GormStore for testing.
func newTestStore(t *testing.T) *gormstore.GormStore {
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

	// Enable foreign keys for SQLite
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	s.SetSyncEmit(true)

	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

func ctx() context.Context {
	return context.Background()
}
