package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var vectorCmd = &cobra.Command{
	Use:   "vector",
	Short: "Vector store management",
}

var vectorReindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Reindex all tasks and notes into the vector store",
	RunE: func(cmd *cobra.Command, args []string) error {
		syncer := getVectorSyncer()
		if syncer == nil {
			return fmt.Errorf("vector sync is not configured (enable in config)")
		}

		clear, _ := cmd.Flags().GetBool("clear")

		err := syncer.Reindex(cmd.Context(), clear, func(done, total int) {
			fmt.Printf("\rEmbedded %d/%d documents...", done, total)
		})
		if err != nil {
			return err
		}

		fmt.Println("\nReindex complete.")
		return nil
	},
}

func init() {
	vectorReindexCmd.Flags().Bool("clear", false, "drop and recreate collection (required on embedder change)")
	vectorCmd.AddCommand(vectorReindexCmd)
	rootCmd.AddCommand(vectorCmd)
}
