package synced_test

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
	"github.com/csams/todo/store/synced"
	"github.com/csams/todo/vectorstore"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --- Mock Embedder ---

type mockEmbedder struct {
	calls []string // texts that were embedded
	mu    sync.Mutex
}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, text)
	return hashVec(text), nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		m.calls = append(m.calls, t)
		vecs[i] = hashVec(t)
	}
	return vecs, nil
}

func (m *mockEmbedder) Dimensions() int  { return 4 }
func (m *mockEmbedder) ModelName() string { return "mock/test" }

// hashVec creates a deterministic 4-dim vector from text for testing.
func hashVec(text string) []float32 {
	h := float32(0)
	for _, c := range text {
		h += float32(c)
	}
	return []float32{h, h * 0.1, h * 0.01, h * 0.001}
}

// --- Mock VectorStore ---

type mockVectorStore struct {
	docs map[string]vectorstore.Document
	mu   sync.Mutex
}

func newMockVectorStore() *mockVectorStore {
	return &mockVectorStore{docs: make(map[string]vectorstore.Document)}
}

func (m *mockVectorStore) Upsert(_ context.Context, docs []vectorstore.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range docs {
		m.docs[d.ID] = d
	}
	return nil
}

func (m *mockVectorStore) Delete(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		delete(m.docs, id)
	}
	return nil
}

func (m *mockVectorStore) Search(_ context.Context, query []float32, limit int, filter vectorstore.SearchFilter) ([]vectorstore.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	excludeSet := make(map[string]bool)
	for _, id := range filter.ExcludeIDs {
		excludeSet[id] = true
	}

	var results []vectorstore.SearchResult
	for _, d := range m.docs {
		if excludeSet[d.ID] {
			continue
		}
		if filter.Type != nil {
			if t, ok := d.Metadata["type"].(string); ok && t != *filter.Type {
				continue
			}
		}
		if filter.TaskID != nil {
			if tid, ok := d.Metadata["task_id"].(int); ok && uint(tid) != *filter.TaskID {
				continue
			}
		}
		if filter.Archived != nil {
			archived, ok := d.Metadata["archived"].(bool)
			if !ok || archived != *filter.Archived {
				continue
			}
		}

		score := cosineSim(query, d.Vector)
		results = append(results, vectorstore.SearchResult{
			Document: d,
			Score:    score,
		})
	}

	// Sort by score descending
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (m *mockVectorStore) CollectionInfo(_ context.Context) (string, int, error) {
	return "mock/test", 4, nil
}

func (m *mockVectorStore) Reset(_ context.Context, _ string, _ int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs = make(map[string]vectorstore.Document)
	return nil
}

func (m *mockVectorStore) Close() error { return nil }

func cosineSim(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

// --- Test helpers ---

func newTestSetup(t *testing.T) (store.Store, *mockEmbedder, *mockVectorStore, *synced.VectorSyncer) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	gs, err := gormstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	gs.SetSyncEmit(true)

	emb := &mockEmbedder{}
	vs := newMockVectorStore()
	log := slog.Default()

	syncer := synced.New(vs, emb, gs, log)
	gs.AddObserver(syncer)

	t.Cleanup(func() { gs.Close(context.Background()) })
	return gs, emb, vs, syncer
}

func bg() context.Context {
	return context.Background()
}

// --- Tests ---

func TestSync_CreateTaskEmbedsDocument(t *testing.T) {
	s, emb, vs, _ := newTestSetup(t)

	_, err := s.CreateTask(bg(), "Fix auth bug", "Token expiry issue", 1, nil, []string{"backend"})
	if err != nil {
		t.Fatal(err)
	}

	if len(emb.calls) == 0 {
		t.Fatal("expected embedder to be called on task creation")
	}

	// Check vector store has the document
	vs.mu.Lock()
	defer vs.mu.Unlock()
	doc, ok := vs.docs["task:1"]
	if !ok {
		t.Fatal("expected task:1 in vector store")
	}
	if doc.Metadata["type"] != "task" {
		t.Errorf("metadata type = %v, want 'task'", doc.Metadata["type"])
	}
	// Embedded text should include tags and state
	if !strings.Contains(doc.Text, "backend") {
		t.Errorf("embedded text should contain tag 'backend': %q", doc.Text)
	}
	if !strings.Contains(doc.Text, "New") {
		t.Errorf("embedded text should contain state 'New': %q", doc.Text)
	}
}

func TestSync_AddNoteEmbedsDocument(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask(bg(), "Task", "", 0, nil, nil)
	_, err := s.AddNote(bg(), 1, "investigation notes here")
	if err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()
	doc, ok := vs.docs["note:1"]
	if !ok {
		t.Fatal("expected note:1 in vector store")
	}
	if doc.Text != "investigation notes here" {
		t.Errorf("text = %q", doc.Text)
	}
}

func TestSync_DeleteTaskRemovesFromVectorStore(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask(bg(), "Task to delete", "", 0, nil, nil)

	vs.mu.Lock()
	_, hasBefore := vs.docs["task:1"]
	vs.mu.Unlock()
	if !hasBefore {
		t.Fatal("task should be in vector store before delete")
	}

	s.DeleteTask(bg(), 1, false)

	vs.mu.Lock()
	_, hasAfter := vs.docs["task:1"]
	vs.mu.Unlock()
	if hasAfter {
		t.Error("task should be removed from vector store after delete")
	}
}

func TestSemanticSearch_Basic(t *testing.T) {
	s, _, _, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Fix authentication bug", "Login token expiry", 1, nil, []string{"auth"})
	s.CreateTask(bg(), "Update documentation", "README changes", 3, nil, []string{"docs"})
	s.AddNote(bg(), 1, "Auth tokens expire after 5 minutes")

	results, err := syncer.SemanticSearch(bg(), "authentication token", store.SemanticSearchOptions{
		Limit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from semantic search")
	}
	// The auth-related task/note should rank higher than the docs task
	// (mock embedder uses character sum, so similar texts score similarly)
	t.Logf("Got %d results", len(results))
	for _, r := range results {
		t.Logf("  [%.3f] %s: %s", r.Score, r.ID, r.Text[:min(60, len(r.Text))])
	}
}

func TestSemanticSearch_TypeFilter(t *testing.T) {
	s, _, _, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Task A", "", 0, nil, nil)
	s.AddNote(bg(), 1, "Note for task A")

	results, err := syncer.SemanticSearch(bg(), "task", store.SemanticSearchOptions{
		Limit: 10,
		Type:  "note",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Metadata["type"] != "note" {
			t.Errorf("expected only notes, got %v", r.Metadata["type"])
		}
	}
}

func TestSemanticSearchContext(t *testing.T) {
	s, _, _, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Auth module", "Handle login flow", 0, nil, nil)
	s.AddNote(bg(), 1, "Uses JWT tokens")
	s.CreateTask(bg(), "Token refresh", "Implement refresh tokens", 0, nil, nil)

	results, err := syncer.SemanticSearchContext(bg(), 1, store.SemanticSearchOptions{
		Limit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Should not include task:1 or note:1 (excluded)
	for _, r := range results {
		if r.ID == "task:1" || r.ID == "note:1" {
			t.Errorf("context search should exclude source docs, got %s", r.ID)
		}
	}
	t.Logf("Got %d context results", len(results))
}

func TestReindex(t *testing.T) {
	s, _, vs, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Task 1", "", 0, nil, nil)
	s.CreateTask(bg(), "Task 2", "", 0, nil, nil)
	s.AddNote(bg(), 1, "Note 1")

	// Clear vector store manually
	vs.mu.Lock()
	vs.docs = make(map[string]vectorstore.Document)
	vs.mu.Unlock()

	// Reindex
	err := syncer.Reindex(bg(), false, func(done, total int) {
		t.Logf("Progress: %d/%d", done, total)
	})
	if err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	expected := []string{"task:1", "task:2", "note:1"}
	for _, id := range expected {
		if _, ok := vs.docs[id]; !ok {
			t.Errorf("expected %s in vector store after reindex", id)
		}
	}
}

func TestReindex_WithClear(t *testing.T) {
	s, _, vs, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Task 1", "", 0, nil, nil)
	s.AddNote(bg(), 1, "Note 1")

	// Reindex with clear=true should reset and repopulate
	err := syncer.Reindex(bg(), true, nil)
	if err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()
	if _, ok := vs.docs["task:1"]; !ok {
		t.Error("expected task:1 after clear reindex")
	}
	if _, ok := vs.docs["note:1"]; !ok {
		t.Error("expected note:1 after clear reindex")
	}
}

func TestSemanticSearch_ExcludesArchived(t *testing.T) {
	s, _, _, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Active task", "This is active", 0, nil, nil)
	s.AddNote(bg(), 1, "Note on active task")
	s.CreateTask(bg(), "Archived task", "This will be archived", 0, nil, nil)
	s.AddNote(bg(), 2, "Note on archived task")
	s.ArchiveTask(bg(), 2, true)

	// Default search (IncludeArchived=false) should exclude archived task and its note
	results, err := syncer.SemanticSearch(bg(), "task", store.SemanticSearchOptions{
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.ID == "task:2" {
			t.Errorf("default search should exclude archived task, got %s", r.ID)
		}
		// note:2 belongs to archived task 2 (note:1 is on task 1)
		if r.ID == "note:2" {
			t.Errorf("default search should exclude note of archived task, got %s", r.ID)
		}
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result from non-archived items")
	}
}

func TestSemanticSearch_IncludeArchived(t *testing.T) {
	s, _, _, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Active task", "This is active", 0, nil, nil)
	s.CreateTask(bg(), "Archived task", "This will be archived", 0, nil, nil)
	s.AddNote(bg(), 2, "Note on archived task")
	s.ArchiveTask(bg(), 2, true)

	// With IncludeArchived=true, should return all items
	results, err := syncer.SemanticSearch(bg(), "task", store.SemanticSearchOptions{
		Limit:           10,
		IncludeArchived: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	if !ids["task:2"] {
		t.Error("IncludeArchived should return archived task")
	}
	// Note is note:1 (first note created in this test)
	if !ids["note:1"] {
		t.Error("IncludeArchived should return note of archived task")
	}
}

func TestArchiveEvent_UpdatesNoteMetadata(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask(bg(), "Task with notes", "", 0, nil, nil)
	s.AddNote(bg(), 1, "Important note")

	// Before archive, note should have archived=false
	vs.mu.Lock()
	noteBefore := vs.docs["note:1"]
	vs.mu.Unlock()
	if archived, ok := noteBefore.Metadata["archived"].(bool); !ok || archived {
		t.Errorf("note should have archived=false before archiving, got %v", noteBefore.Metadata["archived"])
	}

	// Archive the task
	s.ArchiveTask(bg(), 1, true)

	// After archive, note should have archived=true
	vs.mu.Lock()
	noteAfter := vs.docs["note:1"]
	vs.mu.Unlock()
	if archived, ok := noteAfter.Metadata["archived"].(bool); !ok || !archived {
		t.Errorf("note should have archived=true after archiving, got %v", noteAfter.Metadata["archived"])
	}

	// Unarchive the task
	s.ArchiveTask(bg(), 1, false)

	// After unarchive, note should have archived=false again
	vs.mu.Lock()
	noteRestored := vs.docs["note:1"]
	vs.mu.Unlock()
	if archived, ok := noteRestored.Metadata["archived"].(bool); !ok || archived {
		t.Errorf("note should have archived=false after unarchiving, got %v", noteRestored.Metadata["archived"])
	}
}
