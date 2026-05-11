package cmd

import (
	"github.com/csams/todo/model"
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
		defer s.Close(cmd.Context())

		tasks, err := s.SearchTasks(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		items := make([]model.TaskListItem, len(tasks))
		for i := range tasks {
			items[i] = model.TaskListItem{Task: tasks[i]}
		}
		outputTaskList(items)
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskSearchCmd)
}
