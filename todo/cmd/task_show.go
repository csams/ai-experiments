package cmd

import (
	"github.com/spf13/cobra"
)

var taskShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show full task details",
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

		detail, err := s.GetTask(id)
		if err != nil {
			return err
		}

		outputTaskDetail(detail)
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskShowCmd)
}
