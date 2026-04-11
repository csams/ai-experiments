package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/csams/todo/model"
	"github.com/spf13/cobra"
)

// parseTaskID parses a task ID from a positional argument string.
func parseTaskID(arg string) (uint, error) {
	id, err := strconv.ParseUint(arg, 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid task ID: %q (must be a positive integer)", arg)
	}
	return uint(id), nil
}

// parseUint parses an arbitrary uint from a string.
func parseUint(arg string) (uint, error) {
	id, err := strconv.ParseUint(arg, 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid ID: %q (must be a positive integer)", arg)
	}
	return uint(id), nil
}

// outputJSON writes v as indented JSON to stdout.
func outputJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// outputTask prints a task in human-readable format.
func outputTask(task *model.Task) {
	if jsonOutput {
		outputJSON(task)
		return
	}
	fmt.Printf("Created task %d\n", task.ID)
}

// outputTaskUpdated prints a brief update confirmation.
func outputTaskUpdated(task *model.Task) {
	if jsonOutput {
		outputJSON(task)
		return
	}
	fmt.Printf("Updated task %d\n", task.ID)
}

// outputTaskList prints a list of tasks as a table.
func outputTaskList(tasks []model.Task) {
	if jsonOutput {
		outputJSON(tasks)
		return
	}
	if len(tasks) == 0 {
		fmt.Println("No tasks found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPRI\tSTATE\tDUE\tPARENT\tTITLE\tTAGS")
	for _, t := range tasks {
		due := ""
		if t.DueAt != nil {
			due = t.DueAt.Format("2006-01-02")
			if t.DueAt.Before(time.Now()) {
				due = due + " !"
			}
		}
		parent := ""
		if t.ParentID != nil {
			parent = fmt.Sprintf("%d", *t.ParentID)
		}
		title := t.Title
		if len(title) > 50 {
			title = title[:47] + "..."
		}
		tags := tagNames(t.Tags)
		archived := ""
		if t.Archived {
			archived = " [archived]"
		}
		fmt.Fprintf(w, "%d\t%d\t%s%s\t%s\t%s\t%s\t%s\n",
			t.ID, t.Priority, t.State, archived, due, parent, title, tags)
	}
	w.Flush()
}

// outputTaskDetail prints a full task detail view.
func outputTaskDetail(detail *model.TaskDetail) {
	if jsonOutput {
		outputJSON(detail)
		return
	}

	t := detail.Task
	fmt.Printf("Task #%d: %s\n", t.ID, t.Title)
	fmt.Printf("  State:    %s\n", t.State)
	fmt.Printf("  Priority: %d\n", t.Priority)
	if t.Archived {
		fmt.Println("  Archived: yes")
	}
	if t.DueAt != nil {
		label := t.DueAt.Format("2006-01-02 15:04 UTC")
		if t.DueAt.Before(time.Now()) {
			label += " (OVERDUE)"
		}
		fmt.Printf("  Due:      %s\n", label)
	}
	if t.ParentID != nil {
		fmt.Printf("  Parent:   #%d\n", *t.ParentID)
	}
	if t.Description != "" {
		fmt.Printf("  Description: %s\n", t.Description)
	}
	fmt.Printf("  Created:  %s\n", t.CreatedAt.Format(time.RFC3339))
	fmt.Printf("  Updated:  %s\n", t.UpdatedAt.Format(time.RFC3339))

	if len(detail.Blockers) > 0 {
		fmt.Println("\n  Blocked by:")
		for _, b := range detail.Blockers {
			fmt.Printf("    #%d %s [%s]\n", b.ID, b.Title, b.State)
		}
	}
	if len(detail.Blocking) > 0 {
		fmt.Println("\n  Blocking:")
		for _, b := range detail.Blocking {
			fmt.Printf("    #%d %s [%s]\n", b.ID, b.Title, b.State)
		}
	}
	if len(t.Children) > 0 {
		fmt.Println("\n  Children:")
		for _, c := range t.Children {
			fmt.Printf("    #%d %s [%s]\n", c.ID, c.Title, c.State)
		}
	}
	if len(t.Tags) > 0 {
		fmt.Printf("\n  Tags: %s\n", tagNames(t.Tags))
	}
	if len(t.Links) > 0 {
		fmt.Println("\n  Links:")
		for _, l := range t.Links {
			fmt.Printf("    [%s] %s\n", l.Type, l.URL)
		}
	}
	if len(t.Notes) > 0 {
		fmt.Println("\n  Notes:")
		for _, n := range t.Notes {
			fmt.Printf("    #%d (%s): %s\n", n.ID, n.CreatedAt.Format("2006-01-02"), truncate(n.Text, 100))
		}
	}
}

// outputNotes prints a list of notes.
func outputNotes(notes []model.Note) {
	if jsonOutput {
		outputJSON(notes)
		return
	}
	if len(notes) == 0 {
		fmt.Println("No notes found.")
		return
	}
	for _, n := range notes {
		fmt.Printf("#%d (task %d, %s): %s\n", n.ID, n.TaskID, n.CreatedAt.Format("2006-01-02"), n.Text)
	}
}

// outputLinks prints a list of links.
func outputLinks(links []model.Link) {
	if jsonOutput {
		outputJSON(links)
		return
	}
	if len(links) == 0 {
		fmt.Println("No links found.")
		return
	}
	for _, l := range links {
		fmt.Printf("#%d [%s] %s\n", l.ID, l.Type, l.URL)
	}
}

func tagNames(tags []model.TaskTag) string {
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Tag
	}
	return strings.Join(names, ", ")
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// normalizeState converts a case-insensitive state string to the canonical TaskState.
func normalizeState(s string) (model.TaskState, error) {
	for state := range model.ValidTaskStates {
		if strings.EqualFold(string(state), s) {
			return state, nil
		}
	}
	return "", fmt.Errorf("invalid state %q (valid: New, Progressing, Blocked, Unblocked, Done)", s)
}

// detectLinkType auto-detects link type from a URL string.
func detectLinkType(url string) model.LinkType {
	upper := strings.ToUpper(url)
	// JIRA pattern: PROJ-123
	if matched := jiraPattern(upper); matched {
		return model.LinkJira
	}
	// GitHub/GitLab PR pattern
	lower := strings.ToLower(url)
	if strings.Contains(lower, "/pull/") || strings.Contains(lower, "/merge_requests/") {
		return model.LinkPR
	}
	return model.LinkURL
}

func jiraPattern(s string) bool {
	// Simple heuristic: uppercase letters, dash, digits (e.g., PROJ-123)
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return false
	}
	for _, c := range parts[0] {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(parts[0]) > 0 && len(parts[1]) > 0
}

// requireArgs validates that at least n args are provided.
func requireArgs(cmd *cobra.Command, args []string, n int) error {
	if len(args) < n {
		return fmt.Errorf("requires at least %d arg(s), got %d", n, len(args))
	}
	return nil
}
