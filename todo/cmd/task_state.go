package cmd

import (
	"fmt"

	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var taskStateCmd = &cobra.Command{
	Use:   "state <id> <state>",
	Short: "Set a task's state (New, Progressing, Done)",
	Long: `Set a task's state.

Blocked is not directly settable — use 'todo task block' / 'todo task unblock'
or the MCP update_blockers tool. Unblocked is an auto-transition that fires
when a Blocked task's last blocker is removed; set Progressing or New instead.

Done is terminal and always clears the task's blocker rows (auto-unblocking
dependents whose blocker counts hit zero). Transitioning a Blocked task to
a non-Done state is rejected by default to prevent silent loss of
dependency information; pass --force-clear-blockers to drop the blockers
as part of the transition.`,
	Args: cobra.ExactArgs(2),
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

		force, _ := cmd.Flags().GetBool("force-clear-blockers")
		task, err := s.SetTaskState(cmd.Context(), id, state, store.SetTaskStateOptions{
			ForceClearBlockers: force,
		})
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
	taskStateCmd.Flags().Bool("force-clear-blockers", false,
		"drop outstanding blocker rows when transitioning a Blocked task to a non-Done state")
	taskCmd.AddCommand(taskStateCmd)
}
