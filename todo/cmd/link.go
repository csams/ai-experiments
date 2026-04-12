package cmd

import (
	"fmt"

	"github.com/csams/todo/model"
	"github.com/spf13/cobra"
)

var linkCmd = &cobra.Command{
	Use:   "link",
	Short: "Manage links (external references)",
}

var linkAddCmd = &cobra.Command{
	Use:   "add <task-id> <url>",
	Short: "Add a link to a task (type auto-detected or --type override)",
	Args:  cobra.ExactArgs(2),
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
		url := args[1]

		typeStr, _ := cmd.Flags().GetString("type")
		var linkType model.LinkType
		if typeStr != "" {
			linkType = model.LinkType(typeStr)
			if !model.ValidLinkTypes[linkType] {
				return fmt.Errorf("invalid link type %q (valid: jira, pr, url)", typeStr)
			}
		} else {
			linkType = detectLinkType(url)
		}

		link, err := s.AddLink(cmd.Context(), taskID, linkType, url)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(link)
		}
		fmt.Printf("Added link #%d [%s] %s to task %d\n", link.ID, link.Type, link.URL, taskID)
		return nil
	},
}

var linkListCmd = &cobra.Command{
	Use:   "list <task-id>",
	Short: "List links for a task",
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

		links, err := s.ListLinks(cmd.Context(), taskID)
		if err != nil {
			return err
		}

		outputLinks(links)
		return nil
	},
}

var linkDeleteCmd = &cobra.Command{
	Use:   "delete <task-id> <link-id>",
	Short: "Delete a link",
	Args:  cobra.ExactArgs(2),
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
		linkID, err := parseUint(args[1])
		if err != nil {
			return err
		}

		if err := s.DeleteLink(cmd.Context(), taskID, linkID); err != nil {
			return err
		}

		fmt.Printf("Deleted link %d from task %d\n", linkID, taskID)
		return nil
	},
}

func init() {
	linkAddCmd.Flags().String("type", "", "link type: jira, pr, url (auto-detected if omitted)")
	linkCmd.AddCommand(linkAddCmd)
	linkCmd.AddCommand(linkListCmd)
	linkCmd.AddCommand(linkDeleteCmd)
	rootCmd.AddCommand(linkCmd)
}
