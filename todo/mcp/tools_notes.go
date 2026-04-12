package mcp

import (
	"context"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerNoteTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("add_note",
		mcpgo.WithDescription("Add a note to a task. Returns the created note."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithString("text", mcpgo.Required(), mcpgo.Description("Note text")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		text, err := requireStr(req, "text")
		if err != nil {
			return errResult(err), nil
		}
		note, err := s.AddNote(ctx, taskID, text)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(note)), nil
	})

	srv.AddTool(mcpgo.NewTool("update_note",
		mcpgo.WithDescription("Update a note's text. Returns the updated note."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithNumber("note_id", mcpgo.Required(), mcpgo.Description("Note ID"), mcpgo.Min(1)),
		mcpgo.WithString("text", mcpgo.Required(), mcpgo.Description("New text")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		noteID, err := requireUint(req, "note_id")
		if err != nil {
			return errResult(err), nil
		}
		text, err := requireStr(req, "text")
		if err != nil {
			return errResult(err), nil
		}
		note, err := s.UpdateNote(ctx, taskID, noteID, text)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(note)), nil
	})

	srv.AddTool(mcpgo.NewTool("list_notes",
		mcpgo.WithDescription("List all notes for a task. Returns an array of notes."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		notes, err := s.ListNotes(ctx, taskID)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(notes)), nil
	})

	srv.AddTool(mcpgo.NewTool("delete_note",
		mcpgo.WithDescription("Delete a note"),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithNumber("note_id", mcpgo.Required(), mcpgo.Description("Note ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		noteID, err := requireUint(req, "note_id")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.DeleteNote(ctx, taskID, noteID); err != nil {
			return errResult(err), nil
		}
		return textResult("deleted"), nil
	})
}
