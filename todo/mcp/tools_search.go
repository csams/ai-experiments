package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerSemanticTools(srv *server.MCPServer, ss store.SemanticSearcher) {
	srv.AddTool(mcpgo.NewTool("semantic_search",
		mcpgo.WithDescription("Semantic search across tasks and notes using vector similarity. Returns ranked results with scores."),
		mcpgo.WithString("query", mcpgo.Required(), mcpgo.Description("Natural language search query"), mcpgo.MaxLength(500)),
		mcpgo.WithString("type", mcpgo.Description("Filter by type"), mcpgo.Enum("task", "note")),
		mcpgo.WithNumber("task_id", mcpgo.Description("Filter to a specific task's entities"), mcpgo.Min(1)),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default 10, max 100)")),
		mcpgo.WithBoolean("include_archived", mcpgo.Description("Include archived tasks/notes (default false)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		query, err := requireStr(req, "query")
		if err != nil {
			return errResult(err), nil
		}
		limit := getInt(req, "limit")
		if limit < 0 {
			limit = 0
		}
		if limit > 100 {
			limit = 100
		}
		opts := store.SemanticSearchOptions{
			Limit:           limit,
			Type:            strings.ToLower(getStr(req, "type")),
			IncludeArchived: getBool(req, "include_archived"),
		}
		if tid := getUint(req, "task_id"); tid > 0 {
			opts.TaskID = &tid
		}
		results, err := ss.SemanticSearch(ctx, query, opts)
		if err != nil {
			slog.Error("semantic search failed", "error", err)
			return errResult(fmt.Errorf("semantic search failed: %w", err)), nil
		}
		return textResult(toJSON(results)), nil
	})

	srv.AddTool(mcpgo.NewTool("semantic_search_context",
		mcpgo.WithDescription("Find tasks and notes semantically related to a given task. Aggregates task text + notes, searches excluding the source task's own documents."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID to find related items for"), mcpgo.Min(1)),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default 10, max 100)")),
		mcpgo.WithString("type", mcpgo.Description("Filter by type"), mcpgo.Enum("task", "note")),
		mcpgo.WithBoolean("include_archived", mcpgo.Description("Include archived tasks/notes (default false)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		limit := getInt(req, "limit")
		if limit < 0 {
			limit = 0
		}
		if limit > 100 {
			limit = 100
		}
		results, err := ss.SemanticSearchContext(ctx, taskID, store.SemanticSearchOptions{
			Limit:           limit,
			Type:            strings.ToLower(getStr(req, "type")),
			IncludeArchived: getBool(req, "include_archived"),
		})
		if err != nil {
			slog.Error("semantic search context failed", "error", err, "task_id", taskID)
			return errResult(fmt.Errorf("semantic search failed: %w", err)), nil
		}
		return textResult(toJSON(results)), nil
	})
}
