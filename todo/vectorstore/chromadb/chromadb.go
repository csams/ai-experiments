package chromadb

import (
	"context"
	"fmt"
	"sync"

	chroma "github.com/amikos-tech/chroma-go/pkg/api/v2"
	"github.com/amikos-tech/chroma-go/pkg/embeddings"

	"github.com/csams/todo/vectorstore"
)

const (
	metaKeyModel = "todo_embedder_model"
	metaKeyDims  = "todo_embedder_dims"
)

// Store implements vectorstore.VectorStore using ChromaDB.
type Store struct {
	client     chroma.Client
	collection chroma.Collection
	collName   string
	mu         sync.RWMutex // protects collection during Reset
}

// New creates a ChromaDB VectorStore.
func New(ctx context.Context, url, collectionName, tenant, database, authToken string) (*Store, error) {
	opts := []chroma.ClientOption{
		chroma.WithBaseURL(url),
	}
	if authToken != "" {
		opts = append(opts, chroma.WithAuth(
			chroma.NewTokenAuthCredentialsProvider(authToken, chroma.AuthorizationTokenHeader),
		))
	}
	if tenant != "" && database != "" {
		opts = append(opts, chroma.WithDatabaseAndTenant(database, tenant))
	} else if tenant != "" {
		opts = append(opts, chroma.WithTenant(tenant))
	}

	client, err := chroma.NewHTTPClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("chromadb client: %w", err)
	}

	coll, err := client.GetOrCreateCollection(ctx, collectionName,
		chroma.WithEmbeddingFunctionCreate(embeddings.NewConsistentHashEmbeddingFunction()),
	)
	if err != nil {
		return nil, fmt.Errorf("chromadb get/create collection %q: %w", collectionName, err)
	}

	return &Store{
		client:     client,
		collection: coll,
		collName:   collectionName,
	}, nil
}

func (s *Store) Upsert(ctx context.Context, docs []vectorstore.Document) error {
	if len(docs) == 0 {
		return nil
	}

	ids := make([]chroma.DocumentID, len(docs))
	texts := make([]string, len(docs))
	embs := make([]embeddings.Embedding, len(docs))
	metas := make([]chroma.DocumentMetadata, len(docs))

	for i, d := range docs {
		ids[i] = chroma.DocumentID(d.ID)
		texts[i] = d.Text
		embs[i] = embeddings.NewEmbeddingFromFloat32(d.Vector)
		metas[i] = toDocMeta(d.Metadata)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.collection.Upsert(ctx,
		chroma.WithIDs(ids...),
		chroma.WithTexts(texts...),
		chroma.WithEmbeddings(embs...),
		chroma.WithMetadatas(metas...),
	)
}

func (s *Store) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	docIDs := make([]chroma.DocumentID, len(ids))
	for i, id := range ids {
		docIDs[i] = chroma.DocumentID(id)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.collection.Delete(ctx, chroma.WithIDs(docIDs...))
}

func (s *Store) Search(ctx context.Context, query []float32, limit int, filter vectorstore.SearchFilter) ([]vectorstore.SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	queryEmb := embeddings.NewEmbeddingFromFloat32(query)

	opts := []chroma.CollectionQueryOption{
		chroma.WithQueryEmbeddings(queryEmb),
		chroma.WithNResults(limit),
		chroma.WithInclude(chroma.IncludeDocuments, chroma.IncludeMetadatas, chroma.IncludeDistances),
	}

	// Build where filter
	var clauses []chroma.WhereClause
	if filter.Type != nil {
		clauses = append(clauses, chroma.EqString(chroma.K("type"), *filter.Type))
	}
	if filter.TaskID != nil {
		clauses = append(clauses, chroma.EqInt(chroma.K("task_id"), int(*filter.TaskID)))
	}
	if filter.Archived != nil {
		clauses = append(clauses, chroma.EqBool(chroma.K("archived"), *filter.Archived))
	}

	if len(clauses) == 1 {
		opts = append(opts, chroma.WithWhere(clauses[0]))
	} else if len(clauses) > 1 {
		opts = append(opts, chroma.WithWhere(chroma.And(clauses...)))
	}

	result, err := s.collection.Query(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("chromadb query: %w", err)
	}

	return convertQueryResult(result, filter.ExcludeIDs), nil
}

func (s *Store) CollectionInfo(ctx context.Context) (string, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta := s.collection.Metadata()
	if meta == nil {
		return "", 0, nil
	}
	model, _ := meta.GetString(metaKeyModel)
	dimsInt, ok := meta.GetInt(metaKeyDims)
	if !ok {
		return model, 0, nil
	}
	return model, int(dimsInt), nil
}

func (s *Store) Reset(ctx context.Context, modelName string, dims int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Delete and recreate collection
	_ = s.client.DeleteCollection(ctx, s.collName)

	meta := chroma.NewMetadataFromMap(map[string]any{
		metaKeyModel: modelName,
		metaKeyDims:  int64(dims),
	})

	coll, err := s.client.CreateCollection(ctx, s.collName,
		chroma.WithCollectionMetadataCreate(meta),
		chroma.WithEmbeddingFunctionCreate(embeddings.NewConsistentHashEmbeddingFunction()),
	)
	if err != nil {
		return fmt.Errorf("chromadb recreate collection: %w", err)
	}
	s.collection = coll
	return nil
}

func (s *Store) Close() error {
	return nil
}

// --- helpers ---

// knownMetaKeys is the set of metadata keys written by the synced layer and
// read back in search results. When adding new metadata fields, update this
// list so they are preserved on both the write (toDocMeta) and read
// (extractMetaMap) paths.
var knownMetaKeys = []string{"type", "task_id", "note_id", "state", "priority", "archived"}

// toDocMeta converts a metadata map to ChromaDB DocumentMetadata.
// All provided keys are stored; see knownMetaKeys for the set that will be
// extracted on read.
func toDocMeta(m map[string]any) chroma.DocumentMetadata {
	dm := chroma.NewDocumentMetadata()
	for k, v := range m {
		switch val := v.(type) {
		case string:
			dm.SetString(k, val)
		case int:
			dm.SetInt(k, int64(val))
		case int64:
			dm.SetInt(k, val)
		case uint:
			dm.SetInt(k, int64(val))
		case float64:
			dm.SetFloat(k, val)
		case bool:
			dm.SetBool(k, val)
		default:
			dm.SetString(k, fmt.Sprintf("%v", val))
		}
	}
	return dm
}

// extractMetaMap extracts known metadata keys from a DocumentMetadata.
// Only keys in knownMetaKeys are returned; other keys stored by toDocMeta
// are silently dropped.
func extractMetaMap(dm chroma.DocumentMetadata) map[string]any {
	if dm == nil {
		return nil
	}
	m := make(map[string]any)
	for _, k := range knownMetaKeys {
		if v, ok := dm.GetRaw(k); ok {
			m[k] = v
		}
	}
	return m
}

func convertQueryResult(qr chroma.QueryResult, excludeIDs []string) []vectorstore.SearchResult {
	excludeSet := make(map[string]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		excludeSet[id] = true
	}

	idGroups := qr.GetIDGroups()
	docGroups := qr.GetDocumentsGroups()
	metaGroups := qr.GetMetadatasGroups()
	distGroups := qr.GetDistancesGroups()

	var results []vectorstore.SearchResult

	for gi := range idGroups {
		for di := range idGroups[gi] {
			id := string(idGroups[gi][di])
			if excludeSet[id] {
				continue
			}

			doc := ""
			if gi < len(docGroups) && di < len(docGroups[gi]) {
				doc = docGroups[gi][di].ContentString()
			}

			var meta map[string]any
			if gi < len(metaGroups) && di < len(metaGroups[gi]) {
				meta = extractMetaMap(metaGroups[gi][di])
			}

			// ChromaDB returns distances (lower = closer). Convert to similarity score.
			score := float32(0)
			if gi < len(distGroups) && di < len(distGroups[gi]) {
				dist := distGroups[gi][di]
				score = 1.0 / (1.0 + float32(dist))
			}

			results = append(results, vectorstore.SearchResult{
				Document: vectorstore.Document{
					ID:       id,
					Text:     doc,
					Metadata: meta,
				},
				Score: score,
			})
		}
	}

	return results
}

var _ vectorstore.VectorStore = (*Store)(nil)
