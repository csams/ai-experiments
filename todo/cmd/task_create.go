package cmd

import (
	"encoding/json"
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

		parentFlag, _ := cmd.Flags().GetUint("parent")
		var parentID *uint
		if parentFlag > 0 {
			p := parentFlag
			parentID = &p
		}

		task, err := s.CreateTask(cmd.Context(), store.CreateTaskOptions{
			Title:       title,
			Description: desc,
			Priority:    priority,
			DueAt:       dueAt,
			Tags:        tags,
			Links:       links,
			ParentID:    parentID,
		})
		if err != nil {
			return err
		}

		return outputTask(task)
	},
}

// parseLinkFlags converts repeatable --link values into []model.LinkInput.
//
// Two forms are accepted per flag:
//
//  1. JSON object — when the trimmed value's first char is '{':
//
//     --link '{"type":"jira","url":"https://x","description":"text, with, commas"}'
//
//     Use this when the description contains commas or other characters
//     that the key=value form doesn't handle. Field names match
//     model.LinkInput's json tags: "type", "url", "description". Note
//     the JSON form does NOT accept the `desc` short alias the
//     key=value form allows — use `description` here.
//
//  2. Legacy key=value list — comma-separated:
//
//     --link "type=<jira|pr|url>,url=<URL>[,desc=<text>]"
//
//     Accepted keys (lowercase): type, url, desc, description. Keys and
//     values are whitespace-trimmed. Duplicate keys within one value are
//     an error. Comma-in-description is NOT supported in this form;
//     switch to the JSON form for that case.
//
// Both forms require type and url to be non-empty.
func parseLinkFlags(flags []string) ([]model.LinkInput, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	out := make([]model.LinkInput, 0, len(flags))
	for _, raw := range flags {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, fmt.Errorf("empty --link value")
		}
		var in model.LinkInput
		var err error
		if trimmed[0] == '{' {
			in, err = parseLinkJSON(trimmed)
		} else {
			in, err = parseLinkKV(raw)
		}
		if err != nil {
			return nil, err
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

// parseLinkJSON decodes a single --link value as a JSON object.
// Unknown fields are an error (DisallowUnknownFields) so a typo in
// "desription" doesn't get silently discarded.
func parseLinkJSON(raw string) (model.LinkInput, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var in model.LinkInput
	if err := dec.Decode(&in); err != nil {
		return model.LinkInput{}, fmt.Errorf("--link JSON decode: %w", err)
	}
	// Reject trailing data after the closing brace — `{"type":"url",...} junk`.
	if dec.More() {
		return model.LinkInput{}, fmt.Errorf("--link JSON has trailing content after object")
	}
	return in, nil
}

// parseLinkKV decodes the legacy comma-separated key=value form.
func parseLinkKV(raw string) (model.LinkInput, error) {
	seen := map[string]bool{}
	var in model.LinkInput
	for _, segment := range strings.Split(raw, ",") {
		eq := strings.IndexByte(segment, '=')
		if eq < 0 {
			return model.LinkInput{}, fmt.Errorf("--link segment %q has no '='", strings.TrimSpace(segment))
		}
		key := strings.TrimSpace(segment[:eq])
		val := strings.TrimSpace(segment[eq+1:])
		if seen[key] {
			return model.LinkInput{}, fmt.Errorf("--link has duplicate key %q", key)
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
			return model.LinkInput{}, fmt.Errorf("--link has unknown key %q (valid: type, url, desc)", key)
		}
	}
	return in, nil
}

func init() {
	taskCreateCmd.Flags().StringP("description", "d", "", "task description")
	taskCreateCmd.Flags().IntP("priority", "p", 0, "priority (lower = more important)")
	taskCreateCmd.Flags().String("due", "", "due date (YYYY-MM-DD)")
	taskCreateCmd.Flags().StringSlice("tag", nil, "tags (repeatable)")
	taskCreateCmd.Flags().Uint("parent", 0, "create as a subtask of this parent task ID")
	taskCreateCmd.Flags().StringArray("link", nil,
		"attach a link (repeatable). Two forms accepted: "+
			"key=value list — `type=<jira|pr|url>,url=<URL>[,desc=<text>]`; or "+
			"JSON object — `{\"type\":\"...\", \"url\":\"...\", \"description\":\"text, with, commas\"}` (use this when description contains commas).")
	taskCmd.AddCommand(taskCreateCmd)
}
