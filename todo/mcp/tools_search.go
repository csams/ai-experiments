package mcp

import (
	"context"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerSearchTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("search_tasks",
		mcpgo.WithDescription("Search tasks by title and description (case-insensitive substring match)"),
		mcpgo.WithString("query", mcpgo.Required(), mcpgo.Description("Search query")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		tasks, err := s.SearchTasks(getStr(req, "query"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	srv.AddTool(mcpgo.NewTool("search_notes",
		mcpgo.WithDescription("Search notes by text content (case-insensitive). Returns notes with task IDs."),
		mcpgo.WithString("query", mcpgo.Required(), mcpgo.Description("Search query")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		notes, err := s.SearchNotes(getStr(req, "query"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(notes)), nil
	})
}

func registerSemanticTools(srv *server.MCPServer, ss store.SemanticSearcher) {
	srv.AddTool(mcpgo.NewTool("semantic_search",
		mcpgo.WithDescription("Semantic search across tasks and notes using vector similarity. Returns ranked results with scores."),
		mcpgo.WithString("query", mcpgo.Required(), mcpgo.Description("Natural language search query")),
		mcpgo.WithString("type", mcpgo.Description("Filter by type: task, note")),
		mcpgo.WithNumber("task_id", mcpgo.Description("Filter to a specific task's entities")),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default 10)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		opts := store.SemanticSearchOptions{
			Limit: getInt(req, "limit"),
			Type:  getStr(req, "type"),
		}
		if tid := getUint(req, "task_id"); tid > 0 {
			opts.TaskID = &tid
		}
		results, err := ss.SemanticSearch(ctx, getStr(req, "query"), opts)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(results)), nil
	})

	srv.AddTool(mcpgo.NewTool("semantic_search_context",
		mcpgo.WithDescription("Find tasks and notes semantically related to a given task. Aggregates task text + notes, searches excluding the source task's own documents."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID to find related items for")),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default 10)")),
		mcpgo.WithString("type", mcpgo.Description("Filter by type: task, note")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		results, err := ss.SemanticSearchContext(ctx, getUint(req, "task_id"), store.SemanticSearchOptions{
			Limit: getInt(req, "limit"),
			Type:  getStr(req, "type"),
		})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(results)), nil
	})
}
