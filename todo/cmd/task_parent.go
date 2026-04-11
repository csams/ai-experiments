package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var taskParentCmd = &cobra.Command{
	Use:   "parent <id> <parent-id>",
	Short: "Make a task a subtask of another",
	Args:  cobra.ExactArgs(2),
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
		parentID, err := parseTaskID(args[1])
		if err != nil {
			return err
		}

		if err := s.SetParent(id, &parentID); err != nil {
			return err
		}

		fmt.Printf("Task %d is now a subtask of %d\n", id, parentID)
		return nil
	},
}

var taskUnparentCmd = &cobra.Command{
	Use:   "unparent <id>",
	Short: "Make a task top-level",
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

		if err := s.SetParent(id, nil); err != nil {
			return err
		}

		fmt.Printf("Task %d is now top-level\n", id)
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskParentCmd)
	taskCmd.AddCommand(taskUnparentCmd)
}
