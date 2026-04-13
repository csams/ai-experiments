package pgvector

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	pgv "github.com/pgvector/pgvector-go"
	"gorm.io/gorm"

	"github.com/csams/todo/vectorstore"
)

const (
	metaKeyModel = "todo_embedder_model"
	metaKeyDims  = "todo_embedder_dims"
)

// Store implements vectorstore.VectorStore using pgvector in PostgreSQL.
type Store struct {
	db   *gorm.DB
	dims int
	mu   sync.RWMutex // protects dims and table during Reset
}

// New creates a pgvector VectorStore that shares the given *gorm.DB connection.
// It enables the pgvector extension and creates tables. Metadata is only written
// when no existing metadata is found, so the caller's dimension mismatch check
// (via CollectionInfo) can detect changes. Reset() updates the metadata.
func New(db *gorm.DB, modelName string, dims int) (*Store, error) {
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return nil, fmt.Errorf("pgvector extension: %w", err)
	}

	if err := createTables(db, dims); err != nil {
		return nil, err
	}

	// Only insert metadata if none exists. This preserves existing metadata so the
	// caller can detect dimension mismatches between the stored model and the current
	// embedder. Reset() overwrites metadata when the user explicitly reindexes.
	if err := insertMetaIfMissing(db, modelName, dims); err != nil {
		return nil, err
	}

	return &Store{db: db, dims: dims}, nil
}

func createTables(db *gorm.DB, dims int) error {
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS vector_documents (
			id         TEXT PRIMARY KEY,
			text       TEXT NOT NULL,
			embedding  vector(%d) NOT NULL,
			doc_type   TEXT,
			task_id    INTEGER,
			note_id    INTEGER,
			state      TEXT,
			priority   INTEGER,
			archived   BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`, dims)
	if err := db.Exec(ddl).Error; err != nil {
		return fmt.Errorf("create vector_documents: %w", err)
	}

	if err := db.Exec(`
		CREATE TABLE IF NOT EXISTS vector_metadata (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`).Error; err != nil {
		return fmt.Errorf("create vector_metadata: %w", err)
	}

	// HNSW index — CREATE INDEX IF NOT EXISTS is idempotent.
	if err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_vector_documents_embedding
		ON vector_documents USING hnsw (embedding vector_cosine_ops)
	`).Error; err != nil {
		return fmt.Errorf("create hnsw index: %w", err)
	}

	return nil
}

func insertMetaIfMissing(db *gorm.DB, modelName string, dims int) error {
	if err := db.Exec(`
		INSERT INTO vector_metadata (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO NOTHING
	`, metaKeyModel, modelName).Error; err != nil {
		return fmt.Errorf("insert model metadata: %w", err)
	}
	if err := db.Exec(`
		INSERT INTO vector_metadata (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO NOTHING
	`, metaKeyDims, strconv.Itoa(dims)).Error; err != nil {
		return fmt.Errorf("insert dims metadata: %w", err)
	}
	return nil
}

func upsertMeta(db *gorm.DB, modelName string, dims int) error {
	if err := db.Exec(`
		INSERT INTO vector_metadata (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, metaKeyModel, modelName).Error; err != nil {
		return fmt.Errorf("upsert model metadata: %w", err)
	}
	if err := db.Exec(`
		INSERT INTO vector_metadata (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, metaKeyDims, strconv.Itoa(dims)).Error; err != nil {
		return fmt.Errorf("upsert dims metadata: %w", err)
	}
	return nil
}

func (s *Store) Upsert(ctx context.Context, docs []vectorstore.Document) error {
	if len(docs) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		for _, d := range docs {
			docType, _ := d.Metadata["type"].(string)
			taskID := metaInt(d.Metadata, "task_id")
			noteID := metaInt(d.Metadata, "note_id")
			state, _ := d.Metadata["state"].(string)
			priority := metaInt(d.Metadata, "priority")
			archived, _ := d.Metadata["archived"].(bool)

			if err := tx.Exec(`
				INSERT INTO vector_documents (id, text, embedding, doc_type, task_id, note_id, state, priority, archived, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET
					text = EXCLUDED.text,
					embedding = EXCLUDED.embedding,
					doc_type = EXCLUDED.doc_type,
					task_id = EXCLUDED.task_id,
					note_id = EXCLUDED.note_id,
					state = EXCLUDED.state,
					priority = EXCLUDED.priority,
					archived = EXCLUDED.archived,
					updated_at = EXCLUDED.updated_at
			`, d.ID, d.Text, pgv.NewVector(d.Vector), docType, taskID, noteID, state, priority, archived, now, now).Error; err != nil {
				return fmt.Errorf("upsert %q: %w", d.ID, err)
			}
		}

		return nil
	})
}

func (s *Store) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.db.WithContext(ctx).Exec(
		"DELETE FROM vector_documents WHERE id IN (?)", ids,
	).Error; err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

func (s *Store) Search(ctx context.Context, query []float32, limit int, filter vectorstore.SearchFilter) ([]vectorstore.SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	qv := pgv.NewVector(query)

	var args []any
	var conditions []string

	if filter.Type != nil {
		conditions = append(conditions, "doc_type = ?")
		args = append(args, *filter.Type)
	}
	if filter.TaskID != nil {
		conditions = append(conditions, "task_id = ?")
		args = append(args, *filter.TaskID)
	}
	if filter.Archived != nil {
		conditions = append(conditions, "archived = ?")
		args = append(args, *filter.Archived)
	}
	if len(filter.ExcludeIDs) > 0 {
		conditions = append(conditions, "id NOT IN (?)")
		args = append(args, filter.ExcludeIDs)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Cosine similarity: 1 - cosine_distance gives [-1, 1] where 1 = identical.
	sql := fmt.Sprintf(`
		SELECT id, text, doc_type, task_id, note_id, state, priority, archived,
		       1 - (embedding <=> ?) AS score
		FROM vector_documents
		%s
		ORDER BY embedding <=> ?
		LIMIT ?
	`, where)

	// Prepend the query vector (for score), append it again (for ORDER BY), then limit.
	finalArgs := []any{qv}
	finalArgs = append(finalArgs, args...)
	finalArgs = append(finalArgs, qv, limit)

	type row struct {
		ID       string
		Text     string
		DocType  *string
		TaskID   *int
		NoteID   *int
		State    *string
		Priority *int
		Archived *bool
		Score    float32
	}

	var rows []row
	if err := s.db.WithContext(ctx).Raw(sql, finalArgs...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	results := make([]vectorstore.SearchResult, len(rows))
	for i, r := range rows {
		results[i] = vectorstore.SearchResult{
			Document: vectorstore.Document{
				ID:       r.ID,
				Text:     r.Text,
				Metadata: buildMeta(r.DocType, r.TaskID, r.NoteID, r.State, r.Priority, r.Archived),
			},
			Score: r.Score,
		}
	}

	return results, nil
}

func (s *Store) CollectionInfo(ctx context.Context) (string, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type kv struct {
		Key   string
		Value string
	}
	var rows []kv
	if err := s.db.WithContext(ctx).Raw(
		"SELECT key, value FROM vector_metadata WHERE key IN (?, ?)",
		metaKeyModel, metaKeyDims,
	).Scan(&rows).Error; err != nil {
		return "", 0, fmt.Errorf("collection info: %w", err)
	}

	var model string
	var dims int
	for _, r := range rows {
		switch r.Key {
		case metaKeyModel:
			model = r.Value
		case metaKeyDims:
			dims, _ = strconv.Atoi(r.Value)
		}
	}
	return model, dims, nil
}

func (s *Store) Reset(ctx context.Context, modelName string, dims int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dims != s.dims {
		// Dimension changed — must drop and recreate table + index.
		if err := s.db.WithContext(ctx).Exec("DROP TABLE IF EXISTS vector_documents").Error; err != nil {
			return fmt.Errorf("drop vector_documents: %w", err)
		}
		if err := createTables(s.db, dims); err != nil {
			return err
		}
		s.dims = dims
	} else {
		if err := s.db.WithContext(ctx).Exec("TRUNCATE vector_documents").Error; err != nil {
			return fmt.Errorf("truncate vector_documents: %w", err)
		}
	}

	if err := upsertMeta(s.db, modelName, dims); err != nil {
		return err
	}

	return nil
}

func (s *Store) Close() error {
	return nil
}

// --- helpers ---

// metaInt extracts an integer from a metadata map, handling int, int64, uint, and float64.
func metaInt(m map[string]any, key string) *int {
	v, ok := m[key]
	if !ok {
		return nil
	}
	var n int
	switch val := v.(type) {
	case int:
		n = val
	case int64:
		n = int(val)
	case uint:
		n = int(val)
	case float64:
		n = int(val)
	default:
		return nil
	}
	return &n
}

// buildMeta reconstructs a metadata map from explicit columns.
// Only non-nil values are included.
func buildMeta(docType *string, taskID, noteID *int, state *string, priority *int, archived *bool) map[string]any {
	m := make(map[string]any)
	if docType != nil {
		m["type"] = *docType
	}
	if taskID != nil {
		m["task_id"] = *taskID
	}
	if noteID != nil {
		m["note_id"] = *noteID
	}
	if state != nil {
		m["state"] = *state
	}
	if priority != nil {
		m["priority"] = *priority
	}
	if archived != nil {
		m["archived"] = *archived
	}
	return m
}

var _ vectorstore.VectorStore = (*Store)(nil)
