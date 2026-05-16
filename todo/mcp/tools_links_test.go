package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
)

// seedLink helper: creates a task and a link on it, returns both IDs.
func seedLink(t *testing.T, s interface {
	CreateTask(ctx context.Context, opts store.CreateTaskOptions) (*model.Task, error)
	AddLink(ctx context.Context, taskID uint, linkType model.LinkType, url, description string) (*model.Link, error)
}) (taskID, linkID uint) {
	t.Helper()
	task, err := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	link, err := s.AddLink(context.Background(), task.ID, model.LinkURL, "https://example.com", "desc")
	if err != nil {
		t.Fatalf("add link: %v", err)
	}
	return task.ID, link.ID
}

// TestUpdateLink_RejectsExplicitEmptyType — the previous handler treated
// `type: ""` as "leave the field alone." That silent no-op masked a
// real caller intent ("clear the type"), which the store layer
// correctly refuses. The new contract: explicit empty is an error;
// omit the key to leave the field unchanged.
func TestUpdateLink_RejectsExplicitEmptyType(t *testing.T) {
	c, s := newMCPTestClient(t)
	taskID, linkID := seedLink(t, s)

	res := callTool(t, c, "update_link", map[string]any{
		"task_id": float64(taskID),
		"link_id": float64(linkID),
		"type":    "",
	})
	if !res.IsError {
		t.Fatalf("expected error for explicit empty type; got: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "type cannot be cleared") {
		t.Errorf("error should explain the cleared-not-allowed rule; got %q", resultText(t, res))
	}

	// State unchanged: the original link still has its type.
	links, _ := s.ListLinks(context.Background(), taskID)
	if len(links) != 1 || links[0].Type != model.LinkURL {
		t.Errorf("rejection should preserve original link state; got %+v", links)
	}
}

func TestUpdateLink_RejectsExplicitEmptyURL(t *testing.T) {
	c, s := newMCPTestClient(t)
	taskID, linkID := seedLink(t, s)

	res := callTool(t, c, "update_link", map[string]any{
		"task_id": float64(taskID),
		"link_id": float64(linkID),
		"url":     "",
	})
	if !res.IsError {
		t.Fatalf("expected error for explicit empty url; got: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "url cannot be cleared") {
		t.Errorf("error should explain the cleared-not-allowed rule; got %q", resultText(t, res))
	}
}

// TestUpdateLink_OmittingKeysLeavesFieldsUnchanged — the absent-key form
// is still the way to say "leave this field alone." Changing only the
// description must not touch type or url.
func TestUpdateLink_OmittingKeysLeavesFieldsUnchanged(t *testing.T) {
	c, s := newMCPTestClient(t)
	taskID, linkID := seedLink(t, s)

	res := callTool(t, c, "update_link", map[string]any{
		"task_id":     float64(taskID),
		"link_id":     float64(linkID),
		"description": "new description",
	})
	if res.IsError {
		t.Fatalf("update_link errored: %s", resultText(t, res))
	}

	links, _ := s.ListLinks(context.Background(), taskID)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	got := links[0]
	if got.Type != model.LinkURL {
		t.Errorf("type changed: got %q, want LinkURL", got.Type)
	}
	if got.URL != "https://example.com" {
		t.Errorf("url changed: got %q", got.URL)
	}
	if got.Description != "new description" {
		t.Errorf("description = %q, want updated", got.Description)
	}
}

// TestUpdateLink_DescriptionCanBeExplicitlyCleared — description is the
// one clearable field; an explicit `description: ""` must succeed.
func TestUpdateLink_DescriptionCanBeExplicitlyCleared(t *testing.T) {
	c, s := newMCPTestClient(t)
	taskID, linkID := seedLink(t, s)

	res := callTool(t, c, "update_link", map[string]any{
		"task_id":     float64(taskID),
		"link_id":     float64(linkID),
		"description": "",
	})
	if res.IsError {
		t.Fatalf("clearing description should succeed; got: %s", resultText(t, res))
	}

	links, _ := s.ListLinks(context.Background(), taskID)
	if got := links[0].Description; got != "" {
		t.Errorf("description = %q, want empty (cleared)", got)
	}
}

// TestAddLink_InvalidTypeRejected — mcp-go does not enforce Enum constraints
// server-side; the handler is the primary validation gate.
func TestAddLink_InvalidTypeRejected(t *testing.T) {
	c, s := newMCPTestClient(t)
	task, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "t"})
	res := callTool(t, c, "add_link", map[string]any{
		"task_id": float64(task.ID),
		"type":    "invalid",
		"url":     "https://example.com",
	})
	if !res.IsError {
		t.Fatalf("expected error for invalid type; got: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "invalid link type") {
		t.Errorf("error text = %q; want substring 'invalid link type'", resultText(t, res))
	}
}

// TestUpdateLink_InvalidTypeRejected — empty-string type is caught first ("cannot be
// cleared"); a non-empty invalid type reaches the new ValidLinkTypes check.
func TestUpdateLink_InvalidTypeRejected(t *testing.T) {
	c, s := newMCPTestClient(t)
	taskID, linkID := seedLink(t, s)
	res := callTool(t, c, "update_link", map[string]any{
		"task_id": float64(taskID),
		"link_id": float64(linkID),
		"type":    "invalid",
	})
	if !res.IsError {
		t.Fatalf("expected error for invalid type; got: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "invalid link type") {
		t.Errorf("error text = %q; want substring 'invalid link type'", resultText(t, res))
	}
}
