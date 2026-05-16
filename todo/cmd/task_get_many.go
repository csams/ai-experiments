package cmd

import (
	"fmt"
	"strings"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var taskGetManyCmd = &cobra.Command{
	Use:   "get-many <id> [<id>...]",
	Short: "Fetch multiple tasks in one call (max 100)",
	Long: "Batch-fetch multiple tasks in one call. Output preserves the order of the " +
		"given IDs and collapses duplicates to first occurrence. IDs with no matching " +
		"task are reported separately rather than failing the call.",
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		ids, err := parseIDList(args)
		if err != nil {
			return err
		}

		result, err := s.GetTasks(cmd.Context(), ids, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(result)
		}

		for i := range result.Tasks {
			if i > 0 {
				fmt.Println()
			}
			if err := outputTaskDetail(&result.Tasks[i]); err != nil {
				return err
			}
		}
		if len(result.NotFound) > 0 {
			parts := make([]string, len(result.NotFound))
			for i, id := range result.NotFound {
				parts[i] = fmt.Sprintf("%d", id)
			}
			fmt.Printf("Not found: %s\n", strings.Join(parts, ", "))
		}
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskGetManyCmd)
}
