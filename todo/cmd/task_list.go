package cmd

import (
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
	taskCmd.AddCommand(taskListCmd)
}
