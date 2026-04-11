package mcp

import (
	"context"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTaskTools(srv *server.MCPServer, s store.Store) {
	// create_task
	srv.AddTool(mcpgo.NewTool("create_task",
		mcpgo.WithDescription("Create a new task with title, description, priority, due date, and tags"),
		mcpgo.WithString("title", mcpgo.Required(), mcpgo.Description("Task title (max 500 chars)")),
		mcpgo.WithString("description", mcpgo.Description("Task description")),
		mcpgo.WithNumber("priority", mcpgo.Description("Priority (lower = more important, negative OK)")),
		mcpgo.WithString("due_at", mcpgo.Description("Due date in YYYY-MM-DD format")),
		mcpgo.WithArray("tags", mcpgo.Description("Tags for categorization"), mcpgo.WithStringItems()),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		task, err := s.CreateTask(
			getStr(req, "title"),
			getStr(req, "description"),
			getInt(req, "priority"),
			getTime(req, "due_at"),
			getStrSlice(req, "tags"),
		)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// list_tasks
	srv.AddTool(mcpgo.NewTool("list_tasks",
		mcpgo.WithDescription("List tasks with filters and sorting. By default shows only non-archived top-level tasks sorted by priority."),
		mcpgo.WithString("state", mcpgo.Description("Filter by state"), mcpgo.Enum("New", "Progressing", "Blocked", "Unblocked", "Done")),
		mcpgo.WithBoolean("include_archived", mcpgo.Description("Include archived tasks")),
		mcpgo.WithBoolean("include_subtasks", mcpgo.Description("Include subtasks (flat list)")),
		mcpgo.WithNumber("parent_id", mcpgo.Description("Filter to subtree of this task ID (includes root)")),
		mcpgo.WithArray("tags", mcpgo.Description("Filter by tags (AND logic)"), mcpgo.WithStringItems()),
		mcpgo.WithBoolean("overdue", mcpgo.Description("Only overdue tasks")),
		mcpgo.WithString("sort_by", mcpgo.Description("Sort by field"), mcpgo.Enum("priority", "due", "created", "updated")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		opts := store.ListTasksOptions{
			IncludeArchived: getBool(req, "include_archived"),
			IncludeSubtasks: getBool(req, "include_subtasks"),
			Tags:            getStrSlice(req, "tags"),
			Overdue:         getBool(req, "overdue"),
			SortBy:          getStr(req, "sort_by"),
		}
		if stateStr := getStr(req, "state"); stateStr != "" {
			state := model.TaskState(stateStr)
			opts.State = &state
		}
		if pid := getUint(req, "parent_id"); pid > 0 {
			opts.ParentID = &pid
		}

		tasks, err := s.ListTasks(opts)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	// get_task
	srv.AddTool(mcpgo.NewTool("get_task",
		mcpgo.WithDescription("Get full task detail including blockers, blocking, children, notes, links, and tags"),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		detail, err := s.GetTask(getUint(req, "id"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(detail)), nil
	})

	// update_task
	srv.AddTool(mcpgo.NewTool("update_task",
		mcpgo.WithDescription("Update a task's title, description, priority, or due date. Only provided fields are changed."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithString("title", mcpgo.Description("New title")),
		mcpgo.WithString("description", mcpgo.Description("New description")),
		mcpgo.WithNumber("priority", mcpgo.Description("New priority")),
		mcpgo.WithString("due_at", mcpgo.Description("New due date (YYYY-MM-DD)")),
		mcpgo.WithBoolean("clear_due", mcpgo.Description("Remove due date")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		opts := store.UpdateTaskOptions{}
		if v, ok := args["title"].(string); ok {
			opts.Title = &v
		}
		if v, ok := args["description"].(string); ok {
			opts.Description = &v
		}
		if v, ok := args["priority"].(float64); ok {
			p := int(v)
			opts.Priority = &p
		}
		if getBool(req, "clear_due") {
			opts.ClearDueAt = true
		} else if t := getTime(req, "due_at"); t != nil {
			opts.DueAt = t
		}

		task, err := s.UpdateTask(getUint(req, "id"), opts)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// set_task_state
	srv.AddTool(mcpgo.NewTool("set_task_state",
		mcpgo.WithDescription("Set task state (New/Progressing/Unblocked/Done). Clears blockers. Done cascades unblocks to dependents. Use add_blockers to set Blocked state."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithString("state", mcpgo.Required(), mcpgo.Description("Target state"), mcpgo.Enum("New", "Progressing", "Unblocked", "Done")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		state, err := getState(req, "state")
		if err != nil {
			return errResult(err), nil
		}
		task, err := s.SetTaskState(getUint(req, "id"), state)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// add_blockers
	srv.AddTool(mcpgo.NewTool("add_blockers",
		mcpgo.WithDescription("Add blocker tasks. Sets state to Blocked. Validates no cycles. Adjusts blocker priorities."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID to block")),
		mcpgo.WithArray("blocker_ids", mcpgo.Required(), mcpgo.Description("IDs of tasks that block this one")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		task, err := s.AddBlockers(getUint(req, "id"), getUintSlice(req, "blocker_ids"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// remove_blockers
	srv.AddTool(mcpgo.NewTool("remove_blockers",
		mcpgo.WithDescription("Remove specific blockers. Auto-transitions to Unblocked if no blockers remain."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithArray("blocker_ids", mcpgo.Required(), mcpgo.Description("Blocker IDs to remove")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		task, err := s.RemoveBlockers(getUint(req, "id"), getUintSlice(req, "blocker_ids"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// archive_task
	srv.AddTool(mcpgo.NewTool("archive_task",
		mcpgo.WithDescription("Archive task and its entire subtask tree. Fails if any task in the set blocks an external task. Preserves blocker entries."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.ArchiveTask(getUint(req, "id"), true); err != nil {
			return errResult(err), nil
		}
		return textResult("archived"), nil
	})

	// unarchive_task
	srv.AddTool(mcpgo.NewTool("unarchive_task",
		mcpgo.WithDescription("Unarchive task and its entire subtask tree. Validates preserved blocker relationships."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.ArchiveTask(getUint(req, "id"), false); err != nil {
			return errResult(err), nil
		}
		return textResult("unarchived"), nil
	})

	// delete_task
	srv.AddTool(mcpgo.NewTool("delete_task",
		mcpgo.WithDescription("Delete a task. recursive=false (default): promotes subtasks to top-level. recursive=true: permanently deletes this task AND all subtasks. Fails if any affected task blocks an external task."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithBoolean("recursive", mcpgo.Description("Delete entire subtask tree (default: false, promotes subtasks)")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.DeleteTask(getUint(req, "id"), getBool(req, "recursive")); err != nil {
			return errResult(err), nil
		}
		return textResult("deleted"), nil
	})

	// set_parent
	srv.AddTool(mcpgo.NewTool("set_parent",
		mcpgo.WithDescription("Make a task a subtask of another. Validates no cycles."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
		mcpgo.WithNumber("parent_id", mcpgo.Required(), mcpgo.Description("Parent task ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		pid := getUint(req, "parent_id")
		if err := s.SetParent(getUint(req, "id"), &pid); err != nil {
			return errResult(err), nil
		}
		return textResult("parent set"), nil
	})

	// unparent
	srv.AddTool(mcpgo.NewTool("unparent",
		mcpgo.WithDescription("Make a task top-level (remove from parent)."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Task ID")),
	), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := s.SetParent(getUint(req, "id"), nil); err != nil {
			return errResult(err), nil
		}
		return textResult("unparented"), nil
	})
}
