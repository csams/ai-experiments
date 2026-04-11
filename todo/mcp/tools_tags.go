package mcp

import (
	"context"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTagTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("add_tags",
		mcpgo.WithDescription("Add tags to a task. Idempotent — existing tags are not duplicated."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithArray("tags", mcpgo.Required(), mcpgo.Description("Tags to add"), mcpgo.WithStringItems()),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.AddTags(getUint(req, "task_id"), getStrSlice(req, "tags")); err != nil {
			return errResult(err), nil
		}
		return textResult("tags added"), nil
	})

	srv.AddTool(mcpgo.NewTool("remove_tags",
		mcpgo.WithDescription("Remove tags from a task. No error if tag doesn't exist."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithArray("tags", mcpgo.Required(), mcpgo.Description("Tags to remove"), mcpgo.WithStringItems()),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.RemoveTags(getUint(req, "task_id"), getStrSlice(req, "tags")); err != nil {
			return errResult(err), nil
		}
		return textResult("tags removed"), nil
	})
}
