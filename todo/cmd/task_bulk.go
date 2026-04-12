package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var taskBulkStateCmd = &cobra.Command{
	Use:   "bulk-state <ids...>",
	Short: "Set state on multiple tasks atomically",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		stateStr, _ := cmd.Flags().GetString("state")
		if stateStr == "" {
			return fmt.Errorf("--state is required")
		}
		state, err := normalizeState(stateStr)
		if err != nil {
			return err
		}

		ids, err := parseIDList(args)
		if err != nil {
			return err
		}

		tasks, err := s.BulkUpdateState(cmd.Context(), ids, state)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(tasks)
		}
		fmt.Printf("Updated %d tasks to %s\n", len(tasks), state)
		return nil
	},
}

var taskBulkPriorityCmd = &cobra.Command{
	Use:   "bulk-priority <ids...>",
	Short: "Set priority on multiple tasks atomically",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		priority, err := cmd.Flags().GetInt("priority")
		if err != nil || !cmd.Flags().Changed("priority") {
			return fmt.Errorf("--priority is required")
		}

		ids, err := parseIDList(args)
		if err != nil {
			return err
		}

		tasks, err := s.BulkUpdatePriority(cmd.Context(), ids, priority)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(tasks)
		}
		fmt.Printf("Updated %d tasks to priority %d\n", len(tasks), priority)
		return nil
	},
}

var taskBulkAddTagsCmd = &cobra.Command{
	Use:   "bulk-add-tags <ids...>",
	Short: "Add tags to multiple tasks atomically",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		tagsStr, _ := cmd.Flags().GetString("tags")
		if tagsStr == "" {
			return fmt.Errorf("--tags is required")
		}
		tags := strings.Split(tagsStr, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}

		ids, err := parseIDList(args)
		if err != nil {
			return err
		}

		if err := s.BulkAddTags(cmd.Context(), ids, tags); err != nil {
			return err
		}

		fmt.Printf("Added tags %v to %d tasks\n", tags, len(ids))
		return nil
	},
}

var taskBulkRemoveTagsCmd = &cobra.Command{
	Use:   "bulk-remove-tags <ids...>",
	Short: "Remove tags from multiple tasks atomically",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		tagsStr, _ := cmd.Flags().GetString("tags")
		if tagsStr == "" {
			return fmt.Errorf("--tags is required")
		}
		tags := strings.Split(tagsStr, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}

		ids, err := parseIDList(args)
		if err != nil {
			return err
		}

		if err := s.BulkRemoveTags(cmd.Context(), ids, tags); err != nil {
			return err
		}

		fmt.Printf("Removed tags %v from %d tasks\n", tags, len(ids))
		return nil
	},
}

func parseIDList(args []string) ([]uint, error) {
	ids := make([]uint, 0, len(args))
	for _, a := range args {
		id, err := parseTaskID(a)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func init() {
	taskBulkStateCmd.Flags().String("state", "", "target state (required)")
	taskBulkPriorityCmd.Flags().Int("priority", 0, "target priority (required)")
	taskBulkAddTagsCmd.Flags().String("tags", "", "comma-separated tags to add (required)")
	taskBulkRemoveTagsCmd.Flags().String("tags", "", "comma-separated tags to remove (required)")

	taskCmd.AddCommand(taskBulkStateCmd)
	taskCmd.AddCommand(taskBulkPriorityCmd)
	taskCmd.AddCommand(taskBulkAddTagsCmd)
	taskCmd.AddCommand(taskBulkRemoveTagsCmd)
}
