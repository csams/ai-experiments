package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fakeOpenAIPath is the only OpenAI endpoint the embedder hits.
const fakeOpenAIPath = "/v1/embeddings"

// FakeRequest captures the inputs the embedder sent so test cases can
// reflect them in their response.
type FakeRequest struct {
	Model  string
	Inputs []string
}

// FakeResponse drives the fake handler. Embeddings is the per-input
// vector slice. By default each entry's Index field is its position
// in the slice (matching the API's documented spec). Setting
// SuppressAutoIndex=true switches to IndicesOverride[i] for each
// entry — used to pin pathological out-of-order / duplicate / out-
// of-range responses. IndicesOverride is ignored when
// SuppressAutoIndex is false; the spec-conforming path is the
// default for clarity.
type FakeResponse struct {
	Embeddings        [][]float32
	IndicesOverride   []int
	SuppressAutoIndex bool
	// StatusCode overrides the response code; 0 → 200.
	StatusCode int
}

// fakeHandler builds the HTTP handler the test server runs. Each request
// is decoded into a FakeRequest, passed to the test's handler closure,
// and its FakeResponse is rendered as the OpenAI API would.
func fakeHandler(t *testing.T, h func(*FakeRequest) FakeResponse) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != fakeOpenAIPath {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// The embedder's request shape is `{"model": ..., "input": <string|[]string>}`.
		// Decode loosely so we can extract the input shape either way.
		var probe struct {
			Model string          `json:"model"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		req := &FakeRequest{Model: probe.Model}
		// Try []string then string.
		if err := json.Unmarshal(probe.Input, &req.Inputs); err != nil {
			var single string
			if err := json.Unmarshal(probe.Input, &single); err != nil {
				http.Error(w, "input neither string nor []string", http.StatusBadRequest)
				return
			}
			req.Inputs = []string{single}
		}

		resp := h(req)
		status := resp.StatusCode
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		// Build OpenAI-shaped response. Each Data entry is {embedding,
		// index}. Default Index = i (spec-conforming). When
		// SuppressAutoIndex is true the index comes from
		// IndicesOverride[i] (used to pin out-of-order / duplicate /
		// out-of-range scenarios).
		type entry struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}
		out := struct {
			Data []entry `json:"data"`
		}{}
		for i, vec := range resp.Embeddings {
			idx := i
			if resp.SuppressAutoIndex && i < len(resp.IndicesOverride) {
				idx = resp.IndicesOverride[i]
			}
			out.Data = append(out.Data, entry{Embedding: vec, Index: idx})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}

// rewriteHostTransport rewrites every outgoing request's host to point
// at the test server. The embedder hardcodes https://api.openai.com/...
// in its endpoint, so the only way to redirect it without changing the
// production code is to intercept at the transport.
type rewriteHostTransport struct {
	base  string // e.g. "http://127.0.0.1:53901"
	inner http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	dst, err := url.Parse(t.base)
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = dst.Scheme
	clone.URL.Host = dst.Host
	clone.Host = dst.Host
	// Preserve only the path the embedder set (e.g. /v1/embeddings).
	if !strings.HasPrefix(clone.URL.Path, "/") {
		clone.URL.Path = "/" + clone.URL.Path
	}
	return t.inner.RoundTrip(clone)
}

