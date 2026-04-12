package cmd

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	todomcp "github.com/csams/todo/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

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
			if cfg.MCP.APIKey != "" && cfg.MCP.TLSCert == "" && !insecure {
				return fmt.Errorf("API key auth requires TLS (set tls_cert/tls_key in config or via TODO_MCP_TLS_CERT/TODO_MCP_TLS_KEY env vars, or use --insecure for development)")
			}
			if cfg.MCP.APIKey != "" && cfg.MCP.TLSCert == "" && insecure {
				logger.Warn("MCP API key auth enabled without TLS; tokens sent in cleartext (--insecure)")
			}

			if cfg.MCP.APIKey == "" {
				logger.Warn("MCP HTTP server starting without authentication; all clients have full access")
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

			if cfg.MCP.APIKey != "" {
				mux.Handle("/mcp", bearerAuthMiddleware(cfg.MCP.APIKey, inner))
			} else {
				mux.Handle("/mcp", inner)
			}

			opts = append(opts, server.WithStreamableHTTPServer(&http.Server{
				Handler:           mux,
				ReadHeaderTimeout: 10 * time.Second,
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

func bearerAuthMiddleware(apiKey string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + apiKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(auth, expected) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func init() {
	mcpCmd.Flags().String("transport", "", "transport: stdio (default) or http")
	mcpCmd.Flags().String("addr", "", "listen address for HTTP transport (default from config)")
	mcpCmd.Flags().Bool("insecure", false, "allow API key auth without TLS (development only)")
	rootCmd.AddCommand(mcpCmd)
}
