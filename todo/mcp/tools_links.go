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
		mcpgo.WithString("description", mcpgo.Description("Optional human-readable description (max 1000 chars)"), mcpgo.MaxLength(1000)),
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
		description := getStr(req, "description")
		link, err := s.AddLink(ctx, taskID, linkType, url, description)
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

	srv.AddTool(mcpgo.NewTool("update_link",
		mcpgo.WithDescription("Update a link's type, url, and/or description. "+
			"Omit a field to leave it unchanged. "+
			"`description: \"\"` explicitly clears the description. "+
			"`type` and `url` cannot be cleared; passing an explicit empty string for either is rejected (callers that previously sent `\"\"` to mean \"don't change\" should now omit the key entirely)."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithNumber("link_id", mcpgo.Required(), mcpgo.Description("Link ID"), mcpgo.Min(1)),
		mcpgo.WithString("type", mcpgo.Description("New link type"), mcpgo.Enum("jira", "pr", "url")),
		mcpgo.WithString("url", mcpgo.Description("New URL (max 2000 chars)"), mcpgo.MaxLength(2000)),
		mcpgo.WithString("description", mcpgo.Description("New description (max 1000 chars; empty string clears it)"), mcpgo.MaxLength(1000)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		linkID, err := requireUint(req, "link_id")
		if err != nil {
			return errResult(err), nil
		}
		opts := store.UpdateLinkOptions{}
		// `type` and `url` cannot be cleared by passing an empty
		// string. Distinguish "key absent" (leave the field alone)
		// from "key present and empty" (reject) so a caller that
		// accidentally sends "" doesn't silently no-op.
		if t := getOptStr(req, "type"); t != nil {
			if *t == "" {
				return errResult(fmt.Errorf("type cannot be cleared; omit the key to leave it unchanged")), nil
			}
			lt := model.LinkType(*t)
			if !model.ValidLinkTypes[lt] {
				return errResult(fmt.Errorf("invalid link type %q (valid: jira, pr, url)", lt)), nil
			}
			opts.Type = &lt
		}
		if u := getOptStr(req, "url"); u != nil {
			if *u == "" {
				return errResult(fmt.Errorf("url cannot be cleared; omit the key to leave it unchanged")), nil
			}
			opts.URL = u
		}
		// description IS clearable: an explicit empty string clears it,
		// an absent key leaves it alone.
		opts.Description = getOptStr(req, "description")
		link, err := s.UpdateLink(ctx, taskID, linkID, opts)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(link)), nil
	})

	srv.AddTool(mcpgo.NewTool("delete_link",
		mcpgo.WithDescription("Delete a link from a task."),
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
		return textResult(toJSON(map[string]any{"task_id": taskID, "link_id": linkID, "deleted": true})), nil
	})
}
