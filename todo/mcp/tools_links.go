package mcp

import (
	"context"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerLinkTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("add_link",
		mcpgo.WithDescription("Add a JIRA/PR/URL link to a task"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithString("type", mcpgo.Required(), mcpgo.Description("Link type"), mcpgo.Enum("jira", "pr", "url")),
		mcpgo.WithString("url", mcpgo.Required(), mcpgo.Description("Link URL or JIRA issue ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		link, err := s.AddLink(getUint(req, "task_id"), model.LinkType(getStr(req, "type")), getStr(req, "url"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(link)), nil
	})

	srv.AddTool(mcpgo.NewTool("list_links",
		mcpgo.WithDescription("List links for a task"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		links, err := s.ListLinks(getUint(req, "task_id"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(links)), nil
	})

	srv.AddTool(mcpgo.NewTool("delete_link",
		mcpgo.WithDescription("Delete a link"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithNumber("link_id", mcpgo.Required(), mcpgo.Description("Link ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.DeleteLink(getUint(req, "task_id"), getUint(req, "link_id")); err != nil {
			return errResult(err), nil
		}
		return textResult("deleted"), nil
	})
}
