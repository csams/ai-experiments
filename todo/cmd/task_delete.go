package cmd

import (
	"fmt"

	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var taskDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a task (notes are kept as standalone unless --delete-notes is set)",
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
		deleteNotes, _ := cmd.Flags().GetBool("delete-notes")

		opts := store.DeleteTaskOptions{
			Recursive:   recursive,
			DeleteNotes: deleteNotes,
		}
		if err := s.DeleteTask(cmd.Context(), id, opts); err != nil {
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
	taskDeleteCmd.Flags().Bool("delete-notes", false, "hard-delete the task's notes instead of orphaning them")
	taskCmd.AddCommand(taskDeleteCmd)
}
