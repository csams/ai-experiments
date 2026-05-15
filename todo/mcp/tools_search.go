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
		mcpgo.WithDescription("Semantic search across tasks and notes using vector similarity. "+
			"Exactly one of `query` or `related_to_task_id` must be provided:\n"+
			"  - `query`: natural-language search; results scored against the query text.\n"+
			"  - `related_to_task_id`: context search; the source task's text and notes are "+
			"aggregated as the query, and the source task and its attached notes are excluded "+
			"from results.\n"+
			"Long descriptions and notes are chunked; each result groups all matching chunks "+
			"under one parent task or note (Chunks[] is sorted by score, Score is the best "+
			"chunk's score). `task_id` (scope filter) is ignored in context mode."),
		mcpgo.WithString("query", mcpgo.Description("Natural-language search query. Mutually exclusive with related_to_task_id."), mcpgo.MaxLength(500)),
		mcpgo.WithNumber("related_to_task_id", mcpgo.Description("Source task for context search. Mutually exclusive with query."), mcpgo.Min(1)),
		mcpgo.WithString("type", mcpgo.Description("Filter by type"), mcpgo.Enum("task", "note")),
		mcpgo.WithNumber("task_id", mcpgo.Description("Scope filter: restrict results to entities tied to this task (query mode only; ignored in context mode)."), mcpgo.Min(1)),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default 10, max 100)")),
		mcpgo.WithBoolean("include_archived", mcpgo.Description("Include archived tasks/notes (default false)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		query := getStr(req, "query")
		relatedID := getOptUint(req, "related_to_task_id")
		hasQuery := query != ""
		hasRelated := relatedID != nil
		if hasQuery == hasRelated {
			return errResult(fmt.Errorf("exactly one of query or related_to_task_id must be set")), nil
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

		if hasRelated {
			// Context mode. Scope task_id is ignored (the context path does not
			// accept a separate scope filter — results are derived from the
			// source task's content and the source is excluded).
			results, err := ss.SemanticSearchContext(ctx, *relatedID, opts)
			if err != nil {
				slog.Error("semantic search context failed", "error", err, "task_id", *relatedID)
				return errResult(fmt.Errorf("semantic search failed: %w", err)), nil
			}
			return textResult(toJSON(results)), nil
		}

		// Query mode.
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
}
