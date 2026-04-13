package pgvector

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/csams/todo/vectorstore"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		fmt.Println("skipping pgvector tests: TEST_POSTGRES_DSN not set")
		os.Exit(0)
	}

	var err error
	testDB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func cleanTables(t *testing.T) {
	t.Helper()
	testDB.Exec("DROP TABLE IF EXISTS vector_documents")
	testDB.Exec("DROP TABLE IF EXISTS vector_metadata")
}

func newTestStore(t *testing.T, dims int) *Store {
	t.Helper()
	cleanTables(t)
	t.Cleanup(func() { cleanTables(t) })

	s, err := New(testDB, "test/model", dims)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNew_CreatesExtensionAndTables(t *testing.T) {
	s := newTestStore(t, 3)

	// Verify tables exist by querying them.
	var count int64
	if err := s.db.Raw("SELECT COUNT(*) FROM vector_documents").Scan(&count).Error; err != nil {
		t.Fatalf("vector_documents table missing: %v", err)
	}
	if err := s.db.Raw("SELECT COUNT(*) FROM vector_metadata").Scan(&count).Error; err != nil {
		t.Fatalf("vector_metadata table missing: %v", err)
	}
}

func TestUpsert_InsertAndUpdate(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	doc := vectorstore.Document{
		ID:     "task:1",
		Text:   "original text",
		Vector: []float32{1, 0, 0},
		Metadata: map[string]any{
			"type":    "task",
			"task_id": 1,
		},
	}

	// Insert
	if err := s.Upsert(ctx, []vectorstore.Document{doc}); err != nil {
		t.Fatalf("Upsert insert: %v", err)
	}

	// Verify
	results, err := s.Search(ctx, []float32{1, 0, 0}, 10, vectorstore.SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Text != "original text" {
		t.Fatalf("expected 1 result with original text, got %v", results)
	}

	// Update
	doc.Text = "updated text"
	doc.Vector = []float32{0, 1, 0}
	if err := s.Upsert(ctx, []vectorstore.Document{doc}); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}

	results, err = s.Search(ctx, []float32{0, 1, 0}, 10, vectorstore.SearchFilter{})
	if err != nil {
		t.Fatalf("Search after update: %v", err)
	}
	if len(results) != 1 || results[0].Text != "updated text" {
		t.Fatalf("expected updated text, got %v", results)
	}
}

func TestUpsert_EmptySlice(t *testing.T) {
	s := newTestStore(t, 3)
	if err := s.Upsert(context.Background(), nil); err != nil {
		t.Fatalf("Upsert empty: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []vectorstore.Document{
		{ID: "task:1", Text: "one", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task"}},
		{ID: "task:2", Text: "two", Vector: []float32{0, 1, 0}, Metadata: map[string]any{"type": "task"}},
	}
	if err := s.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := s.Delete(ctx, []string{"task:1"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, 10, vectorstore.SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "task:2" {
		t.Fatalf("expected task:2 only, got %v", results)
	}
}

func TestDelete_EmptySlice(t *testing.T) {
	s := newTestStore(t, 3)
	if err := s.Delete(context.Background(), nil); err != nil {
		t.Fatalf("Delete empty: %v", err)
	}
}

func TestSearch_CosineSimilarity(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []vectorstore.Document{
		{ID: "task:1", Text: "close match", Vector: []float32{0.9, 0.1, 0}, Metadata: map[string]any{"type": "task"}},
		{ID: "task:2", Text: "far match", Vector: []float32{0, 0, 1}, Metadata: map[string]any{"type": "task"}},
	}
	if err := s.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, 10, vectorstore.SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "task:1" {
		t.Errorf("expected task:1 first (closest), got %s", results[0].ID)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("first result score (%f) should be > second (%f)", results[0].Score, results[1].Score)
	}
}

func TestSearch_TypeFilter(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []vectorstore.Document{
		{ID: "task:1", Text: "a task", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task"}},
		{ID: "note:1", Text: "a note", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "note"}},
	}
	if err := s.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	noteType := "note"
	results, err := s.Search(ctx, []float32{1, 0, 0}, 10, vectorstore.SearchFilter{Type: &noteType})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "note:1" {
		t.Fatalf("expected only note:1, got %v", results)
	}
}

func TestSearch_ArchivedFilter(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []vectorstore.Document{
		{ID: "task:1", Text: "active", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task", "archived": false}},
		{ID: "task:2", Text: "archived", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task", "archived": true}},
	}
	if err := s.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	notArchived := false
	results, err := s.Search(ctx, []float32{1, 0, 0}, 10, vectorstore.SearchFilter{Archived: &notArchived})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "task:1" {
		t.Fatalf("expected only task:1, got %v", results)
	}
}

func TestSearch_ExcludeIDs(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []vectorstore.Document{
		{ID: "task:1", Text: "one", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task"}},
		{ID: "task:2", Text: "two", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task"}},
	}
	if err := s.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, 10, vectorstore.SearchFilter{
		ExcludeIDs: []string{"task:1"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "task:2" {
		t.Fatalf("expected only task:2, got %v", results)
	}
}

func TestSearch_CombinedFilters(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []vectorstore.Document{
		{ID: "task:1", Text: "active task", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task", "archived": false}},
		{ID: "note:1", Text: "active note", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "note", "archived": false, "task_id": 1}},
		{ID: "note:2", Text: "archived note", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "note", "archived": true, "task_id": 1}},
	}
	if err := s.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	noteType := "note"
	notArchived := false
	results, err := s.Search(ctx, []float32{1, 0, 0}, 10, vectorstore.SearchFilter{
		Type:     &noteType,
		Archived: &notArchived,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "note:1" {
		t.Fatalf("expected only note:1, got %v", results)
	}
}

func TestSearch_Limit(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	docs := []vectorstore.Document{
		{ID: "task:1", Text: "one", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task"}},
		{ID: "task:2", Text: "two", Vector: []float32{0.9, 0.1, 0}, Metadata: map[string]any{"type": "task"}},
		{ID: "task:3", Text: "three", Vector: []float32{0.8, 0.2, 0}, Metadata: map[string]any{"type": "task"}},
	}
	if err := s.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, 2, vectorstore.SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestCollectionInfo(t *testing.T) {
	s := newTestStore(t, 3)

	model, dims, err := s.CollectionInfo(context.Background())
	if err != nil {
		t.Fatalf("CollectionInfo: %v", err)
	}
	if model != "test/model" {
		t.Errorf("model = %q, want %q", model, "test/model")
	}
	if dims != 3 {
		t.Errorf("dims = %d, want 3", dims)
	}
}

func TestReset_SameDimension(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	// Insert a document
	if err := s.Upsert(ctx, []vectorstore.Document{
		{ID: "task:1", Text: "test", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task"}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Reset with same dims — should truncate
	if err := s.Reset(ctx, "test/model-v2", 3); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Table should be empty
	results, err := s.Search(ctx, []float32{1, 0, 0}, 10, vectorstore.SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results after reset, got %d", len(results))
	}

	// Metadata should be updated
	model, dims, err := s.CollectionInfo(ctx)
	if err != nil {
		t.Fatalf("CollectionInfo: %v", err)
	}
	if model != "test/model-v2" || dims != 3 {
		t.Errorf("metadata = (%q, %d), want (test/model-v2, 3)", model, dims)
	}
}

func TestReset_DifferentDimension(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	// Insert with 3 dims
	if err := s.Upsert(ctx, []vectorstore.Document{
		{ID: "task:1", Text: "test", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task"}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Reset with different dims — should drop and recreate
	if err := s.Reset(ctx, "test/model-large", 5); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Should accept 5-dim vectors now
	if err := s.Upsert(ctx, []vectorstore.Document{
		{ID: "task:2", Text: "five dims", Vector: []float32{1, 0, 0, 0, 0}, Metadata: map[string]any{"type": "task"}},
	}); err != nil {
		t.Fatalf("Upsert with new dims: %v", err)
	}

	model, dims, err := s.CollectionInfo(ctx)
	if err != nil {
		t.Fatalf("CollectionInfo: %v", err)
	}
	if model != "test/model-large" || dims != 5 {
		t.Errorf("metadata = (%q, %d), want (test/model-large, 5)", model, dims)
	}
}

func TestClose_IsNoop(t *testing.T) {
	s := newTestStore(t, 3)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// DB should still work after Close
	_, _, err := s.CollectionInfo(context.Background())
	if err != nil {
		t.Fatalf("CollectionInfo after Close: %v", err)
	}
}

func TestSearch_MetadataRoundTrip(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	doc := vectorstore.Document{
		ID:     "task:42",
		Text:   "test task",
		Vector: []float32{1, 0, 0},
		Metadata: map[string]any{
			"type":     "task",
			"task_id":  42,
			"state":    "Progressing",
			"priority": 1,
			"archived": false,
		},
	}
	if err := s.Upsert(ctx, []vectorstore.Document{doc}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, 1, vectorstore.SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	meta := results[0].Metadata
	if meta["type"] != "task" {
		t.Errorf("type = %v, want task", meta["type"])
	}
	// Integer metadata may come back as int or int64 depending on driver — check both.
	taskID, ok := toInt(meta["task_id"])
	if !ok || taskID != 42 {
		t.Errorf("task_id = %v, want 42", meta["task_id"])
	}
	if meta["state"] != "Progressing" {
		t.Errorf("state = %v, want Progressing", meta["state"])
	}
	priority, ok := toInt(meta["priority"])
	if !ok || priority != 1 {
		t.Errorf("priority = %v, want 1", meta["priority"])
	}
	if meta["archived"] != false {
		t.Errorf("archived = %v, want false", meta["archived"])
	}
}

func TestSearch_ScoreRange(t *testing.T) {
	s := newTestStore(t, 3)
	ctx := context.Background()

	// Insert a doc identical to the query vector.
	if err := s.Upsert(ctx, []vectorstore.Document{
		{ID: "task:1", Text: "exact", Vector: []float32{1, 0, 0}, Metadata: map[string]any{"type": "task"}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, 1, vectorstore.SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Exact match should have cosine similarity ~1.0.
	if math.Abs(float64(results[0].Score)-1.0) > 0.001 {
		t.Errorf("exact match score = %f, want ~1.0", results[0].Score)
	}
}

// toInt converts various numeric types to int for test assertions.
func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int32:
		return int(val), true
	case int64:
		return int(val), true
	case float64:
		return int(val), true
	default:
		return 0, false
	}
}
