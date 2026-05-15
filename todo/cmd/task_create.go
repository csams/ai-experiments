package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/spf13/cobra"
)

var taskCreateCmd = &cobra.Command{
	Use:   "create <title>",
	Short: "Create a new task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, _, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close(cmd.Context())

		title := args[0]
		desc, _ := cmd.Flags().GetString("description")
		priority, _ := cmd.Flags().GetInt("priority")
		dueStr, _ := cmd.Flags().GetString("due")
		tags, _ := cmd.Flags().GetStringSlice("tag")
		linkFlags, _ := cmd.Flags().GetStringArray("link")

		var dueAt *time.Time
		if dueStr != "" {
			dueAt, err = parseDate(dueStr)
			if err != nil {
				return err
			}
		}

		links, err := parseLinkFlags(linkFlags)
		if err != nil {
			return err
		}

		task, err := s.CreateTask(cmd.Context(), store.CreateTaskOptions{
			Title:       title,
			Description: desc,
			Priority:    priority,
			DueAt:       dueAt,
			Tags:        tags,
			Links:       links,
		})
		if err != nil {
			return err
		}

		outputTask(task)
		return nil
	},
}

// parseLinkFlags converts repeatable --link values into []model.LinkInput.
// Format per flag: "type=<jira|pr|url>,url=<URL>[,desc=<text>]".
// Accepted keys (lowercase only): type, url, desc, description. Keys and
// values are whitespace-trimmed. type and url are required; duplicate keys
// within a single --link value are an error. Comma-in-description is not
// supported (use `todo link add` after creation as a workaround).
func parseLinkFlags(flags []string) ([]model.LinkInput, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	out := make([]model.LinkInput, 0, len(flags))
	for _, raw := range flags {
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("empty --link value")
		}
		seen := map[string]bool{}
		var in model.LinkInput
		for _, segment := range strings.Split(raw, ",") {
			eq := strings.IndexByte(segment, '=')
			if eq < 0 {
				return nil, fmt.Errorf("--link segment %q has no '='", strings.TrimSpace(segment))
			}
			key := strings.TrimSpace(segment[:eq])
			val := strings.TrimSpace(segment[eq+1:])
			if seen[key] {
				return nil, fmt.Errorf("--link has duplicate key %q", key)
			}
			seen[key] = true
			switch key {
			case "type":
				in.Type = model.LinkType(val)
			case "url":
				in.URL = val
			case "desc", "description":
				in.Description = val
			default:
				return nil, fmt.Errorf("--link has unknown key %q (valid: type, url, desc)", key)
			}
		}
		if in.Type == "" {
			return nil, fmt.Errorf("--link missing required key 'type'")
		}
		if in.URL == "" {
			return nil, fmt.Errorf("--link missing required key 'url'")
		}
		out = append(out, in)
	}
	return out, nil
}

func init() {
	taskCreateCmd.Flags().StringP("description", "d", "", "task description")
	taskCreateCmd.Flags().IntP("priority", "p", 0, "priority (lower = more important)")
	taskCreateCmd.Flags().String("due", "", "due date (YYYY-MM-DD)")
	taskCreateCmd.Flags().StringSlice("tag", nil, "tags (repeatable)")
	taskCreateCmd.Flags().StringArray("link", nil,
		"attach a link (repeatable). Format: type=<jira|pr|url>,url=<URL>[,desc=<text>]. "+
			"Comma-in-description is not supported; use the link add subcommand afterward for that case.")
	taskCmd.AddCommand(taskCreateCmd)
}
