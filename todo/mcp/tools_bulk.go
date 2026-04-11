package mcp

import (
	"context"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerBulkTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("bulk_update_state",
		mcpgo.WithDescription("Set state on multiple tasks (max 100) atomically. Processes in ID order. Done cascades unblocks."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs")),
		mcpgo.WithString("state", mcpgo.Required(), mcpgo.Description("Target state"), mcpgo.Enum("New", "Progressing", "Unblocked", "Done")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		state := model.TaskState(getStr(req, "state"))
		tasks, err := s.BulkUpdateState(getUintSlice(req, "ids"), state)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	srv.AddTool(mcpgo.NewTool("bulk_update_priority",
		mcpgo.WithDescription("Set priority on multiple tasks (max 100) atomically with propagation."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs")),
		mcpgo.WithNumber("priority", mcpgo.Required(), mcpgo.Description("Target priority")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		tasks, err := s.BulkUpdatePriority(getUintSlice(req, "ids"), getInt(req, "priority"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	srv.AddTool(mcpgo.NewTool("bulk_add_tags",
		mcpgo.WithDescription("Add tags to multiple tasks (max 100) atomically."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs")),
		mcpgo.WithArray("tags", mcpgo.Required(), mcpgo.Description("Tags to add"), mcpgo.WithStringItems()),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.BulkAddTags(getUintSlice(req, "ids"), getStrSlice(req, "tags")); err != nil {
			return errResult(err), nil
		}
		return textResult("tags added"), nil
	})

	srv.AddTool(mcpgo.NewTool("bulk_remove_tags",
		mcpgo.WithDescription("Remove tags from multiple tasks (max 100) atomically."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs")),
		mcpgo.WithArray("tags", mcpgo.Required(), mcpgo.Description("Tags to remove"), mcpgo.WithStringItems()),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.BulkRemoveTags(getUintSlice(req, "ids"), getStrSlice(req, "tags")); err != nil {
			return errResult(err), nil
		}
		return textResult("tags removed"), nil
	})
}
