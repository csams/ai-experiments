package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/csams/todo/config"
	todomcp "github.com/csams/todo/mcp"
	"github.com/csams/todo/store"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// minAPIKeyLength is the floor we enforce on cfg.MCP.APIKey when the
// HTTP transport is in use. 20 ASCII characters carries ~119 bits of
// entropy if generated from a typical random alphabet (more if hex);
// well above any practical brute-force threshold and short enough that
// a hand-typed key for a small private deployment isn't onerous.
const minAPIKeyLength = 20

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server for AI agent access",
	Long: `Start an MCP (Model Context Protocol) server that exposes all task tracking
tools to AI agents like Claude.

Supports stdio transport (default, for Claude Code / Claude Desktop) and
HTTP streamable transport (for remote / multi-client access).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, gs, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		// Start the vector-sync reconciler if vector sync is configured.
		// The reconciler is gated to long-lived processes (the MCP
		// server) — short-lived CLI commands don't run it; dirty rows
		// persist in the DB until the next MCP-server tick.
		//
		// Deferred-in-reverse: StopReconciler runs BEFORE s.Close so
		// the reconciler can't issue store reads against a closed DB.
		if syncer := getVectorSyncer(); syncer != nil {
			syncer.StartReconciler(cmd.Context(), cfg.Vector.ReconcileInterval, cfg.Vector.ReconcileBatchSize)
			defer func() {
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				syncer.StopReconciler(stopCtx)
			}()
		}

		// Set source for audit logging
		transport, _ := cmd.Flags().GetString("transport")
		switch transport {
		case "http":
			gs.SetSource("mcp-http")
		default:
			gs.SetSource("mcp-stdio")
		}

		// Create MCP server with all tools
		mcpServer := todomcp.NewServer(s, getSemanticSearcher())

		switch transport {
		case "http":
			addr, _ := cmd.Flags().GetString("addr")
			if addr == "" {
				addr = cfg.MCP.Addr
			}

			insecure, _ := cmd.Flags().GetBool("insecure")
			apiKeys, err := validateMCPHTTPConfig(cfg.MCP, insecure)
			if err != nil {
				return err
			}
			if len(apiKeys) == 0 {
				logger.Warn("MCP HTTP server starting without authentication; all clients have full access")
			} else {
				if cfg.MCP.TLSCert == "" && insecure {
					logger.Warn("MCP API key auth enabled without TLS; tokens sent in cleartext (--insecure)")
				}
				if cfg.MCP.APIKey != "" {
					// Legacy single-tenant form. Audit events will
					// attribute every action to actor="default".
					logger.Info("MCP using legacy single-tenant api_key (actor=default); consider moving to api_keys for per-client attribution")
				} else {
					logger.Info("MCP multi-tenant auth enabled", "key_count", len(apiKeys))
				}
			}

			var handler http.Handler
			var opts []server.StreamableHTTPOption

			if cfg.MCP.TLSCert != "" {
				opts = append(opts, server.WithTLSCert(cfg.MCP.TLSCert, cfg.MCP.TLSKey))
			}

			mux := http.NewServeMux()
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r)
			})

			// Bound the request body before any downstream handler reads it.
			// mcp-go's streamable HTTP handler does io.ReadAll(r.Body) without
			// its own cap, so an unbounded body would force unbounded buffering.
			capped := bodySizeLimitMiddleware(cfg.MCP.MaxBodyBytes, inner)

			if len(apiKeys) > 0 {
				mux.Handle("/mcp", bearerAuthMiddleware(apiKeys, capped))
			} else {
				mux.Handle("/mcp", capped)
			}

			opts = append(opts, server.WithStreamableHTTPServer(&http.Server{
				Handler:           mux,
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       cfg.MCP.ReadTimeout,
				WriteTimeout:      cfg.MCP.WriteTimeout,
				IdleTimeout:       120 * time.Second,
				MaxHeaderBytes:    1 << 20, // 1MB
			}))

			httpServer := server.NewStreamableHTTPServer(mcpServer, opts...)
			handler = httpServer

			// Graceful shutdown: signal triggers httpServer.Shutdown, which
			// causes httpServer.Start to return, then defer s.Close runs.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				logger.Info("shutting down MCP HTTP server")
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				httpServer.Shutdown(shutdownCtx)
			}()

			logger.Info("starting MCP HTTP server", "addr", addr)
			fmt.Fprintf(os.Stderr, "MCP HTTP server listening on %s\n", addr)
			return httpServer.Start(addr)

		default: // stdio
			// ServeStdio handles SIGINT/SIGTERM internally — it cancels its
			// context and Listen returns. Then defer s.Close runs naturally.
			return server.ServeStdio(mcpServer)
		}
	},
}

// validateMCPHTTPConfig enforces startup-time invariants on the HTTP
// transport's config and returns the resolved label→key map for the
// caller to feed bearerAuthMiddleware.
//
// Rules:
//   - api_key and api_keys cannot both be set (mutually exclusive
//     forms; ResolveAPIKeys returns an error for the caller to surface).
//   - Any keys configured without TLS: refuse unless --insecure
//     (developer override).
//   - Every key shorter than minAPIKeyLength: refuse regardless of
//     --insecure. Weak credentials are weak credentials; the dev/prod
//     transport choice is independent. Use `todo mcp gen-key` to
//     generate strong keys.
//   - When api_keys is set, every value is validated. A single weak
//     key in a multi-tenant config rejects startup.
//
// A nil-or-empty return map means "no auth configured" — the caller
// registers a different middleware (or none) for that path.
//
// Warnings (cleartext-on-insecure, no-auth-at-all, single-tenant
// deprecation hint) live at the call site so they fire after this
// check passes.
func validateMCPHTTPConfig(c config.MCPConfig, insecure bool) (map[string]string, error) {
	keys, err := c.ResolveAPIKeys()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	if c.TLSCert == "" && !insecure {
		return nil, fmt.Errorf("API key auth requires TLS (set tls_cert/tls_key in config or via TODO_MCP_TLS_CERT/TODO_MCP_TLS_KEY env vars, or use --insecure for development)")
	}
	// Length-check every key. Sort labels so the error message is
	// stable across runs (map iteration would otherwise pick a random
	// "first" violator).
	labels := make([]string, 0, len(keys))
	for label := range keys {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		if len(keys[label]) < minAPIKeyLength {
			return nil, fmt.Errorf("MCP API key %q must be at least %d characters (got %d); generate a strong key with `todo mcp gen-key`",
				label, minAPIKeyLength, len(keys[label]))
		}
	}
	return keys, nil
}

// mcpGenKeyCmd prints a freshly-generated random API key suitable for
// dropping into config (mcp.api_key) or the TODO_MCP_API_KEY env var.
// 32 bytes of randomness rendered as hex = 64 characters of high-
// entropy ASCII, comfortably above minAPIKeyLength.
var mcpGenKeyCmd = &cobra.Command{
	Use:   "gen-key",
	Short: "Print a fresh 256-bit random API key (hex-encoded)",
	Long: `Print a fresh 256-bit random API key.

The output is 64 hex characters drawn from crypto/rand. Pipe it into
your config or environment, e.g.

    TODO_MCP_API_KEY=$(todo mcp gen-key) ./todo mcp --transport http`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var key [32]byte
		if _, err := rand.Read(key[:]); err != nil {
			return fmt.Errorf("generate random key: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), hex.EncodeToString(key[:]))
		return nil
	},
}

// bodySizeLimitMiddleware caps the request body to maxBytes. A maxBytes <= 0
// disables the cap (matches the http.Server convention of "0 = unlimited").
// When the limit is exceeded, downstream io.ReadAll returns *http.MaxBytesError,
// which the mcp-go handler surfaces as a JSON-RPC parse error to the client.
func bodySizeLimitMiddleware(maxBytes int64, next http.Handler) http.Handler {
	if maxBytes <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

// bearerAuthMiddleware checks the Authorization header against the
// configured API keys in constant time, then stamps the matched key's
// label onto the request context via store.SetActorContext so audit
// events emitted downstream carry that identity.
//
// Hashing both sides with SHA-256 before comparison equalizes input
// lengths so subtle.ConstantTimeCompare's length-mismatch fast-path
// can't fire and leak the expected length. With multiple keys, the
// loop iterates ALL configured keys on every request (never short-
// circuits on first match) so total work doesn't reveal which key
// matched. With N keys the per-request cost is N constant-time
// 32-byte compares — trivial up to thousands of keys.
//
// SHA-256 is the right primitive: the API keys are high-entropy
// (gen-key produces 256-bit random tokens). A password hash (bcrypt /
// argon2) would be wrong — it's tuned to stretch low-entropy passwords,
// not for one-shot equality checks.
//
// The keys map is empty → caller should not register this middleware
// (the no-auth code path runs instead). A zero-length map at runtime
// rejects every request.
func bearerAuthMiddleware(keys map[string]string, next http.Handler) http.Handler {
	// Pre-hash each configured key once at startup. Sort labels so
	// iteration order is deterministic regardless of map randomization,
	// which makes tests stable; the security guarantee depends only on
	// running the full loop, not on order.
	labels := make([]string, 0, len(keys))
	for label := range keys {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	type entry struct {
		label string
		hash  [32]byte
	}
	entries := make([]entry, len(labels))
	for i, label := range labels {
		entries[i] = entry{label: label, hash: sha256.Sum256([]byte("Bearer " + keys[label]))}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		matched := ""
		for _, e := range entries {
			// Intentionally NOT short-circuiting on first match: every
			// request runs N comparisons so timing can't distinguish
			// which key matched. ConstantTimeCompare returns 1 on hit
			// and 0 on miss; we just remember the last hit (there
			// should be at most one — duplicate keys across labels are
			// a misconfiguration the caller can audit at startup).
			if subtle.ConstantTimeCompare(got[:], e.hash[:]) == 1 {
				matched = e.label
			}
		}
		if matched == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(store.SetActorContext(r.Context(), matched)))
	})
}

func init() {
	mcpCmd.Flags().String("transport", "", "transport: stdio (default) or http")
	mcpCmd.Flags().String("addr", "", "listen address for HTTP transport (default from config)")
	mcpCmd.Flags().Bool("insecure", false, "allow API key auth without TLS (development only)")
	mcpCmd.AddCommand(mcpGenKeyCmd)
	rootCmd.AddCommand(mcpCmd)
}
