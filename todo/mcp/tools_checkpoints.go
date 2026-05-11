package mcp

import (
	"context"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerCheckpointTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("set_checkpoint",
		mcpgo.WithDescription(
			"Create or replace the singleton 'resume here' checkpoint on a task. "+
				"Upsert: each task has at most one checkpoint; calling again replaces it.",
		),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithString("recap", mcpgo.Required(), mcpgo.Description("Short recap of what was worked on (max 10000 chars)"), mcpgo.MaxLength(10000)),
		mcpgo.WithString("next_steps", mcpgo.Required(), mcpgo.Description("How to resume — specific next step queued up (max 10000 chars)"), mcpgo.MaxLength(10000)),
		mcpgo.WithString("open_threads", mcpgo.Description("Optional: deferred or tangential threads worth circling back to (max 10000 chars)"), mcpgo.MaxLength(10000)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		recap, err := requireStr(req, "recap")
		if err != nil {
			return errResult(err), nil
		}
		next, err := requireStr(req, "next_steps")
		if err != nil {
			return errResult(err), nil
		}
		openPtr := getOptStr(req, "open_threads")
		opts := store.SetCheckpointOptions{Recap: recap, NextSteps: next}
		if openPtr != nil {
			opts.OpenThreads = *openPtr
		}
		cp, err := s.SetCheckpoint(ctx, taskID, opts)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(cp)), nil
	})

	srv.AddTool(mcpgo.NewTool("get_checkpoint",
		mcpgo.WithDescription("Get the checkpoint for a task. Returns an error if the task has no checkpoint."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		cp, err := s.GetCheckpoint(ctx, taskID)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(cp)), nil
	})

	srv.AddTool(mcpgo.NewTool("delete_checkpoint",
		mcpgo.WithDescription("Delete a task's checkpoint."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.DeleteCheckpoint(ctx, taskID); err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(map[string]any{"task_id": taskID, "deleted": true})), nil
	})
}
