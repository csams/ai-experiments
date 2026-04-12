package mcp

import (
	"context"
	"fmt"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const maxBulkMCPIDs = 100

func registerBulkTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("bulk_update_state",
		mcpgo.WithDescription("Set state on multiple tasks (max 100) atomically. Processes in ID order. Done cascades unblocks. Returns updated tasks."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithString("state", mcpgo.Required(), mcpgo.Description("Target state"), mcpgo.Enum("New", "Progressing", "Unblocked", "Done")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		ids, err := requireUintSlice(req, "ids")
		if err != nil {
			return errResult(err), nil
		}
		if len(ids) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("ids: max %d per call", maxBulkMCPIDs)), nil
		}
		state, err := getState(req, "state")
		if err != nil {
			return errResult(err), nil
		}
		tasks, err := s.BulkUpdateState(ctx, ids, state)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	srv.AddTool(mcpgo.NewTool("bulk_update_priority",
		mcpgo.WithDescription("Set priority on multiple tasks (max 100) atomically. Blockers are promoted to at least match the priority of tasks they block. Returns updated tasks."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithNumber("priority", mcpgo.Required(), mcpgo.Description("Target priority")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		ids, err := requireUintSlice(req, "ids")
		if err != nil {
			return errResult(err), nil
		}
		if len(ids) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("ids: max %d per call", maxBulkMCPIDs)), nil
		}
		tasks, err := s.BulkUpdatePriority(ctx, ids, getInt(req, "priority"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	srv.AddTool(mcpgo.NewTool("bulk_add_tags",
		mcpgo.WithDescription("Add tags to multiple tasks (max 100) atomically."),
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

	srv.AddTool(mcpgo.NewTool("bulk_remove_tags",
		mcpgo.WithDescription("Remove tags from multiple tasks (max 100) atomically."),
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
