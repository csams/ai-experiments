package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/csams/todo/embed"
)

// Embedder implements embed.Embedder using the OpenAI API.
type Embedder struct {
	apiKey string
	model  string
	dims   int
	client *http.Client
}

// New creates an OpenAI Embedder. Reads API key from OPENAI_API_KEY env var.
func New(model string) (*Embedder, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is not set")
	}

	var dims int
	switch model {
	case "text-embedding-3-small":
		dims = 1536
	case "text-embedding-3-large":
		dims = 3072
	case "text-embedding-ada-002":
		dims = 1536
	default:
		return nil, fmt.Errorf("unknown embedding model %q; supported: text-embedding-3-small, text-embedding-3-large, text-embedding-ada-002", model)
	}

	return &Embedder{
		apiKey: key,
		model:  model,
		dims:   dims,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

type embeddingRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.doEmbed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("openai returned no embeddings")
	}
	return vecs[0], nil
}

func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return e.doEmbed(ctx, texts)
}

func (e *Embedder) doEmbed(ctx context.Context, input any) ([][]float32, error) {
	body, err := json.Marshal(embeddingRequest{Model: e.model, Input: input})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai embedding request failed with status %d: %s", resp.StatusCode, string(errBody))
	}

	var result embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai decode: %w", err)
	}

	vecs := make([][]float32, len(result.Data))
	for _, d := range result.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("openai returned invalid embedding index %d for batch of %d", d.Index, len(vecs))
		}
		if vecs[d.Index] != nil {
			return nil, fmt.Errorf("openai returned duplicate embedding index %d", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}

func (e *Embedder) Dimensions() int {
	return e.dims
}

func (e *Embedder) ModelName() string {
	return "openai/" + e.model
}

var _ embed.Embedder = (*Embedder)(nil)
