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
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithArray("tags", mcpgo.Required(), mcpgo.Description("Tags to add (alphanumeric/hyphens/underscores only, max 100 chars each)"), mcpgo.WithStringItems(mcpgo.MaxLength(100)), mcpgo.MaxItems(50)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		tags, err := requireStrSlice(req, "tags")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.AddTags(ctx, taskID, tags); err != nil {
			return errResult(err), nil
		}
		return textResult("tags added"), nil
	})

	srv.AddTool(mcpgo.NewTool("remove_tags",
		mcpgo.WithDescription("Remove tags from a task. No error if tag doesn't exist."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithArray("tags", mcpgo.Required(), mcpgo.Description("Tags to remove (alphanumeric/hyphens/underscores only, max 100 chars each)"), mcpgo.WithStringItems(mcpgo.MaxLength(100)), mcpgo.MaxItems(50)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		tags, err := requireStrSlice(req, "tags")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.RemoveTags(ctx, taskID, tags); err != nil {
			return errResult(err), nil
		}
		return textResult("tags removed"), nil
	})
}
