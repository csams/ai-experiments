package cmd

import (
	"bytes"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/csams/todo/config"
	"github.com/csams/todo/store"
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
	h := bearerAuthMiddleware(map[string]string{"default": apiKey}, sink)
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
	h := bearerAuthMiddleware(map[string]string{"default": apiKey}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestValidateMCPHTTPConfig(t *testing.T) {
	longKey := strings.Repeat("a", minAPIKeyLength)
	shortKey := strings.Repeat("a", minAPIKeyLength-1)

	cases := []struct {
		name     string
		cfg      config.MCPConfig
		insecure bool
		wantErr  string // substring; "" means expect nil error
	}{
		{
			name:    "no_api_key_no_tls_is_fine",
			cfg:     config.MCPConfig{},
			wantErr: "",
		},
		{
			name:    "long_key_with_tls",
			cfg:     config.MCPConfig{APIKey: longKey, TLSCert: "/x"},
			wantErr: "",
		},
		{
			name:     "long_key_no_tls_with_insecure",
			cfg:      config.MCPConfig{APIKey: longKey},
			insecure: true,
			wantErr:  "",
		},
		{
			name:    "long_key_no_tls_rejected",
			cfg:     config.MCPConfig{APIKey: longKey},
			wantErr: "API key auth requires TLS",
		},
		{
			name:    "short_key_with_tls_rejected",
			cfg:     config.MCPConfig{APIKey: shortKey, TLSCert: "/x"},
			wantErr: "must be at least",
		},
		{
			name:     "short_key_with_insecure_still_rejected",
			cfg:      config.MCPConfig{APIKey: shortKey},
			insecure: true,
			wantErr:  "must be at least",
		},
		{
			name:    "exactly_min_length_accepted",
			cfg:     config.MCPConfig{APIKey: longKey, TLSCert: "/x"},
			wantErr: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateMCPHTTPConfig(tc.cfg, tc.insecure)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestValidateMCPHTTPConfig_TLSPrecedesLengthCheck pins the message
// priority: when both checks would fail (short key, no TLS, no
// --insecure), the TLS error is the one the user sees first. The
// reasoning is that fixing TLS often involves regenerating the key
// anyway, and "auth without TLS" is the louder operational concern.
func TestValidateMCPHTTPConfig_TLSPrecedesLengthCheck(t *testing.T) {
	cfg := config.MCPConfig{APIKey: "shortkey"} // no TLS, short key
	_, err := validateMCPHTTPConfig(cfg, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "API key auth requires TLS") {
		t.Errorf("expected TLS error first, got %q", err.Error())
	}
}

func TestValidateMCPHTTPConfig_ResolvesAPIKeys(t *testing.T) {
	// Confirm the resolved map shape for the three supported config
	// forms.
	longKey := strings.Repeat("a", minAPIKeyLength)

	t.Run("api_key_legacy_resolves_to_default_label", func(t *testing.T) {
		keys, err := validateMCPHTTPConfig(config.MCPConfig{
			APIKey: longKey, TLSCert: "/x",
		}, false)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if got, want := keys, map[string]string{"default": longKey}; !reflect.DeepEqual(got, want) {
			t.Errorf("resolved keys = %v, want %v", got, want)
		}
	})

	t.Run("api_keys_passes_through", func(t *testing.T) {
		input := map[string]string{"alice": longKey, "bob": longKey + "b"}
		keys, err := validateMCPHTTPConfig(config.MCPConfig{
			APIKeys: input, TLSCert: "/x",
		}, false)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if !reflect.DeepEqual(keys, input) {
			t.Errorf("resolved keys = %v, want %v", keys, input)
		}
	})

	t.Run("both_set_rejected", func(t *testing.T) {
		_, err := validateMCPHTTPConfig(config.MCPConfig{
			APIKey:  longKey,
			APIKeys: map[string]string{"alice": longKey},
			TLSCert: "/x",
		}, false)
		if err == nil {
			t.Fatal("expected error for setting both api_key and api_keys")
		}
		if !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("error = %q, want substring 'mutually exclusive'", err.Error())
		}
	})

	t.Run("any_short_key_in_map_rejects", func(t *testing.T) {
		_, err := validateMCPHTTPConfig(config.MCPConfig{
			APIKeys: map[string]string{"alice": longKey, "bob": "short"},
			TLSCert: "/x",
		}, false)
		if err == nil {
			t.Fatal("expected error for short key in map")
		}
		if !strings.Contains(err.Error(), `"bob"`) {
			t.Errorf("expected error to name 'bob', got %q", err.Error())
		}
	})
}

func TestBearerAuthMiddleware_MultiKeyStampsActor(t *testing.T) {
	var seenActor string
	sink := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenActor = store.ActorFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	keys := map[string]string{
		"alice": "alice-api-key-very-long-string",
		"bob":   "bob-api-key-also-very-long-yes",
	}
	h := bearerAuthMiddleware(keys, sink)

	cases := []struct {
		name      string
		token     string
		wantCode  int
		wantActor string
	}{
		{"alice_token", "alice-api-key-very-long-string", http.StatusOK, "alice"},
		{"bob_token", "bob-api-key-also-very-long-yes", http.StatusOK, "bob"},
		{"unknown_token", "nope", http.StatusUnauthorized, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seenActor = ""
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tc.wantCode)
			}
			if seenActor != tc.wantActor {
				t.Errorf("actor = %q, want %q", seenActor, tc.wantActor)
			}
		})
	}
}

func TestBearerAuthMiddleware_EmptyKeysRejectsAll(t *testing.T) {
	// With no configured keys, every request should be unauthorized —
	// the empty-map case is a misconfiguration (the call site should
	// register a different middleware), but the bearer middleware must
	// fail closed.
	sink := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := bearerAuthMiddleware(map[string]string{}, sink)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (empty keys must fail closed)", w.Code, http.StatusUnauthorized)
	}
}

func TestMCPGenKey_OutputsHexEncodedRandomKey(t *testing.T) {
	// Capture the command's stdout via cobra's SetOut.
	var buf bytes.Buffer
	mcpGenKeyCmd.SetOut(&buf)
	t.Cleanup(func() { mcpGenKeyCmd.SetOut(nil) })

	if err := mcpGenKeyCmd.RunE(mcpGenKeyCmd, nil); err != nil {
		t.Fatalf("gen-key: %v", err)
	}

	out := strings.TrimSpace(buf.String())
	if len(out) != 64 {
		t.Errorf("output length = %d, want 64 hex chars (32 bytes)", len(out))
	}
	if _, err := hex.DecodeString(out); err != nil {
		t.Errorf("output is not valid hex: %v (got %q)", err, out)
	}

	// Comfortably above the production min-length check.
	if len(out) < minAPIKeyLength {
		t.Errorf("gen-key output (%d chars) is below minAPIKeyLength (%d) — gen-key must always produce a passing key",
			len(out), minAPIKeyLength)
	}
}

func TestMCPGenKey_ProducesDistinctKeys(t *testing.T) {
	// crypto/rand should never produce two identical 32-byte sequences
	// in a row. Pin it so a future refactor that accidentally seeds
	// math/rand (or memoizes the output) is caught.
	run := func(t *testing.T) string {
		t.Helper()
		var buf bytes.Buffer
		mcpGenKeyCmd.SetOut(&buf)
		if err := mcpGenKeyCmd.RunE(mcpGenKeyCmd, nil); err != nil {
			t.Fatalf("gen-key: %v", err)
		}
		mcpGenKeyCmd.SetOut(nil)
		return strings.TrimSpace(buf.String())
	}
	a, b := run(t), run(t)
	if a == b {
		t.Errorf("gen-key produced identical output on two calls: %q", a)
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
