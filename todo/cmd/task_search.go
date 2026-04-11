package cmd

import (
	"github.com/spf13/cobra"
)

var taskSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search tasks by title and description",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()

		tasks, err := s.SearchTasks(args[0])
		if err != nil {
			return err
		}

		outputTaskList(tasks)
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskSearchCmd)
}
