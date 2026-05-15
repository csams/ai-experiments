package mcp

import (
	"context"
	"fmt"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerTaskTools(srv *server.MCPServer, s store.Store) {
	// linksSchema is the JSON-Schema fragment for an array of {type, url, description}
	// objects. This is the first object-shaped items schema in this codebase; future
	// tools that accept structured arrays should follow the same mcpgo.Items pattern.
	linksSchema := map[string]any{
		"type":     "object",
		"required": []string{"type", "url"},
		"properties": map[string]any{
			"type":        map[string]any{"type": "string", "enum": []string{"jira", "pr", "url"}},
			"url":         map[string]any{"type": "string", "maxLength": 2000},
			"description": map[string]any{"type": "string", "maxLength": 1000},
		},
	}

	// create_task
	srv.AddTool(mcpgo.NewTool("create_task",
		mcpgo.WithDescription("Create a new task. Returns the created task with tags and links. "+
			"Optionally attach links inline via `links` — atomic with task creation "+
			"(any link validation failure rolls the whole call back) and avoids the "+
			"re-embed churn of separate add_link calls. "+
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithString("title", mcpgo.Required(), mcpgo.Description("Task title (max 512 chars)"), mcpgo.MaxLength(512)),
		mcpgo.WithString("description", mcpgo.Description("Task description (max 100000 chars)"), mcpgo.MaxLength(100000)),
		mcpgo.WithNumber("priority", mcpgo.Description("Priority (lower number = higher importance, negative values allowed)")),
		mcpgo.WithString("due_at", mcpgo.Description("Due date (YYYY-MM-DD)")),
		mcpgo.WithArray("tags", mcpgo.Description("Tags (alphanumeric/hyphens/underscores only, max 100 chars each)"), mcpgo.WithStringItems(mcpgo.MaxLength(100)), mcpgo.MaxItems(50)),
		mcpgo.WithArray("links",
			mcpgo.Description("Optional links to attach atomically with task creation. Each item is {type, url, description}."),
			mcpgo.MaxItems(50),
			mcpgo.Items(linksSchema),
		),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		title, err := requireStr(req, "title")
		if err != nil {
			return errResult(err), nil
		}
		dueAt, err := getTime(req, "due_at")
		if err != nil {
			return errResult(err), nil
		}
		links, err := getLinkInputs(req, "links")
		if err != nil {
			return errResult(err), nil
		}
		task, err := s.CreateTask(ctx, store.CreateTaskOptions{
			Title:       title,
			Description: getStr(req, "description"),
			Priority:    getInt(req, "priority"),
			DueAt:       dueAt,
			Tags:        getStrSlice(req, "tags"),
			Links:       links,
		})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// create_subtask
	srv.AddTool(mcpgo.NewTool("create_subtask",
		mcpgo.WithDescription("Create a subtask under an existing non-archived parent. "+
			"Returns full task detail (parent, children, links, etc.). "+
			"Optionally attach links inline via `links` — atomic with task creation. "+
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("parent_id", mcpgo.Required(), mcpgo.Description("Parent task ID"), mcpgo.Min(1)),
		mcpgo.WithString("title", mcpgo.Required(), mcpgo.Description("Task title (max 512 chars)"), mcpgo.MaxLength(512)),
		mcpgo.WithString("description", mcpgo.Description("Task description (max 100000 chars)"), mcpgo.MaxLength(100000)),
		mcpgo.WithNumber("priority", mcpgo.Description("Priority (lower number = higher importance, negative values allowed)")),
		mcpgo.WithString("due_at", mcpgo.Description("Due date (YYYY-MM-DD)")),
		mcpgo.WithArray("tags", mcpgo.Description("Tags (alphanumeric/hyphens/underscores only, max 100 chars each)"), mcpgo.WithStringItems(mcpgo.MaxLength(100)), mcpgo.MaxItems(50)),
		mcpgo.WithArray("links",
			mcpgo.Description("Optional links to attach atomically with task creation. Each item is {type, url, description}."),
			mcpgo.MaxItems(50),
			mcpgo.Items(linksSchema),
		),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		parentID, err := requireUint(req, "parent_id")
		if err != nil {
			return errResult(err), nil
		}
		title, err := requireStr(req, "title")
		if err != nil {
			return errResult(err), nil
		}
		dueAt, err := getTime(req, "due_at")
		if err != nil {
			return errResult(err), nil
		}
		links, err := getLinkInputs(req, "links")
		if err != nil {
			return errResult(err), nil
		}
		task, err := s.CreateTask(ctx, store.CreateTaskOptions{
			Title:       title,
			Description: getStr(req, "description"),
			Priority:    getInt(req, "priority"),
			DueAt:       dueAt,
			Tags:        getStrSlice(req, "tags"),
			Links:       links,
			ParentID:    &parentID,
		})
		if err != nil {
			return errResult(err), nil
		}
		detail, err := s.GetTask(ctx, task.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(detail)), nil
	})

	// list_tasks
	srv.AddTool(mcpgo.NewTool("list_tasks",
		mcpgo.WithDescription("List tasks with filters and sorting. By default shows only "+
			"non-archived top-level tasks sorted by priority. Supports filtering by state, "+
			"due date (range, exact day, or presence), priority range, tags (superset "+
			"AND logic or subset containment), and a `query` substring filter on title, "+
			"description, and link descriptions. Use `include` to opt into expensive per-item "+
			"fields (description, notes, links, parent, children, blockers); by default each "+
			"item carries only cheap bounded fields plus tags and a has_checkpoint flag."),
		mcpgo.WithArray("states",
			mcpgo.Description("Filter by state (OR logic — task matches any listed state)"),
			mcpgo.WithStringItems(mcpgo.Enum("New", "Progressing", "Blocked", "Unblocked", "Done")),
			mcpgo.MaxItems(5)),
		mcpgo.WithBoolean("include_archived", mcpgo.Description("Include archived tasks")),
		mcpgo.WithBoolean("include_subtasks", mcpgo.Description("Include subtasks (flat list)")),
		mcpgo.WithNumber("parent_id", mcpgo.Description("Filter to subtree of this task ID (includes root)"), mcpgo.Min(1)),
		mcpgo.WithArray("tags", mcpgo.Description("Filter by tags (AND logic)"), mcpgo.WithStringItems(mcpgo.MaxLength(100)), mcpgo.MaxItems(50)),
		mcpgo.WithBoolean("overdue", mcpgo.Description("Only tasks past their due date")),
		mcpgo.WithString("sort_by", mcpgo.Description("Sort field (default: priority)"), mcpgo.Enum("priority", "due", "created", "updated")),
		mcpgo.WithBoolean("has_due_date",
			mcpgo.Description("Filter by due date presence: true = only tasks with a due date, false = only tasks without")),
		mcpgo.WithString("due_before",
			mcpgo.Description("Only tasks due before this date, exclusive (YYYY-MM-DD). Excludes tasks with no due date."),
			mcpgo.Pattern(`^\d{4}-\d{2}-\d{2}$`)),
		mcpgo.WithString("due_after",
			mcpgo.Description("Only tasks due after this date, exclusive (YYYY-MM-DD). Excludes tasks with no due date."),
			mcpgo.Pattern(`^\d{4}-\d{2}-\d{2}$`)),
		mcpgo.WithString("due_on",
			mcpgo.Description("Only tasks due on this calendar day (YYYY-MM-DD, UTC). Excludes tasks with no due date."),
			mcpgo.Pattern(`^\d{4}-\d{2}-\d{2}$`)),
		mcpgo.WithNumber("priority_min",
			mcpgo.Description("Minimum priority value, inclusive (lower number = higher importance). Negative values allowed.")),
		mcpgo.WithNumber("priority_max",
			mcpgo.Description("Maximum priority value, inclusive (lower number = higher importance). Negative values allowed.")),
		mcpgo.WithString("query",
			mcpgo.Description("Case-insensitive substring match on title, description, and link descriptions (max 500 chars). "+
				"Use this for exact keyword/substring lookups (a name, codename, ID fragment, distinctive word). "+
				"For conceptual / 'what was that thing about X' lookups, use semantic_search instead."),
			mcpgo.MaxLength(500)),
		mcpgo.WithArray("tags_subset_of",
			mcpgo.Description("Task's tags must all be within this set (subset check). "+
				"A task with no tags matches (empty set is a subset of any set). "+
				"Combine with 'tags' to require exact tag sets."),
			mcpgo.WithStringItems(mcpgo.MaxLength(100)),
			mcpgo.MaxItems(50)),
		mcpgo.WithArray("include",
			mcpgo.Description("Optional per-item fields to load. By default each item "+
				"carries only cheap fields. Choices: description, notes, links, "+
				"parent, children, blockers. Use \"*\" for all. Note: list_tasks "+
				"does not load `blocking`."),
			mcpgo.WithStringItems(mcpgo.Enum(
				"*", "description", "notes", "links", "parent", "children", "blockers",
			)),
			mcpgo.MaxItems(7)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		inc, err := resolveTaskIncludes(req, model.TaskListIncludes)
		if err != nil {
			return errResult(err), nil
		}
		opts := store.ListTasksOptions{
			IncludeArchived: getBool(req, "include_archived"),
			IncludeSubtasks: getBool(req, "include_subtasks"),
			Tags:            getStrSlice(req, "tags"),
			Overdue:         getBool(req, "overdue"),
			SortBy:          getStr(req, "sort_by"),
			HasDueDate:      getOptBool(req, "has_due_date"),
			PriorityMin:     getOptInt(req, "priority_min"),
			PriorityMax:     getOptInt(req, "priority_max"),
			TagsSubsetOf:    getStrSlice(req, "tags_subset_of"),
			Query:           getStr(req, "query"),
			Include:         inc,
		}
		if raw := getStrSlice(req, "states"); len(raw) > 0 {
			states := make([]model.TaskState, 0, len(raw))
			for _, s := range raw {
				st := model.TaskState(s)
				if !model.ValidTaskStates[st] {
					return errResult(fmt.Errorf("invalid state %q", s)), nil
				}
				states = append(states, st)
			}
			opts.States = states
		}
		if pid := getUint(req, "parent_id"); pid > 0 {
			opts.ParentID = &pid
		}
		if opts.DueBefore, err = getTime(req, "due_before"); err != nil {
			return errResult(err), nil
		}
		if opts.DueAfter, err = getTime(req, "due_after"); err != nil {
			return errResult(err), nil
		}
		if opts.DueOn, err = getTime(req, "due_on"); err != nil {
			return errResult(err), nil
		}

		tasks, err := s.ListTasks(ctx, opts)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	// get_task
	srv.AddTool(mcpgo.NewTool("get_task",
		mcpgo.WithDescription("Get a task. By default returns only cheap, bounded fields "+
			"(id, title, priority, state, archived, due_at, parent_id, timestamps, "+
			"tags, checkpoint). Use `include` to opt into expensive fields. "+
			"Pass [\"*\"] for the full payload (description, notes, links, parent, "+
			"children, blockers, blocking). Empty blocking lists are omitted from "+
			"the JSON output."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithArray("include",
			mcpgo.Description("Optional fields to load. Choices: description, notes, "+
				"links, parent, children, blockers, blocking. Use \"*\" for all."),
			mcpgo.WithStringItems(mcpgo.Enum(
				"*", "description", "notes", "links", "parent", "children", "blockers", "blocking",
			)),
			mcpgo.MaxItems(8),
		),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		inc, err := resolveTaskIncludes(req, model.TaskIncludes)
		if err != nil {
			return errResult(err), nil
		}
		detail, err := s.GetTask(ctx, id, store.GetTaskOptions{Include: inc})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(detail)), nil
	})

	// get_tasks
	srv.AddTool(mcpgo.NewTool("get_tasks",
		mcpgo.WithDescription("Batch-fetch multiple tasks in one call (max 100). Returns "+
			"{\"tasks\": [...], \"not_found\": [...]}. Tasks come back in the order of the "+
			"input `ids` (duplicates collapsed to first occurrence); IDs with no matching "+
			"task go into `not_found` rather than aborting the call. The `include` option "+
			"applies uniformly to every task — same enum and \"*\" semantics as get_task. "+
			"Empty results render as [] (never null)."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithArray("include",
			mcpgo.Description("Optional fields to load. Choices: description, notes, "+
				"links, parent, children, blockers, blocking. Use \"*\" for all."),
			mcpgo.WithStringItems(mcpgo.Enum(
				"*", "description", "notes", "links", "parent", "children", "blockers", "blocking",
			)),
			mcpgo.MaxItems(8),
		),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		ids, err := requireUintSlice(req, "ids")
		if err != nil {
			return errResult(err), nil
		}
		if len(ids) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("ids: max %d per call", maxBulkMCPIDs)), nil
		}
		inc, err := resolveTaskIncludes(req, model.TaskIncludes)
		if err != nil {
			return errResult(err), nil
		}
		result, err := s.GetTasks(ctx, ids, store.GetTaskOptions{Include: inc})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(result)), nil
	})

	// update_task
	srv.AddTool(mcpgo.NewTool("update_task",
		mcpgo.WithDescription("Update a task's title, description, priority, or due date. " +
			"Only provided fields are changed. Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithString("title", mcpgo.Description("Task title (max 512 chars)"), mcpgo.MaxLength(512)),
		mcpgo.WithString("description", mcpgo.Description("Task description (max 100000 chars)"), mcpgo.MaxLength(100000)),
		mcpgo.WithNumber("priority", mcpgo.Description("Priority (lower number = higher importance, negative values allowed)")),
		mcpgo.WithString("due_at", mcpgo.Description("Due date (YYYY-MM-DD)")),
		mcpgo.WithBoolean("clear_due", mcpgo.Description("Remove due date (takes precedence over due_at if both provided)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
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
		} else {
			t, err := getTime(req, "due_at")
			if err != nil {
				return errResult(err), nil
			}
			if t != nil {
				opts.DueAt = t
			}
		}

		task, err := s.UpdateTask(ctx, id, opts)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// set_task_state
	srv.AddTool(mcpgo.NewTool("set_task_state",
		mcpgo.WithDescription("Set task state. Cannot set Blocked directly — use add_blockers instead. " +
			"Setting Done auto-unblocks dependents whose blockers are all complete. " +
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithString("state", mcpgo.Required(), mcpgo.Description("Target state"), mcpgo.Enum("New", "Progressing", "Unblocked", "Done")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		state, err := getState(req, "state")
		if err != nil {
			return errResult(err), nil
		}
		task, err := s.SetTaskState(ctx, id, state)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// add_blockers
	srv.AddTool(mcpgo.NewTool("add_blockers",
		mcpgo.WithDescription("Add blocking dependencies. Transitions task to Blocked state. " +
			"Validates no self-blocking or cycles. Blocker must not be Done or archived. " +
			"Promotes blocker priority to at least match blocked task. " +
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID to block"), mcpgo.Min(1)),
		mcpgo.WithArray("blocker_ids", mcpgo.Required(), mcpgo.Description("IDs of tasks that block this one"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		blockerIDs, err := requireUintSlice(req, "blocker_ids")
		if err != nil {
			return errResult(err), nil
		}
		task, err := s.AddBlockers(ctx, id, blockerIDs)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// remove_blockers
	srv.AddTool(mcpgo.NewTool("remove_blockers",
		mcpgo.WithDescription("Remove specific blockers. Auto-transitions to Unblocked if no blockers remain. " +
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithArray("blocker_ids", mcpgo.Required(), mcpgo.Description("Blocker IDs to remove"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		blockerIDs, err := requireUintSlice(req, "blocker_ids")
		if err != nil {
			return errResult(err), nil
		}
		task, err := s.RemoveBlockers(ctx, id, blockerIDs)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// archive_task
	srv.AddTool(mcpgo.NewTool("archive_task",
		mcpgo.WithDescription("Archive task and its entire subtask tree. " +
			"Fails if any task in the set blocks an external task. Preserves blocker entries. " +
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.ArchiveTask(ctx, id, true); err != nil {
			return errResult(err), nil
		}
		detail, err := s.GetTask(ctx, id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(detail)), nil
	})

	// unarchive_task
	srv.AddTool(mcpgo.NewTool("unarchive_task",
		mcpgo.WithDescription("Unarchive task and its entire subtask tree. " +
			"Validates preserved blocker relationships. " +
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.ArchiveTask(ctx, id, false); err != nil {
			return errResult(err), nil
		}
		detail, err := s.GetTask(ctx, id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(detail)), nil
	})

	// delete_task
	srv.AddTool(mcpgo.NewTool("delete_task",
		mcpgo.WithDescription("Delete a task. recursive=false (default): promotes subtasks to top-level. recursive=true: permanently deletes this task AND all subtasks. By default the task's notes are orphaned (kept as standalone notes); pass delete_notes=true to hard-delete them. Fails if any affected task blocks an external task."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithBoolean("recursive", mcpgo.Description("Delete entire subtask tree (default: false, promotes subtasks)")),
		mcpgo.WithBoolean("delete_notes", mcpgo.Description("Hard-delete the task's notes instead of orphaning them (default: false)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		opts := store.DeleteTaskOptions{
			Recursive:   getBool(req, "recursive"),
			DeleteNotes: getBool(req, "delete_notes"),
		}
		if err := s.DeleteTask(ctx, id, opts); err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(map[string]any{"task_id": id, "deleted": true})), nil
	})

	// set_parent
	srv.AddTool(mcpgo.NewTool("set_parent",
		mcpgo.WithDescription("Make a task a subtask of another. Validates no cycles. " +
			"Returns full task detail. Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithNumber("parent_id", mcpgo.Required(), mcpgo.Description("Parent task ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		pid, err := requireUint(req, "parent_id")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.SetParent(ctx, id, &pid); err != nil {
			return errResult(err), nil
		}
		task, err := s.GetTask(ctx, id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// unparent
	srv.AddTool(mcpgo.NewTool("unparent",
		mcpgo.WithDescription("Make a task top-level (remove from parent). Returns full task detail. " +
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		if err := s.SetParent(ctx, id, nil); err != nil {
			return errResult(err), nil
		}
		task, err := s.GetTask(ctx, id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})
}
