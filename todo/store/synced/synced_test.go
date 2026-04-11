package synced_test

import (
	"context"
	"fmt"
	"log/slog"
	"math"
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

func (m *mockEmbedder) Dimensions() int    { return 4 }
func (m *mockEmbedder) ModelName() string   { return "mock/test" }

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
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.Exec("PRAGMA foreign_keys = ON")

	gs, err := gormstore.New(db)
	if err != nil {
		t.Fatal(err)
	}

	emb := &mockEmbedder{}
	vs := newMockVectorStore()
	log := slog.Default()

	syncer := synced.New(vs, emb, gs, log)
	gs.AddObserver(syncer)

	t.Cleanup(func() { gs.Close() })
	return gs, emb, vs, syncer
}

// --- Tests ---

func TestSync_CreateTaskEmbedsDocument(t *testing.T) {
	s, emb, vs, _ := newTestSetup(t)

	_, err := s.CreateTask("Fix auth bug", "Token expiry issue", 1, nil, []string{"backend"})
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
	if !contains(doc.Text, "backend") {
		t.Errorf("embedded text should contain tag 'backend': %q", doc.Text)
	}
	if !contains(doc.Text, "New") {
		t.Errorf("embedded text should contain state 'New': %q", doc.Text)
	}
}

func TestSync_AddNoteEmbedsDocument(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask("Task", "", 0, nil, nil)
	_, err := s.AddNote(1, "investigation notes here")
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

	s.CreateTask("Task to delete", "", 0, nil, nil)

	vs.mu.Lock()
	_, hasBefore := vs.docs["task:1"]
	vs.mu.Unlock()
	if !hasBefore {
		t.Fatal("task should be in vector store before delete")
	}

	s.DeleteTask(1, false)

	vs.mu.Lock()
	_, hasAfter := vs.docs["task:1"]
	vs.mu.Unlock()
	if hasAfter {
		t.Error("task should be removed from vector store after delete")
	}
}

func TestSemanticSearch_Basic(t *testing.T) {
	s, _, _, syncer := newTestSetup(t)

	s.CreateTask("Fix authentication bug", "Login token expiry", 1, nil, []string{"auth"})
	s.CreateTask("Update documentation", "README changes", 3, nil, []string{"docs"})
	s.AddNote(1, "Auth tokens expire after 5 minutes")

	results, err := syncer.SemanticSearch(context.Background(), "authentication token", store.SemanticSearchOptions{
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

	s.CreateTask("Task A", "", 0, nil, nil)
	s.AddNote(1, "Note for task A")

	results, err := syncer.SemanticSearch(context.Background(), "task", store.SemanticSearchOptions{
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

	s.CreateTask("Auth module", "Handle login flow", 0, nil, nil)
	s.AddNote(1, "Uses JWT tokens")
	s.CreateTask("Token refresh", "Implement refresh tokens", 0, nil, nil)

	results, err := syncer.SemanticSearchContext(context.Background(), 1, store.SemanticSearchOptions{
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

	s.CreateTask("Task 1", "", 0, nil, nil)
	s.CreateTask("Task 2", "", 0, nil, nil)
	s.AddNote(1, "Note 1")

	// Clear vector store manually
	vs.mu.Lock()
	vs.docs = make(map[string]vectorstore.Document)
	vs.mu.Unlock()

	// Reindex
	err := syncer.Reindex(context.Background(), false, func(done, total int) {
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Suppress unused import warning
var _ = fmt.Sprintf
