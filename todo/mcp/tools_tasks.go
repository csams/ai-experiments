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
		mcpgo.WithDescription("Create a new task, optionally as a subtask via parent_id. "+
			"Returns full task detail by default; pass `include` to restrict which "+
			"expensive fields are loaded. "+
			"Optionally attach links inline via `links` — atomic with task creation "+
			"(any link validation failure rolls the whole call back) and avoids the "+
			"re-embed churn of separate add_link calls. "+
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithString("title", mcpgo.Required(), mcpgo.Description("Task title (max 512 chars)"), mcpgo.MaxLength(512)),
		mcpgo.WithNumber("parent_id", mcpgo.Description("Optional parent task ID. Omit to create a top-level task; set to make this a subtask of an existing non-archived parent."), mcpgo.Min(1)),
		mcpgo.WithString("description", mcpgo.Description("Task description (max 100000 chars)"), mcpgo.MaxLength(100000)),
		mcpgo.WithNumber("priority", mcpgo.Description("Priority (lower number = higher importance, negative values allowed)")),
		mcpgo.WithString("due_at", mcpgo.Description("Due date (YYYY-MM-DD)")),
		mcpgo.WithArray("tags", mcpgo.Description("Tags (alphanumeric/hyphens/underscores only, max 100 chars each)"), mcpgo.WithStringItems(mcpgo.MaxLength(100)), mcpgo.MaxItems(50)),
		mcpgo.WithArray("links",
			mcpgo.Description("Optional links to attach atomically with task creation. Each item is {type, url, description}."),
			mcpgo.MaxItems(50),
			mcpgo.Items(linksSchema),
		),
		mcpgo.WithArray("include",
			mcpgo.Description("Optional fields to load on the returned task. Choices: description, notes, "+
				"links, parent, children, blockers, blocking. Use \"*\" for all. "+
				"Omit the parameter entirely to get full detail (the default). "+
				"Passing an explicit empty array returns cheap fields only — that is a distinct request, not the default."),
			mcpgo.WithStringItems(mcpgo.Enum(
				"*", "description", "notes", "links", "parent", "children", "blockers", "blocking",
			)),
			mcpgo.MaxItems(8),
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
		opts := store.CreateTaskOptions{
			Title:       title,
			Description: getStr(req, "description"),
			Priority:    getInt(req, "priority"),
			DueAt:       dueAt,
			Tags:        getStrSlice(req, "tags"),
			Links:       links,
		}
		opts.ParentID = getOptUint(req, "parent_id")
		task, err := s.CreateTask(ctx, opts)
		if err != nil {
			return errResult(err), nil
		}
		// Default to full detail; honor explicit include when provided.
		inc := model.AllTaskIncludesSet()
		if _, ok := req.GetArguments()["include"]; ok {
			inc, err = resolveTaskIncludes(req, model.TaskIncludes)
			if err != nil {
				return errResult(err), nil
			}
		}
		detail, err := s.GetTask(ctx, task.ID, store.GetTaskOptions{Include: inc})
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
		mcpgo.WithDescription("Set the state of one or more tasks (1..100 IDs). Atomic across the entire array; "+
			"processes tasks in ascending ID order. Cannot set Blocked or Unblocked directly — Blocked is set via "+
			"update_blockers; Unblocked is an auto-transition fired when a Blocked task's last blocker is removed. "+
			"Setting Done is terminal: it always clears the task's blocker rows and auto-unblocks dependents whose "+
			"remaining blockers all complete. "+
			"Transitioning a Blocked task to any non-Done state is rejected by default to prevent silent loss of "+
			"dependency information; pass force_clear_blockers=true to drop the outstanding blockers as part of "+
			"the transition. A single rejected task aborts the whole batch. "+
			"Returns updated tasks. Empty descriptions are omitted from the JSON response."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithString("state", mcpgo.Required(), mcpgo.Description("Target state"), mcpgo.Enum("New", "Progressing", "Done")),
		mcpgo.WithBoolean("force_clear_blockers", mcpgo.Description(
			"When true, allow transitioning a Blocked task to a non-Done state by dropping its outstanding blocker rows. "+
				"Has no effect for Done transitions (terminal) or for tasks that are not currently Blocked.")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		ids, err := requireUintSlice(req, "ids")
		if err != nil {
			return errResult(err), nil
		}
		if len(ids) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("ids: max %d per call", maxBulkMCPIDs)), nil
		}
		state, err := getState(req, "state")
		if err != nil {
			return errResult(err), nil
		}
		tasks, err := s.BulkUpdateState(ctx, ids, state, store.SetTaskStateOptions{
			ForceClearBlockers: getBool(req, "force_clear_blockers"),
		})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	// set_task_priority
	srv.AddTool(mcpgo.NewTool("set_task_priority",
		mcpgo.WithDescription("Set the priority of one or more tasks (1..100 IDs). Atomic across the entire array; "+
			"processes tasks in ascending ID order. "+
			"Lower number = higher importance; negative values are allowed. "+
			"Blockers are automatically promoted to at least match the priority of any task they block. "+
			"Returns updated tasks. Empty descriptions are omitted from the JSON response."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithNumber("priority", mcpgo.Required(), mcpgo.Description("Priority (lower number = higher importance, negative values allowed)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		ids, err := requireUintSlice(req, "ids")
		if err != nil {
			return errResult(err), nil
		}
		if len(ids) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("ids: max %d per call", maxBulkMCPIDs)), nil
		}
		tasks, err := s.BulkUpdatePriority(ctx, ids, getInt(req, "priority"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(tasks)), nil
	})

	// update_blockers
	srv.AddTool(mcpgo.NewTool("update_blockers",
		mcpgo.WithDescription("Add and/or remove blocker dependencies on a task in one transaction. "+
			"At least one of `add` or `remove` must be non-empty. "+
			"Removals are processed first so a blocker can be swapped (drop + add) "+
			"without tripping cycle detection on a stale row. "+
			"Adding blockers: validates no self-blocking and no cycles; each blocker must not be Done or archived; "+
			"blocker priority is promoted to at least match the blocked task. "+
			"State transitions: if any blockers remain after the update the task is set to Blocked; "+
			"if all blockers are gone and the task was Blocked it transitions to Unblocked. "+
			"Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithArray("add", mcpgo.Description("Blocker IDs to add (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithArray("remove", mcpgo.Description("Blocker IDs to remove (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		add := getUintSlice(req, "add")
		remove := getUintSlice(req, "remove")
		if len(add) == 0 && len(remove) == 0 {
			return errResult(fmt.Errorf("at least one of add or remove must be non-empty")), nil
		}
		if len(add) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("add: max %d per call", maxBulkMCPIDs)), nil
		}
		if len(remove) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("remove: max %d per call", maxBulkMCPIDs)), nil
		}
		task, err := s.UpdateBlockers(ctx, id, add, remove)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})

	// set_task_archived
	srv.AddTool(mcpgo.NewTool("set_task_archived",
		mcpgo.WithDescription("Set the archived flag on one or more tasks (1..100 IDs). "+
			"Atomic across the entire array: archiving cascades to each task's subtask tree, "+
			"and any rejection (e.g. an affected task blocks an external task while archiving) "+
			"rolls back the whole batch — no partial commit. "+
			"Cross-input blockers are permitted: if A blocks B and both are in the call, the "+
			"external-blocker check treats the union of all input subtrees as the 'set' so the "+
			"call succeeds. Unarchiving cleans up stale blocker rows (blockers that are now "+
			"Done or archived) and re-Unblocked tasks whose blocker count drops to zero. "+
			"Empty `ids` arrays are rejected client-side. "+
			"Returns full detail for each input ID in input order (duplicates collapsed to "+
			"first occurrence)."),
		mcpgo.WithArray("ids", mcpgo.Required(), mcpgo.Description("Task IDs (max 100)"), mcpgo.WithNumberItems(mcpgo.Min(1)), mcpgo.MaxItems(100)),
		mcpgo.WithBoolean("archived", mcpgo.Required(), mcpgo.Description("Target archive state: true to archive, false to unarchive")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		ids, err := requireUintSlice(req, "ids")
		if err != nil {
			return errResult(err), nil
		}
		if len(ids) > maxBulkMCPIDs {
			return errResult(fmt.Errorf("ids: max %d per call", maxBulkMCPIDs)), nil
		}
		archived := getBool(req, "archived")
		details, err := s.BulkSetArchived(ctx, ids, archived)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(details)), nil
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
		mcpgo.WithDescription("Set or clear a task's parent. Pass parent_id to make this task a subtask "+
			"of that parent (validates no cycles). Omit parent_id to make the task top-level. "+
			"Returns full task detail. Empty descriptions are omitted from the JSON response."),
		mcpgo.WithNumber("task_id", mcpgo.Required(), mcpgo.Description("Task ID"), mcpgo.Min(1)),
		mcpgo.WithNumber("parent_id", mcpgo.Description("Parent task ID. Omit to unparent (make top-level)."), mcpgo.Min(1)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		id, err := requireUint(req, "task_id")
		if err != nil {
			return errResult(err), nil
		}
		parentID := getOptUint(req, "parent_id")
		if err := s.SetParent(ctx, id, parentID); err != nil {
			return errResult(err), nil
		}
		task, err := s.GetTask(ctx, id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			return errResult(err), nil
		}
		return textResult(toJSON(task)), nil
	})
}
