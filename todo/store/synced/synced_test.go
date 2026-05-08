package synced_test

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/csams/todo/model"
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

func (m *mockVectorStore) DeleteTaskDocs(_ context.Context, taskID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, d := range m.docs {
		if t, _ := d.Metadata["type"].(string); t != "task" {
			continue
		}
		if tid, ok := d.Metadata["task_id"].(int); ok && uint(tid) == taskID {
			delete(m.docs, id)
		}
	}
	return nil
}

func (m *mockVectorStore) DeleteNoteDocs(_ context.Context, noteID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, d := range m.docs {
		if t, _ := d.Metadata["type"].(string); t != "note" {
			continue
		}
		if nid, ok := d.Metadata["note_id"].(int); ok && uint(nid) == noteID {
			delete(m.docs, id)
		}
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
		if filter.ExcludeTaskID != nil {
			if tid, ok := d.Metadata["task_id"].(int); ok && uint(tid) == *filter.ExcludeTaskID {
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

// findTaskChunks returns all chunks for the given task, sorted by chunk index.
// Caller must already hold m.mu.
func (m *mockVectorStore) findTaskChunks(taskID uint) []vectorstore.Document {
	var out []vectorstore.Document
	for _, d := range m.docs {
		if t, _ := d.Metadata["type"].(string); t != "task" {
			continue
		}
		if tid, ok := d.Metadata["task_id"].(int); ok && uint(tid) == taskID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChunkIndex < out[j].ChunkIndex })
	return out
}

// findNoteChunks returns all chunks for the given note. Caller must already hold m.mu.
func (m *mockVectorStore) findNoteChunks(noteID uint) []vectorstore.Document {
	var out []vectorstore.Document
	for _, d := range m.docs {
		if t, _ := d.Metadata["type"].(string); t != "note" {
			continue
		}
		if nid, ok := d.Metadata["note_id"].(int); ok && uint(nid) == noteID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChunkIndex < out[j].ChunkIndex })
	return out
}

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

func uintPtr(v uint) *uint { return &v }

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
	chunks := vs.findTaskChunks(1)
	if len(chunks) == 0 {
		t.Fatal("expected task 1 chunks in vector store")
	}
	doc := chunks[0]
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
	_, err := s.AddNote(bg(), uintPtr(1), "investigation notes here")
	if err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()
	chunks := vs.findNoteChunks(1)
	if len(chunks) == 0 {
		t.Fatal("expected note 1 chunks in vector store")
	}
	if !strings.Contains(chunks[0].Text, "investigation notes here") {
		t.Errorf("chunk text should contain note body: %q", chunks[0].Text)
	}
}

func TestSync_StandaloneNoteEmbedsWithoutTaskID(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	note, err := s.AddNote(bg(), nil, "standalone capture text")
	if err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	chunks := vs.findNoteChunks(note.ID)
	vs.mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("standalone note not embedded")
	}
	doc := chunks[0]
	if _, present := doc.Metadata["task_id"]; present {
		t.Errorf("standalone note metadata should omit task_id, got: %v", doc.Metadata)
	}
	if archived, ok := doc.Metadata["archived"].(bool); !ok || archived {
		t.Errorf("standalone note metadata archived = %v, want false", doc.Metadata["archived"])
	}
}

func TestSync_NoteReparentReembedsMetadata(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	t1, _ := s.CreateTask(bg(), "T1", "", 0, nil, nil)
	t2, _ := s.CreateTask(bg(), "T2", "", 0, nil, nil)
	note, _ := s.AddNote(bg(), &t1.ID, "n")

	if _, err := s.UpdateNote(bg(), note.ID, store.UpdateNoteOptions{
		SetTaskID: true,
		TaskID:    &t2.ID,
	}); err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	chunks := vs.findNoteChunks(note.ID)
	vs.mu.Unlock()
	if len(chunks) == 0 {
		t.Fatalf("expected note %d chunks", note.ID)
	}
	doc := chunks[0]
	if got, ok := doc.Metadata["task_id"].(int); !ok || uint(got) != t2.ID {
		t.Errorf("after reparent, task_id metadata = %v, want %d", doc.Metadata["task_id"], t2.ID)
	}
}

func TestSync_DeleteTaskRemovesFromVectorStore(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask(bg(), "Task to delete", "", 0, nil, nil)

	vs.mu.Lock()
	hasBefore := len(vs.findTaskChunks(1)) > 0
	vs.mu.Unlock()
	if !hasBefore {
		t.Fatal("task should be in vector store before delete")
	}

	s.DeleteTask(bg(), 1, store.DeleteTaskOptions{})

	vs.mu.Lock()
	hasAfter := len(vs.findTaskChunks(1)) > 0
	vs.mu.Unlock()
	if hasAfter {
		t.Error("task should be removed from vector store after delete")
	}
}

func TestSync_AddLinkRefreshesTaskEmbedding(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask(bg(), "Auth bug", "", 0, nil, nil)
	if _, err := s.AddLink(bg(), 1, model.LinkJira, "AUTH-456", "original ticket describing the regression"); err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	chunks := vs.findTaskChunks(1)
	vs.mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("expected task 1 chunks in vector store")
	}
	if !strings.Contains(chunks[0].Text, "original ticket describing the regression") {
		t.Errorf("link description not folded into task embedding text: %q", chunks[0].Text)
	}
}

func TestSync_UpdateLinkDescriptionRefreshesTaskEmbedding(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask(bg(), "Task", "", 0, nil, nil)
	link, _ := s.AddLink(bg(), 1, model.LinkURL, "https://x.example.com", "first description")

	newDesc := "second description after edit"
	if _, err := s.UpdateLink(bg(), 1, link.ID, store.UpdateLinkOptions{Description: &newDesc}); err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	chunks := vs.findTaskChunks(1)
	vs.mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("expected task 1 chunks")
	}
	doc := chunks[0]
	if strings.Contains(doc.Text, "first description") {
		t.Errorf("stale link description should be replaced after update: %q", doc.Text)
	}
	if !strings.Contains(doc.Text, "second description after edit") {
		t.Errorf("updated link description not in embedding: %q", doc.Text)
	}
}

func TestSync_ClearLinkDescriptionRefreshesTaskEmbedding(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask(bg(), "Task", "", 0, nil, nil)
	link, _ := s.AddLink(bg(), 1, model.LinkURL, "https://x.example.com", "secret content to remove")

	empty := ""
	if _, err := s.UpdateLink(bg(), 1, link.ID, store.UpdateLinkOptions{Description: &empty}); err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	chunks := vs.findTaskChunks(1)
	vs.mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("expected task 1 chunks")
	}
	if strings.Contains(chunks[0].Text, "secret content to remove") {
		t.Errorf("cleared description should be gone from embedding: %q", chunks[0].Text)
	}
}

func TestSync_DeleteLinkRefreshesTaskEmbedding(t *testing.T) {
	s, _, vs, _ := newTestSetup(t)

	s.CreateTask(bg(), "Task", "", 0, nil, nil)
	link, _ := s.AddLink(bg(), 1, model.LinkURL, "https://x.example.com", "to be removed")

	if err := s.DeleteLink(bg(), 1, link.ID); err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	chunks := vs.findTaskChunks(1)
	vs.mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("expected task 1 chunks")
	}
	if strings.Contains(chunks[0].Text, "to be removed") {
		t.Errorf("deleted link description should not be in embedding: %q", chunks[0].Text)
	}
}

func TestReindex_IncludesLinkDescriptions(t *testing.T) {
	s, _, vs, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Reindex me", "", 0, nil, nil)
	s.AddLink(bg(), 1, model.LinkJira, "PROJ-1", "searchable link content")

	vs.mu.Lock()
	vs.docs = make(map[string]vectorstore.Document)
	vs.mu.Unlock()

	if err := syncer.Reindex(bg(), false, nil); err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	chunks := vs.findTaskChunks(1)
	vs.mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("expected task 1 chunks after reindex")
	}
	if !strings.Contains(chunks[0].Text, "searchable link content") {
		t.Errorf("Reindex should include link description in task embedding: %q", chunks[0].Text)
	}
}

func TestSemanticSearch_Basic(t *testing.T) {
	s, _, _, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Fix authentication bug", "Login token expiry", 1, nil, []string{"auth"})
	s.CreateTask(bg(), "Update documentation", "README changes", 3, nil, []string{"docs"})
	s.AddNote(bg(), uintPtr(1), "Auth tokens expire after 5 minutes")

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
	s.AddNote(bg(), uintPtr(1), "Note for task A")

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
	s.AddNote(bg(), uintPtr(1), "Uses JWT tokens")
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
	s.AddNote(bg(), uintPtr(1), "Note 1")

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

	if len(vs.findTaskChunks(1)) == 0 {
		t.Errorf("expected task 1 chunks after reindex")
	}
	if len(vs.findTaskChunks(2)) == 0 {
		t.Errorf("expected task 2 chunks after reindex")
	}
	if len(vs.findNoteChunks(1)) == 0 {
		t.Errorf("expected note 1 chunks after reindex")
	}
}

func TestReindex_WithClear(t *testing.T) {
	s, _, vs, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Task 1", "", 0, nil, nil)
	s.AddNote(bg(), uintPtr(1), "Note 1")

	// Reindex with clear=true should reset and repopulate
	err := syncer.Reindex(bg(), true, nil)
	if err != nil {
		t.Fatal(err)
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()
	if len(vs.findTaskChunks(1)) == 0 {
		t.Error("expected task 1 chunks after clear reindex")
	}
	if len(vs.findNoteChunks(1)) == 0 {
		t.Error("expected note 1 chunks after clear reindex")
	}
}

func TestSemanticSearch_ExcludesArchived(t *testing.T) {
	s, _, _, syncer := newTestSetup(t)

	s.CreateTask(bg(), "Active task", "This is active", 0, nil, nil)
	s.AddNote(bg(), uintPtr(1), "Note on active task")
	s.CreateTask(bg(), "Archived task", "This will be archived", 0, nil, nil)
	s.AddNote(bg(), uintPtr(2), "Note on archived task")
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
	s.AddNote(bg(), uintPtr(2), "Note on archived task")
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
	s.AddNote(bg(), uintPtr(1), "Important note")

	// Before archive, note should have archived=false
	vs.mu.Lock()
	beforeChunks := vs.findNoteChunks(1)
	vs.mu.Unlock()
	if len(beforeChunks) == 0 {
		t.Fatal("expected note 1 chunks")
	}
	noteBefore := beforeChunks[0]
	if archived, ok := noteBefore.Metadata["archived"].(bool); !ok || archived {
		t.Errorf("note should have archived=false before archiving, got %v", noteBefore.Metadata["archived"])
	}

	// Archive the task
	s.ArchiveTask(bg(), 1, true)

	// After archive, note should have archived=true
	vs.mu.Lock()
	afterChunks := vs.findNoteChunks(1)
	vs.mu.Unlock()
	if len(afterChunks) == 0 {
		t.Fatal("expected note 1 chunks after archive")
	}
	noteAfter := afterChunks[0]
	if archived, ok := noteAfter.Metadata["archived"].(bool); !ok || !archived {
		t.Errorf("note should have archived=true after archiving, got %v", noteAfter.Metadata["archived"])
	}

	// Unarchive the task
	s.ArchiveTask(bg(), 1, false)

	// After unarchive, note should have archived=false again
	vs.mu.Lock()
	restoredChunks := vs.findNoteChunks(1)
	vs.mu.Unlock()
	if len(restoredChunks) == 0 {
		t.Fatal("expected note 1 chunks after unarchive")
	}
	noteRestored := restoredChunks[0]
	if archived, ok := noteRestored.Metadata["archived"].(bool); !ok || archived {
		t.Errorf("note should have archived=false after unarchiving, got %v", noteRestored.Metadata["archived"])
	}
}
