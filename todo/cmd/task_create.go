package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var taskCreateCmd = &cobra.Command{
	Use:   "create <title>",
	Short: "Create a new task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()

		title := args[0]
		desc, _ := cmd.Flags().GetString("description")
		priority, _ := cmd.Flags().GetInt("priority")
		dueStr, _ := cmd.Flags().GetString("due")
		tags, _ := cmd.Flags().GetStringSlice("tag")

		var dueAt *time.Time
		if dueStr != "" {
			t, err := time.Parse("2006-01-02", dueStr)
			if err != nil {
				return fmt.Errorf("invalid date %q (use YYYY-MM-DD): %w", dueStr, err)
			}
			utc := t.UTC()
			dueAt = &utc
		}

		task, err := s.CreateTask(title, desc, priority, dueAt, tags)
		if err != nil {
			return err
		}

		outputTask(task)
		return nil
	},
}

func init() {
	taskCreateCmd.Flags().StringP("description", "d", "", "task description")
	taskCreateCmd.Flags().IntP("priority", "p", 0, "priority (lower = more important)")
	taskCreateCmd.Flags().String("due", "", "due date (YYYY-MM-DD)")
	taskCreateCmd.Flags().StringSlice("tag", nil, "tags (repeatable)")
	taskCmd.AddCommand(taskCreateCmd)
}
