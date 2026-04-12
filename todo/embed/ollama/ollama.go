package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/csams/todo/embed"
)

// Embedder implements embed.Embedder using the Ollama HTTP API.
type Embedder struct {
	url    string // e.g., "http://localhost:11434"
	model  string // e.g., "nomic-embed-text"
	dims   int
	client *http.Client
}

// New creates an Ollama Embedder. It queries the model to determine dimensions.
func New(url, model string) (*Embedder, error) {
	e := &Embedder{
		url:    url,
		model:  model,
		client: &http.Client{Timeout: 30 * time.Second},
	}

	// Probe dimensions by embedding a short string
	vec, err := e.Embed(context.Background(), "dimension probe")
	if err != nil {
		return nil, fmt.Errorf("ollama probe (is Ollama running at %s with model %s?): %w", url, model, err)
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("ollama returned empty embedding vector for model %s", model)
	}
	e.dims = len(vec)

	return e, nil
}

type embedRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.doEmbed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("ollama returned no embeddings")
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
	body, err := json.Marshal(embedRequest{Model: e.model, Input: input})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.url+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embedding request failed with status %d: %s", resp.StatusCode, string(errBody))
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama decode: %w", err)
	}

	return result.Embeddings, nil
}

func (e *Embedder) Dimensions() int {
	return e.dims
}

func (e *Embedder) ModelName() string {
	return "ollama/" + e.model
}

var _ embed.Embedder = (*Embedder)(nil)
