package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var taskArchiveCmd = &cobra.Command{
	Use:   "archive <id>",
	Short: "Archive a task and its subtree",
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

		if err := s.ArchiveTask(cmd.Context(), id, true); err != nil {
			return err
		}

		fmt.Printf("Archived task %d (and subtree)\n", id)
		return nil
	},
}

var taskUnarchiveCmd = &cobra.Command{
	Use:   "unarchive <id>",
	Short: "Unarchive a task and its subtree",
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

		if err := s.ArchiveTask(cmd.Context(), id, false); err != nil {
			return err
		}

		fmt.Printf("Unarchived task %d (and subtree)\n", id)
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskArchiveCmd)
	taskCmd.AddCommand(taskUnarchiveCmd)
}
