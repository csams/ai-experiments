package cmd

import (
	"fmt"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		opts := store.ListTasksOptions{}
		opts.IncludeArchived, _ = cmd.Flags().GetBool("all")
		opts.IncludeSubtasks, _ = cmd.Flags().GetBool("subtasks")
		opts.Overdue, _ = cmd.Flags().GetBool("overdue")
		opts.SortBy, _ = cmd.Flags().GetString("sort")
		opts.Tags, _ = cmd.Flags().GetStringSlice("tag")
		opts.TagsSubsetOf, _ = cmd.Flags().GetStringSlice("tag-subset-of")

		if stateStr, _ := cmd.Flags().GetString("state"); stateStr != "" {
			state, err := normalizeState(stateStr)
			if err != nil {
				return err
			}
			opts.State = &state
		}

		if parentStr, _ := cmd.Flags().GetString("parent"); parentStr != "" {
			pid, err := parseTaskID(parentStr)
			if err != nil {
				return err
			}
			opts.ParentID = &pid
		}

		// Due date presence filters
		hasDue := cmd.Flags().Changed("has-due-date")
		noDue := cmd.Flags().Changed("no-due-date")
		if hasDue && noDue {
			return fmt.Errorf("--has-due-date and --no-due-date are mutually exclusive")
		}
		if hasDue {
			v := true
			opts.HasDueDate = &v
		} else if noDue {
			v := false
			opts.HasDueDate = &v
		}

		// Due date range filters
		if s, _ := cmd.Flags().GetString("due-before"); s != "" {
			opts.DueBefore, err = parseDate(s)
			if err != nil {
				return err
			}
		}
		if s, _ := cmd.Flags().GetString("due-after"); s != "" {
			opts.DueAfter, err = parseDate(s)
			if err != nil {
				return err
			}
		}
		if s, _ := cmd.Flags().GetString("due-on"); s != "" {
			opts.DueOn, err = parseDate(s)
			if err != nil {
				return err
			}
		}

		// Priority range filters
		if cmd.Flags().Changed("priority-min") {
			v, _ := cmd.Flags().GetInt("priority-min")
			opts.PriorityMin = &v
		}
		if cmd.Flags().Changed("priority-max") {
			v, _ := cmd.Flags().GetInt("priority-max")
			opts.PriorityMax = &v
		}

		tasks, err := s.ListTasks(cmd.Context(), opts)
		if err != nil {
			return err
		}

		// If state filter is Blocked, also show what's blocking each task
		if opts.State != nil && *opts.State == model.StateBlocked {
			// Enhance with blocker info if not in JSON mode
			// (JSON mode uses the raw task list)
		}

		outputTaskList(tasks)
		return nil
	},
}

func init() {
	taskListCmd.Flags().Bool("all", false, "include archived tasks")
	taskListCmd.Flags().Bool("subtasks", false, "include subtasks (flat list)")
	taskListCmd.Flags().Bool("overdue", false, "only overdue tasks")
	taskListCmd.Flags().String("state", "", "filter by state")
	taskListCmd.Flags().String("parent", "", "filter to subtree of task ID")
	taskListCmd.Flags().String("sort", "priority", "sort by: priority, due, created, updated")
	taskListCmd.Flags().StringSlice("tag", nil, "filter by tag (AND logic, repeatable)")
	taskListCmd.Flags().Bool("has-due-date", false, "only tasks with a due date")
	taskListCmd.Flags().Bool("no-due-date", false, "only tasks without a due date")
	taskListCmd.Flags().String("due-before", "", "tasks due before this date (YYYY-MM-DD, exclusive)")
	taskListCmd.Flags().String("due-after", "", "tasks due after this date (YYYY-MM-DD, exclusive)")
	taskListCmd.Flags().String("due-on", "", "tasks due on this date (YYYY-MM-DD)")
	taskListCmd.Flags().Int("priority-min", 0, "minimum priority value (inclusive)")
	taskListCmd.Flags().Int("priority-max", 0, "maximum priority value (inclusive)")
	taskListCmd.Flags().StringSlice("tag-subset-of", nil, "task tags must be within this set (repeatable)")
	taskCmd.AddCommand(taskListCmd)
}
