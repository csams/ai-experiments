package cmd

import (
	"fmt"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
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

		description, _ := cmd.Flags().GetString("description")

		link, err := s.AddLink(cmd.Context(), taskID, linkType, url, description)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(link)
		}
		if link.Description != "" {
			fmt.Printf("Added link #%d [%s] %s — %s to task %d\n", link.ID, link.Type, link.URL, link.Description, taskID)
		} else {
			fmt.Printf("Added link #%d [%s] %s to task %d\n", link.ID, link.Type, link.URL, taskID)
		}
		return nil
	},
}

var linkUpdateCmd = &cobra.Command{
	Use:   "update <task-id> <link-id>",
	Short: "Update a link's type, url, and/or description",
	Long: `Update a link's type, url, and/or description.

Omit a flag to leave the field unchanged. --description="" clears the description.
URL and type cannot be cleared (validation rejects empty values).`,
	Args: cobra.ExactArgs(2),
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

		opts := store.UpdateLinkOptions{}
		if cmd.Flags().Changed("type") {
			typeStr, _ := cmd.Flags().GetString("type")
			lt := model.LinkType(typeStr)
			if !model.ValidLinkTypes[lt] {
				return fmt.Errorf("invalid link type %q (valid: jira, pr, url)", typeStr)
			}
			opts.Type = &lt
		}
		if cmd.Flags().Changed("url") {
			urlStr, _ := cmd.Flags().GetString("url")
			opts.URL = &urlStr
		}
		if cmd.Flags().Changed("description") {
			descStr, _ := cmd.Flags().GetString("description")
			opts.Description = &descStr
		}

		link, err := s.UpdateLink(cmd.Context(), taskID, linkID, opts)
		if err != nil {
			return err
		}

		if jsonOutput {
			return outputJSON(link)
		}
		if link.Description != "" {
			fmt.Printf("Updated link #%d [%s] %s — %s\n", link.ID, link.Type, link.URL, link.Description)
		} else {
			fmt.Printf("Updated link #%d [%s] %s\n", link.ID, link.Type, link.URL)
		}
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
	linkAddCmd.Flags().StringP("description", "d", "", "optional description (max 1000 chars)")
	linkUpdateCmd.Flags().String("type", "", "new link type: jira, pr, url")
	linkUpdateCmd.Flags().String("url", "", "new URL (max 2000 bytes)")
	linkUpdateCmd.Flags().StringP("description", "d", "", "new description (max 1000 chars; empty string clears it)")
	linkCmd.AddCommand(linkAddCmd)
	linkCmd.AddCommand(linkListCmd)
	linkCmd.AddCommand(linkUpdateCmd)
	linkCmd.AddCommand(linkDeleteCmd)
	rootCmd.AddCommand(linkCmd)
}
