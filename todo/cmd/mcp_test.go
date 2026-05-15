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
