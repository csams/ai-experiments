package mcp

import (
	"context"
	"fmt"

	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerNoteTools(srv *server.MCPServer, s store.Store) {
	srv.AddTool(mcpgo.NewTool("add_note",
		mcpgo.WithDescription("Add a note. Pass task_id to attach to a task; omit task_id to create a standalone note."),
		mcpgo.WithNumber("task_id", mcpgo.Description("Optional task ID. Omit to create a standalone note."), mcpgo.Min(1)),
		mcpgo.WithString("text", mcpgo.Required(), mcpgo.Description("Note text (max 50000 chars)"), mcpgo.MaxLength(50000)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID := getOptUint(req, "task_id")
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
		mcpgo.WithDescription(
			"Update a note. Provide note_id and at least one of: text, task_id (reparent target), "+
				"clear_task_id (true to detach), archived. Only provided fields change.",
		),
		mcpgo.WithNumber("note_id", mcpgo.Required(), mcpgo.Description("Note ID"), mcpgo.Min(1)),
		mcpgo.WithString("text", mcpgo.Description("New note text (max 50000 chars)"), mcpgo.MaxLength(50000)),
		mcpgo.WithNumber("task_id", mcpgo.Description("Reparent to this task ID. Mutually exclusive with clear_task_id."), mcpgo.Min(1)),
		mcpgo.WithBoolean("clear_task_id", mcpgo.Description("Set to true to detach the note from any task (make standalone).")),
		mcpgo.WithBoolean("archived", mcpgo.Description("Set the archived flag (only meaningful for standalone notes; task-attached notes inherit from their task at embed time).")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		noteID, err := requireUint(req, "note_id")
		if err != nil {
			return errResult(err), nil
		}
		textPtr := getOptStr(req, "text")
		taskIDPtr := getOptUint(req, "task_id")
		clearPtr := getOptBool(req, "clear_task_id")
		archivedPtr := getOptBool(req, "archived")

		clear := clearPtr != nil && *clearPtr
		if taskIDPtr != nil && clear {
			return errResult(fmt.Errorf("task_id and clear_task_id=true are mutually exclusive")), nil
		}

		opts := store.UpdateNoteOptions{
			Text:     textPtr,
			Archived: archivedPtr,
		}
		if taskIDPtr != nil {
			opts.SetTaskID = true
			opts.TaskID = taskIDPtr
		} else if clear {
			opts.SetTaskID = true
			opts.TaskID = nil
		}

		if opts.Text == nil && !opts.SetTaskID && opts.Archived == nil {
			return errResult(fmt.Errorf("at least one of text, task_id, clear_task_id, archived must be provided")), nil
		}

		note, err := s.UpdateNote(ctx, noteID, opts)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(note)), nil
	})

	srv.AddTool(mcpgo.NewTool("list_notes",
		mcpgo.WithDescription("List notes. With task_id: that task's notes. Without task_id: standalone notes (no parent task)."),
		mcpgo.WithNumber("task_id", mcpgo.Description("Optional task ID. Omit to list standalone notes only."), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		taskID := getOptUint(req, "task_id")
		notes, err := s.ListNotes(ctx, taskID)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(notes)), nil
	})

	srv.AddTool(mcpgo.NewTool("list_all_notes",
		mcpgo.WithDescription("List every note in the system (attached + standalone)."),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		notes, err := s.ListAllNotes(ctx)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(notes)), nil
	})

	srv.AddTool(mcpgo.NewTool("delete_note",
		mcpgo.WithDescription("Delete a note by ID."),
		mcpgo.WithNumber("note_id", mcpgo.Required(), mcpgo.Description("Note ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		noteID, err := requireUint(req, "note_id")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.DeleteNote(ctx, noteID); err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(map[string]any{"note_id": noteID, "deleted": true})), nil
	})
}
