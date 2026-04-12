package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var taskBlockCmd = &cobra.Command{
	Use:   "block <id>",
	Short: "Add blockers to a task (sets state to Blocked)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		id, err := parseTaskID(args[0])
		if err != nil {
			return err
		}

		byStrs, _ := cmd.Flags().GetUintSlice("by")
		if len(byStrs) == 0 {
			return fmt.Errorf("at least one --by <id> is required")
		}

		task, err := s.AddBlockers(cmd.Context(), id, byStrs)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(task)
		}
		fmt.Printf("Task %d blocked by %v\n", task.ID, byStrs)
		return nil
	},
}

var taskUnblockCmd = &cobra.Command{
	Use:   "unblock <id>",
	Short: "Remove specific blockers from a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		id, err := parseTaskID(args[0])
		if err != nil {
			return err
		}

		byStrs, _ := cmd.Flags().GetUintSlice("by")
		if len(byStrs) == 0 {
			return fmt.Errorf("at least one --by <id> is required")
		}

		task, err := s.RemoveBlockers(cmd.Context(), id, byStrs)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(task)
		}
		fmt.Printf("Task %d: removed blockers %v (state: %s)\n", task.ID, byStrs, task.State)
		return nil
	},
}

func init() {
	taskBlockCmd.Flags().UintSlice("by", nil, "blocker task IDs (repeatable)")
	taskUnblockCmd.Flags().UintSlice("by", nil, "blocker task IDs to remove (repeatable)")
	taskCmd.AddCommand(taskBlockCmd)
	taskCmd.AddCommand(taskUnblockCmd)
}
