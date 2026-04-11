package mcp

import (
	"context"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerNoteTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("add_note",
		mcpgo.WithDescription("Add a note to a task"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithString("text", mcpgo.Required(), mcpgo.Description("Note text")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		note, err := s.AddNote(getUint(req, "task_id"), getStr(req, "text"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(note)), nil
	})

	srv.AddTool(mcpgo.NewTool("update_note",
		mcpgo.WithDescription("Update a note's text"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithNumber("note_id", mcpgo.Required(), mcpgo.Description("Note ID")),
		mcpgo.WithString("text", mcpgo.Required(), mcpgo.Description("New text")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		note, err := s.UpdateNote(getUint(req, "task_id"), getUint(req, "note_id"), getStr(req, "text"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(note)), nil
	})

	srv.AddTool(mcpgo.NewTool("list_notes",
		mcpgo.WithDescription("List notes for a task"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		notes, err := s.ListNotes(getUint(req, "task_id"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(notes)), nil
	})

	srv.AddTool(mcpgo.NewTool("delete_note",
		mcpgo.WithDescription("Delete a note"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithNumber("note_id", mcpgo.Required(), mcpgo.Description("Note ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.DeleteNote(getUint(req, "task_id"), getUint(req, "note_id")); err != nil {
			return errResult(err), nil
		}
		return textResult("deleted"), nil
	})
}
