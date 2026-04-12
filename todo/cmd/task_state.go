package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var taskStateCmd = &cobra.Command{
	Use:   "state <id> <state>",
	Short: "Set a task's state (New, Progressing, Unblocked, Done)",
	Args:  cobra.ExactArgs(2),
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

		state, err := normalizeState(args[1])
		if err != nil {
			return err
		}

		task, err := s.SetTaskState(cmd.Context(), id, state)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(task)
		}
		fmt.Printf("Task %d → %s\n", task.ID, task.State)
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskStateCmd)
}
