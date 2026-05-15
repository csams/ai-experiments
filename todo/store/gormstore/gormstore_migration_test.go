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

// TestMigration_SQLite_DropsNotNullPreservesRows builds a SQLite DB with the
// pre-change schema (notes.task_id NOT NULL) and a row, opens it via gormstore.New,
// and asserts the migration drops NOT NULL and preserves the row data.
func TestMigration_SQLite_DropsNotNullPreservesRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("pragma: %v", err)
	}

	// Build the pre-migration notes table with NOT NULL on task_id and the
	// historical column set (no archived/updated_at). Also seed a minimal tasks
	// table with the parent row so AutoMigrate's later notes-table rebuild (to
	// add the FK constraint) doesn't fail FK validation on the legacy row.
	// We let AutoMigrate add columns to `tasks` afterward.
	stmts := []string{
		`CREATE TABLE tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 0,
			state TEXT NOT NULL DEFAULT 'New',
			archived INTEGER NOT NULL DEFAULT 0,
			vector_dirty INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`INSERT INTO tasks (id, title) VALUES (42, 'legacy parent')`,
		`CREATE TABLE notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			text TEXT NOT NULL,
			vector_dirty INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME
		)`,
		`CREATE INDEX idx_notes_task_id ON notes(task_id)`,
		`INSERT INTO notes (id, task_id, text, vector_dirty, created_at)
			VALUES (1, 42, 'legacy note', 0, '2026-01-01 00:00:00')`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("setup %q: %v", s, err)
		}
	}

	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("New (migration): %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	// Verify NOT NULL is dropped: inserting a standalone note should now succeed.
	if _, err := s.AddNote(context.Background(), nil, "standalone after migration"); err != nil {
		t.Fatalf("standalone insert after migration: %v", err)
	}

	// Verify legacy data preserved with intact task_id.
	all, err := s.ListNotes(context.Background(), store.ListNotesOptions{IncludeArchived: true})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 notes (legacy + new), got %d", len(all))
	}
	var legacy, standalone bool
	for _, n := range all {
		switch n.Text {
		case "legacy note":
			legacy = true
			if n.TaskID == nil || *n.TaskID != 42 {
				t.Errorf("legacy note task_id = %v, want 42", n.TaskID)
			}
		case "standalone after migration":
			standalone = true
			if n.TaskID != nil {
				t.Errorf("standalone note task_id = %v, want nil", n.TaskID)
			}
		}
	}
	if !legacy || !standalone {
		t.Errorf("missing notes after migration: legacy=%v standalone=%v", legacy, standalone)
	}

	// Confirm AutoMigrate added the new columns.
	type sqliteCol struct {
		CID     int     `gorm:"column:cid"`
		Name    string  `gorm:"column:name"`
		Type    string  `gorm:"column:type"`
		NotNull int     `gorm:"column:notnull"`
		Dflt    *string `gorm:"column:dflt_value"`
		PK      int     `gorm:"column:pk"`
	}
	var cols []sqliteCol
	if err := db.Raw("PRAGMA table_info(notes)").Scan(&cols).Error; err != nil {
		t.Fatalf("table_info: %v", err)
	}
	have := map[string]sqliteCol{}
	for _, c := range cols {
		have[c.Name] = c
	}
	if c, ok := have["task_id"]; !ok || c.NotNull != 0 {
		t.Errorf("task_id should be nullable, got %+v", c)
	}
	if _, ok := have["archived"]; !ok {
		t.Errorf("archived column missing after migration")
	}
	if _, ok := have["updated_at"]; !ok {
		t.Errorf("updated_at column missing after migration")
	}
}

// TestMigration_SQLite_FreshDB_NoOp asserts the migration is a clean no-op when
// the notes table doesn't exist yet.
func TestMigration_SQLite_FreshDB_NoOp(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("New (fresh): %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	if _, err := s.AddNote(context.Background(), nil, "fresh standalone"); err != nil {
		t.Fatalf("standalone insert on fresh DB: %v", err)
	}
}
