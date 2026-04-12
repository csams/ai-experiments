package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var taskDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a task",
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

		recursive, _ := cmd.Flags().GetBool("recursive")

		if err := s.DeleteTask(cmd.Context(), id, recursive); err != nil {
			return err
		}

		if recursive {
			fmt.Printf("Deleted task %d and all subtasks\n", id)
		} else {
			fmt.Printf("Deleted task %d\n", id)
		}
		return nil
	},
}

func init() {
	taskDeleteCmd.Flags().Bool("recursive", false, "delete entire subtask tree")
	taskCmd.AddCommand(taskDeleteCmd)
}
