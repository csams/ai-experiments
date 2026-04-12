package cmd

import (
	"fmt"

	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search tasks and notes",
}

var searchSemanticCmd = &cobra.Command{
	Use:   "semantic <query>",
	Short: "Semantic search across tasks and notes using vector similarity",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		ss := getSemanticSearcher()
		if ss == nil {
			return fmt.Errorf("semantic search is not configured (enable vector sync in config)")
		}

		typeFilter, _ := cmd.Flags().GetString("type")
		taskStr, _ := cmd.Flags().GetString("task")
		limit, _ := cmd.Flags().GetInt("limit")
		includeArchived, _ := cmd.Flags().GetBool("include-archived")

		opts := store.SemanticSearchOptions{
			Limit:           limit,
			Type:            typeFilter,
			IncludeArchived: includeArchived,
		}
		if taskStr != "" {
			tid, err := parseTaskID(taskStr)
			if err != nil {
				return err
			}
			opts.TaskID = &tid
		}

		results, err := ss.SemanticSearch(cmd.Context(), args[0], opts)
		if err != nil {
			return err
		}

		outputSemanticResults(results)
		return nil
	},
}

var searchContextCmd = &cobra.Command{
	Use:   "context <task-id>",
	Short: "Find items semantically related to a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		ss := getSemanticSearcher()
		if ss == nil {
			return fmt.Errorf("semantic search is not configured (enable vector sync in config)")
		}

		tid, err := parseTaskID(args[0])
		if err != nil {
			return err
		}

		limit, _ := cmd.Flags().GetInt("limit")
		typeFilter, _ := cmd.Flags().GetString("type")
		includeArchived, _ := cmd.Flags().GetBool("include-archived")

		results, err := ss.SemanticSearchContext(cmd.Context(), tid, store.SemanticSearchOptions{
			Limit:           limit,
			Type:            typeFilter,
			IncludeArchived: includeArchived,
		})
		if err != nil {
			return err
		}

		outputSemanticResults(results)
		return nil
	},
}

func outputSemanticResults(results []store.SemanticSearchResult) {
	if jsonOutput {
		outputJSON(results)
		return
	}
	if len(results) == 0 {
		fmt.Println("No results found.")
		return
	}
	for _, r := range results {
		docType := "?"
		if t, ok := r.Metadata["type"].(string); ok {
			docType = t
		}
		text := truncate(r.Text, 80)
		fmt.Printf("[%.3f] %s %s: %s\n", r.Score, docType, r.ID, text)
	}
}

func init() {
	searchSemanticCmd.Flags().String("type", "", "filter by type: task, note")
	searchSemanticCmd.Flags().String("task", "", "filter to a specific task's entities")
	searchSemanticCmd.Flags().Int("limit", 10, "max results")
	searchSemanticCmd.Flags().Bool("include-archived", false, "include archived tasks/notes")

	searchContextCmd.Flags().Int("limit", 10, "max results")
	searchContextCmd.Flags().String("type", "", "filter by type: task, note")
	searchContextCmd.Flags().Bool("include-archived", false, "include archived tasks/notes")

	searchCmd.AddCommand(searchSemanticCmd)
	searchCmd.AddCommand(searchContextCmd)
	rootCmd.AddCommand(searchCmd)
}
