package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodySizeLimitMiddleware(t *testing.T) {
	// Sink handler reads the full body and reports the byte count it could
	// read, plus whether the read errored. The middleware-installed
	// MaxBytesReader must surface a *http.MaxBytesError on Read once the
	// cap is exceeded.
	sink := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	})

	cases := []struct {
		name       string
		maxBytes   int64
		bodySize   int
		wantStatus int
		wantBody   string // substring; "" = don't assert body
	}{
		{name: "under_limit_passes", maxBytes: 1024, bodySize: 512, wantStatus: http.StatusOK},
		{name: "at_limit_passes", maxBytes: 1024, bodySize: 1024, wantStatus: http.StatusOK},
		{name: "over_limit_errors", maxBytes: 1024, bodySize: 2048, wantStatus: http.StatusBadRequest, wantBody: "request body too large"},
		{name: "zero_disables_cap", maxBytes: 0, bodySize: 10_000_000, wantStatus: http.StatusOK},
		{name: "negative_disables_cap", maxBytes: -1, bodySize: 10_000_000, wantStatus: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := bodySizeLimitMiddleware(tc.maxBytes, sink)
			srv := httptest.NewServer(h)
			defer srv.Close()

			body := bytes.Repeat([]byte("x"), tc.bodySize)
			resp, err := http.Post(srv.URL, "application/octet-stream", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				respBody, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want %d (body=%q)", resp.StatusCode, tc.wantStatus, string(respBody))
			}
			if tc.wantBody != "" {
				respBody, _ := io.ReadAll(resp.Body)
				if !strings.Contains(string(respBody), tc.wantBody) {
					t.Errorf("body = %q, want substring %q", string(respBody), tc.wantBody)
				}
			}
		})
	}
}

func TestBearerAuthMiddleware(t *testing.T) {
	// Sink writes "ok" so success/failure responses are easy to assert.
	sink := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	const apiKey = "supersecret-32-byte-token-xyz0123"
	h := bearerAuthMiddleware(apiKey, sink)
	srv := httptest.NewServer(h)
	defer srv.Close()

	cases := []struct {
		name       string
		auth       string // value of Authorization header; "" omits the header
		wantStatus int
	}{
		{
			name:       "valid_token",
			auth:       "Bearer " + apiKey,
			wantStatus: http.StatusOK,
		},
		{
			name:       "wrong_token_same_length",
			auth:       "Bearer wrongsecret-32-byte-token-abc987",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong_token_shorter",
			auth:       "Bearer abc",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong_token_much_longer",
			auth:       "Bearer " + strings.Repeat("z", 200),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing_header",
			auth:       "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong_scheme",
			auth:       "Basic " + apiKey,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "bearer_no_space",
			auth:       "Bearer" + apiKey,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

// TestBearerAuthMiddleware_NoLengthLeak — smoke-test that the SHA-256
// path handles widely varying input lengths (0, 1, 4096) without
// crashing and uniformly returns 401. This is not a timing-attack test
// (timing is hardware-dependent and unreliable in CI); the real
// length-leak elimination is enforced by the implementation itself —
// both inputs to ConstantTimeCompare are fixed 32-byte digests, so the
// length-mismatch fast-path in subtle.ConstantTimeCompare cannot fire.
func TestBearerAuthMiddleware_NoLengthLeak(t *testing.T) {
	const apiKey = "expectedkey"
	h := bearerAuthMiddleware(apiKey, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tries := []string{"", "x", strings.Repeat("y", 4096)}
	for _, val := range tries {
		req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
		if val != "" {
			req.Header.Set("Authorization", val)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("auth %q (len=%d): status = %d, want 401", val, len(val), w.Code)
		}
	}
}

func TestBodySizeLimitMiddleware_PassesThroughWhenDisabled(t *testing.T) {
	// When maxBytes <= 0 the middleware returns next unchanged (no wrapping).
	// Confirm Request.Body is the raw body, not a MaxBytesReader.
	probed := false
	sink := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probed = true
		// MaxBytesReader produces a *maxBytesReader type that wraps the body.
		// We can't distinguish it cleanly via the public API, so just confirm
		// a large body is fully readable.
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("expected no read error, got: %v", err)
		}
		if len(buf) != 5_000_000 {
			t.Errorf("read %d bytes, want 5000000", len(buf))
		}
		w.WriteHeader(http.StatusOK)
	})
	h := bodySizeLimitMiddleware(0, sink)
	srv := httptest.NewServer(h)
	defer srv.Close()

	body := bytes.Repeat([]byte("y"), 5_000_000)
	resp, err := http.Post(srv.URL, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if !probed {
		t.Fatal("handler did not run")
	}
}
