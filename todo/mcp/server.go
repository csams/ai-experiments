package mcp

import (
	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewServer creates an MCP server with all tools registered.
// ss may be nil if semantic search is not configured.
func NewServer(s store.Store, ss store.SemanticSearcher) *server.MCPServer {
	srv := server.NewMCPServer(
		"todo",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithInstructions("Task tracking system. Use tools to create, manage, and search tasks with priorities, states, blocking relationships, subtask hierarchies, notes, links, and tags."),
	)

	registerTaskTools(srv, s)
	registerNoteTools(srv, s)
	registerLinkTools(srv, s)
	registerTagTools(srv, s)
	registerBulkTools(srv, s)
	registerSearchTools(srv, s)

	if ss != nil {
		registerSemanticTools(srv, ss)
	}

	return srv
}

// helper to build a text result
func textResult(text string) *mcpgo.CallToolResult {
	return &mcpgo.CallToolResult{
		Content: []mcpgo.Content{
			mcpgo.NewTextContent(text),
		},
	}
}

// helper to build an error result
func errResult(err error) *mcpgo.CallToolResult {
	return &mcpgo.CallToolResult{
		Content: []mcpgo.Content{
			mcpgo.NewTextContent(err.Error()),
		},
		IsError: true,
	}
}
