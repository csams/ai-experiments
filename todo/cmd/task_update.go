package cmd

import (
	"fmt"
	"time"

	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var taskUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a task's title, description, priority, or due date",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()

		id, err := parseTaskID(args[0])
		if err != nil {
			return err
		}

		opts := store.UpdateTaskOptions{}

		if cmd.Flags().Changed("title") {
			v, _ := cmd.Flags().GetString("title")
			opts.Title = &v
		}
		if cmd.Flags().Changed("description") {
			v, _ := cmd.Flags().GetString("description")
			opts.Description = &v
		}
		if cmd.Flags().Changed("priority") {
			v, _ := cmd.Flags().GetInt("priority")
			opts.Priority = &v
		}
		if cmd.Flags().Changed("clear-due") {
			opts.ClearDueAt = true
		} else if cmd.Flags().Changed("due") {
			dueStr, _ := cmd.Flags().GetString("due")
			t, err := time.Parse("2006-01-02", dueStr)
			if err != nil {
				return fmt.Errorf("invalid date %q (use YYYY-MM-DD): %w", dueStr, err)
			}
			utc := t.UTC()
			opts.DueAt = &utc
		}

		task, err := s.UpdateTask(id, opts)
		if err != nil {
			return err
		}

		outputTaskUpdated(task)
		return nil
	},
}

func init() {
	taskUpdateCmd.Flags().String("title", "", "new title")
	taskUpdateCmd.Flags().StringP("description", "d", "", "new description")
	taskUpdateCmd.Flags().IntP("priority", "p", 0, "new priority")
	taskUpdateCmd.Flags().String("due", "", "new due date (YYYY-MM-DD)")
	taskUpdateCmd.Flags().Bool("clear-due", false, "remove due date")
	taskCmd.AddCommand(taskUpdateCmd)
}
