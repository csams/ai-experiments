package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withFakeOpenAI swaps the embedder's HTTP client to point at a test
// server and rewrites the base URL via a transport. The embedder reads
// its endpoint as a hardcoded string, so the cleanest test-double
// approach is a RoundTripper that rewrites the request URL.
func withFakeOpenAI(t *testing.T, handler func(req *FakeRequest) FakeResponse) *Embedder {
	t.Helper()
	srv := httptest.NewServer(fakeHandler(t, handler))
	t.Cleanup(srv.Close)

	e := &Embedder{
		apiKey: "test-key",
		model:  "text-embedding-3-small",
		dims:   1536,
		client: srv.Client(),
	}
	// Swap the transport to rewrite the request URL so the embedder's
	// hardcoded https://api.openai.com/... lands at the test server.
	rt := e.client.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	e.client.Transport = rewriteHostTransport{base: srv.URL, inner: rt}
	return e
}

func TestOpenAIEmbedder_ShortResponseRejected(t *testing.T) {
	e := withFakeOpenAI(t, func(_ *FakeRequest) FakeResponse {
		// Return only 1 entry for a 3-input batch.
		return FakeResponse{Embeddings: [][]float32{{1, 2, 3}}}
	})

	_, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("expected error when OpenAI returns fewer entries than requested")
	}
	if !strings.Contains(err.Error(), "returned 1 embeddings for a batch of 3") {
		t.Errorf("error message should name the count mismatch; got %q", err.Error())
	}
}

func TestOpenAIEmbedder_LongResponseRejected(t *testing.T) {
	e := withFakeOpenAI(t, func(_ *FakeRequest) FakeResponse {
		// Return 4 entries for a 2-input batch.
		return FakeResponse{Embeddings: [][]float32{{1}, {2}, {3}, {4}}}
	})

	_, err := e.EmbedBatch(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("expected error when OpenAI returns more entries than requested")
	}
	if !strings.Contains(err.Error(), "returned 4 embeddings for a batch of 2") {
		t.Errorf("error message should name the count mismatch; got %q", err.Error())
	}
}

func TestOpenAIEmbedder_HappyPath(t *testing.T) {
	e := withFakeOpenAI(t, func(req *FakeRequest) FakeResponse {
		// Echo: one embedding per input, in order.
		out := make([][]float32, len(req.Inputs))
		for i := range req.Inputs {
			out[i] = []float32{float32(i)}
		}
		return FakeResponse{Embeddings: out}
	})

	got, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len(got) = %d, want 3", len(got))
	}
}

func TestOpenAIEmbedder_OutOfOrderResponseIsReordered(t *testing.T) {
	// OpenAI's spec says the response order matches input order, but
	// the embedder defensively uses Index to reassemble. Confirm.
	e := withFakeOpenAI(t, func(_ *FakeRequest) FakeResponse {
		return FakeResponse{
			Embeddings:        [][]float32{{2}, {0}, {1}},
			IndicesOverride:   []int{2, 0, 1},
			SuppressAutoIndex: true,
		}
	})

	got, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	// Reassembled in input order: got[0] should be {0}, got[1]={1}, got[2]={2}.
	for i, vec := range got {
		if len(vec) != 1 || vec[0] != float32(i) {
			t.Errorf("got[%d] = %v, want [%d]", i, vec, i)
		}
	}
}

func TestOpenAIEmbedder_DuplicateIndexRejected(t *testing.T) {
	e := withFakeOpenAI(t, func(_ *FakeRequest) FakeResponse {
		return FakeResponse{
			Embeddings:        [][]float32{{1}, {2}},
			IndicesOverride:   []int{0, 0}, // both claim index 0
			SuppressAutoIndex: true,
		}
	})

	_, err := e.EmbedBatch(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for duplicate Index")
	}
	if !strings.Contains(err.Error(), "duplicate embedding index") {
		t.Errorf("error should mention duplicate; got %q", err.Error())
	}
}

func TestOpenAIEmbedder_InvalidIndexRejected(t *testing.T) {
	cases := []struct {
		name      string
		overrides []int
		wantSub   string
	}{
		{"too_large", []int{0, 99}, "invalid embedding index 99"},
		{"negative", []int{0, -1}, "invalid embedding index -1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := withFakeOpenAI(t, func(_ *FakeRequest) FakeResponse {
				return FakeResponse{
					Embeddings:        [][]float32{{1}, {2}},
					IndicesOverride:   tc.overrides,
					SuppressAutoIndex: true,
				}
			})
			_, err := e.EmbedBatch(context.Background(), []string{"a", "b"})
			if err == nil {
				t.Fatal("expected error for out-of-range Index")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error should name the bad index; got %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestOpenAIInputCount(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    int
		wantErr bool
	}{
		{"single string", "hello", 1, false},
		{"empty slice", []string{}, 0, false},
		{"three element slice", []string{"a", "b", "c"}, 3, false},
		{"int rejected", 7, 0, true},
		{"nil rejected", nil, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := openaiInputCount(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("count = %d, want %d", got, tc.want)
			}
		})
	}
}
