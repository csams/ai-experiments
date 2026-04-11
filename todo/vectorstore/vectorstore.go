package vectorstore

import "context"

// Document represents an embedded document stored in the vector database.
type Document struct {
	ID       string            // e.g., "task:42", "note:17"
	Text     string            // the text that was embedded
	Metadata map[string]any    // task_id, type ("task"/"note"), title, state, etc.
	Vector   []float32         // embedding vector
}

// SearchResult is a single result from vector similarity search.
type SearchResult struct {
	Document
	Score float32 // similarity score (higher = more similar)
}

// SearchFilter controls vector search filtering.
type SearchFilter struct {
	Type       *string  // "task", "note"
	TaskID     *uint    // filter to a specific task's entities
	Archived   *bool    // filter by archived status
	ExcludeIDs []string // exclude specific document IDs
}

// VectorStore provides vector storage and similarity search.
type VectorStore interface {
	// Upsert inserts or updates documents with their embeddings and metadata.
	Upsert(ctx context.Context, docs []Document) error

	// Delete removes documents by their IDs.
	Delete(ctx context.Context, ids []string) error

	// Search finds the most similar documents to the query vector.
	Search(ctx context.Context, query []float32, limit int, filter SearchFilter) ([]SearchResult, error)

	// CollectionInfo returns the model name and dimensions stored in collection metadata.
	// Returns ("", 0) if no collection exists or metadata is not set.
	CollectionInfo(ctx context.Context) (modelName string, dims int, err error)

	// Reset drops and recreates the collection (used for reindex with --clear).
	Reset(ctx context.Context, modelName string, dims int) error

	// Close releases resources.
	Close() error
}
