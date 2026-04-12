package mcp

import (
	"context"
	"fmt"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerLinkTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("add_link",
		mcpgo.WithDescription("Add a link to a task. Returns the created link."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithString("type", mcpgo.Required(), mcpgo.Description("Link type"), mcpgo.Enum("jira", "pr", "url")),
		mcpgo.WithString("url", mcpgo.Required(), mcpgo.Description("URL (max 2000 chars)"), mcpgo.MaxLength(2000)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		linkType := model.LinkType(getStr(req, "type"))
		if !model.ValidLinkTypes[linkType] {
			return errResult(fmt.Errorf("invalid link type %q (valid: jira, pr, url)", linkType)), nil
		}
		url, err := requireStr(req, "url")
		if err != nil {
			return errResult(err), nil
		}
		link, err := s.AddLink(ctx, taskID, linkType, url)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(link)), nil
	})

	srv.AddTool(mcpgo.NewTool("list_links",
		mcpgo.WithDescription("List all links for a task. Returns an array of links."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		links, err := s.ListLinks(ctx, taskID)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(links)), nil
	})

	srv.AddTool(mcpgo.NewTool("delete_link",
		mcpgo.WithDescription("Delete a link"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithNumber("link_id", mcpgo.Required(), mcpgo.Description("Link ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		linkID, err := requireUint(req, "link_id")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.DeleteLink(ctx, taskID, linkID); err != nil {
			return errResult(err), nil
		}
		return textResult("deleted"), nil
	})
}
