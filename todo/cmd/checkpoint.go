package cmd

import (
	"fmt"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var checkpointCmd = &cobra.Command{
	Use:     "checkpoint",
	Aliases: []string{"cp"},
	Short:   "Manage per-task 'resume here' checkpoints (singleton bookmark)",
}

var checkpointSetCmd = &cobra.Command{
	Use:   "set <task-id>",
	Short: "Create or replace the checkpoint on a task",
	Long: `Create or replace the singleton 'resume here' checkpoint on a task.

Each task has at most one checkpoint. Calling set again replaces the prior contents.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		taskID, err := parseTaskID(args[0])
		if err != nil {
			return err
		}
		recap, _ := cmd.Flags().GetString("recap")
		next, _ := cmd.Flags().GetString("next")
		open, _ := cmd.Flags().GetString("open-threads")

		cp, err := s.SetCheckpoint(cmd.Context(), taskID, store.SetCheckpointOptions{
			Recap:       recap,
			NextSteps:   next,
			OpenThreads: open,
		})
		if err != nil {
			return err
		}
		if jsonOutput {
			return outputJSON(cp)
		}
		fmt.Printf("Set checkpoint on task %d\n", taskID)
		return nil
	},
}

var checkpointShowCmd = &cobra.Command{
	Use:   "show <task-id>",
	Short: "Show a task's checkpoint",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		taskID, err := parseTaskID(args[0])
		if err != nil {
			return err
		}
		cp, err := s.GetCheckpoint(cmd.Context(), taskID)
		if err != nil {
			return err
		}
		if jsonOutput {
			return outputJSON(cp)
		}
		printCheckpoint(taskID, cp)
		return nil
	},
}

var checkpointDeleteCmd = &cobra.Command{
	Use:   "delete <task-id>",
	Short: "Delete a task's checkpoint",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		taskID, err := parseTaskID(args[0])
		if err != nil {
			return err
		}
		if err := s.DeleteCheckpoint(cmd.Context(), taskID); err != nil {
			return err
		}
		fmt.Printf("Deleted checkpoint on task %d\n", taskID)
		return nil
	},
}

func printCheckpoint(taskID uint, cp *model.Checkpoint) {
	fmt.Printf("Checkpoint for task %d (updated %s):\n", taskID, cp.UpdatedAt.Format("2006-01-02 15:04"))
	fmt.Printf("  Recap: %s\n", cp.Recap)
	fmt.Printf("  Next:  %s\n", cp.NextSteps)
	if cp.OpenThreads != "" {
		fmt.Printf("  Open:  %s\n", cp.OpenThreads)
	}
}

func init() {
	checkpointSetCmd.Flags().String("recap", "", "short recap of what was worked on (required, max 10000 chars)")
	checkpointSetCmd.Flags().String("next", "", "next step to pick up (required, max 10000 chars)")
	checkpointSetCmd.Flags().String("open-threads", "", "optional: deferred or tangential threads (max 10000 chars)")
	_ = checkpointSetCmd.MarkFlagRequired("recap")
	_ = checkpointSetCmd.MarkFlagRequired("next")

	checkpointCmd.AddCommand(checkpointSetCmd)
	checkpointCmd.AddCommand(checkpointShowCmd)
	checkpointCmd.AddCommand(checkpointDeleteCmd)
	rootCmd.AddCommand(checkpointCmd)
}
