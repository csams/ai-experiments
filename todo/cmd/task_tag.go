package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var taskTagCmd = &cobra.Command{
	Use:   "tag <id> <tags...>",
	Short: "Add tags to a task",
	Args:  cobra.MinimumNArgs(2),
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

		if err := s.AddTags(id, args[1:]); err != nil {
			return err
		}

		fmt.Printf("Added tags %v to task %d\n", args[1:], id)
		return nil
	},
}

var taskUntagCmd = &cobra.Command{
	Use:   "untag <id> <tags...>",
	Short: "Remove tags from a task",
	Args:  cobra.MinimumNArgs(2),
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

		if err := s.RemoveTags(id, args[1:]); err != nil {
			return err
		}

		fmt.Printf("Removed tags %v from task %d\n", args[1:], id)
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskTagCmd)
	taskCmd.AddCommand(taskUntagCmd)
}
