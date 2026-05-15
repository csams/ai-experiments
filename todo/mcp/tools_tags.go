package mcp

import (
	"context"
	"fmt"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTagTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("add_tags",
		mcpgo.WithDescription("Add tags to one or more tasks (1..100 IDs). Idempotent — existing tags are not duplicated. Atomic across the entire array."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithArray("tags", mcpgo.Required(), mcpgo.Description("Tags to add (alphanumeric/hyphens/underscores only, max 100 chars each)"), mcpgo.WithStringItems(mcpgo.MaxLength(100)), mcpgo.MaxItems(50)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		ids, err := requireUintSlice(req, "ids")
		if err != nil {
			return errResult(err), nil
		}
		if len(ids) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("ids: max %d per call", maxBulkMCPIDs)), nil
		}
		tags, err := requireStrSlice(req, "tags")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.BulkAddTags(ctx, ids, tags); err != nil {
			return errResult(err), nil
		}
		return textResult("tags added"), nil
	})

	srv.AddTool(mcpgo.NewTool("remove_tags",
		mcpgo.WithDescription("Remove tags from one or more tasks (1..100 IDs). No error if a tag isn't present on a task. Atomic across the entire array."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithArray("tags", mcpgo.Required(), mcpgo.Description("Tags to remove (alphanumeric/hyphens/underscores only, max 100 chars each)"), mcpgo.WithStringItems(mcpgo.MaxLength(100)), mcpgo.MaxItems(50)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		ids, err := requireUintSlice(req, "ids")
		if err != nil {
			return errResult(err), nil
		}
		if len(ids) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("ids: max %d per call", maxBulkMCPIDs)), nil
		}
		tags, err := requireStrSlice(req, "tags")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.BulkRemoveTags(ctx, ids, tags); err != nil {
			return errResult(err), nil
		}
		return textResult("tags removed"), nil
	})
}
