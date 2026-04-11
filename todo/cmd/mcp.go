package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
		defer s.Close()

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

		// Graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		switch transport {
		case "http":
			addr, _ := cmd.Flags().GetString("addr")
			if addr == "" {
				addr = cfg.MCP.Addr
			}

			httpServer := server.NewStreamableHTTPServer(mcpServer)

			go func() {
				<-sigCh
				logger.Info("shutting down MCP HTTP server")
				s.Close()
				os.Exit(0)
			}()

			logger.Info("starting MCP HTTP server", "addr", addr)
			fmt.Fprintf(os.Stderr, "MCP HTTP server listening on %s\n", addr)
			return httpServer.Start(addr)

		default: // stdio
			go func() {
				<-sigCh
				logger.Info("shutting down MCP stdio server")
				s.Close()
				os.Exit(0)
			}()

			return server.ServeStdio(mcpServer)
		}
	},
}

func init() {
	mcpCmd.Flags().String("transport", "", "transport: stdio (default) or http")
	mcpCmd.Flags().String("addr", "", "listen address for HTTP transport (default from config)")
	rootCmd.AddCommand(mcpCmd)
}
