package embed

import "context"

// Embedder generates vector embeddings from text.
type Embedder interface {
	// Embed generates an embedding vector for a single text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embedding vectors for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the size of embedding vectors produced by this embedder.
	Dimensions() int

	// ModelName returns an identifier for dimension mismatch detection.
	ModelName() string
}
