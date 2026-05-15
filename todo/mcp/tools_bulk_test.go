package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func mustCallToolRequest(name string, args map[string]any) mcpgo.CallToolRequest {
	return mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: name, Arguments: args}}
}

// PR 4 collapses the four bulk_* tools into array-accepting variants on
// add_tags / remove_tags / set_task_state and a new set_task_priority.
// These tests pin the new shapes and the array semantics.

func TestAddTags_AppliesToAllIDs(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()
	a, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "B"})

	res := callTool(t, c, "add_tags", map[string]any{
		"ids":  []any{float64(a.ID), float64(b.ID)},
		"tags": []any{"alpha", "beta"},
	})
	if res.IsError {
		t.Fatalf("add_tags errored: %s", resultText(t, res))
	}

	for _, id := range []uint{a.ID, b.ID} {
		detail, err := s.GetTask(ctx, id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			t.Fatalf("GetTask(%d): %v", id, err)
		}
		got := map[string]bool{}
		for _, tag := range detail.Tags {
			got[tag.Tag] = true
		}
		if !got["alpha"] || !got["beta"] {
			t.Errorf("task %d tags = %v, want alpha+beta", id, detail.Tags)
		}
	}
}

func TestRemoveTags_AppliesToAllIDs(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()
	a, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "A", Tags: []string{"x", "y"}})
	b, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "B", Tags: []string{"x", "z"}})

	res := callTool(t, c, "remove_tags", map[string]any{
		"ids":  []any{float64(a.ID), float64(b.ID)},
		"tags": []any{"x"},
	})
	if res.IsError {
		t.Fatalf("remove_tags errored: %s", resultText(t, res))
	}

	for _, id := range []uint{a.ID, b.ID} {
		detail, _ := s.GetTask(ctx, id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		for _, tag := range detail.Tags {
			if tag.Tag == "x" {
				t.Errorf("task %d still has tag x after remove_tags: %v", id, detail.Tags)
			}
		}
	}
}

func TestSetTaskState_ArrayHappyPath(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()
	a, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "B"})

	res := callTool(t, c, "set_task_state", map[string]any{
		"ids":   []any{float64(a.ID), float64(b.ID)},
		"state": "Progressing",
	})
	if res.IsError {
		t.Fatalf("set_task_state errored: %s", resultText(t, res))
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 tasks returned, got %d", len(arr))
	}

	for _, id := range []uint{a.ID, b.ID} {
		detail, _ := s.GetTask(ctx, id, store.GetTaskOptions{})
		if string(detail.State) != "Progressing" {
			t.Errorf("task %d state = %q, want Progressing", id, detail.State)
		}
	}
}

func TestSetTaskState_RejectsBlocked(t *testing.T) {
	c, s := newMCPTestClient(t)
	a, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "A"})

	res := callTool(t, c, "set_task_state", map[string]any{
		"ids":   []any{float64(a.ID)},
		"state": "Blocked",
	})
	if !res.IsError {
		t.Fatalf("expected schema rejection for Blocked state; got: %s", resultText(t, res))
	}
}

func TestSetTaskPriority_ArrayHappyPath(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()
	a, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "A", Priority: 5})
	b, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "B", Priority: 5})

	res := callTool(t, c, "set_task_priority", map[string]any{
		"ids":      []any{float64(a.ID), float64(b.ID)},
		"priority": float64(-1),
	})
	if res.IsError {
		t.Fatalf("set_task_priority errored: %s", resultText(t, res))
	}

	for _, id := range []uint{a.ID, b.ID} {
		detail, _ := s.GetTask(ctx, id, store.GetTaskOptions{})
		if detail.Priority != -1 {
			t.Errorf("task %d priority = %d, want -1", id, detail.Priority)
		}
	}
}

func TestSetTaskPriority_PromotesBlockers(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()
	blocker, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "blocker", Priority: 10})
	blocked, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "blocked", Priority: 10})
	if _, err := s.AddBlockers(ctx, blocked.ID, []uint{blocker.ID}); err != nil {
		t.Fatalf("setup AddBlockers: %v", err)
	}

	// Bumping the blocked task to higher priority (lower number) should also
	// promote the blocker.
	res := callTool(t, c, "set_task_priority", map[string]any{
		"ids":      []any{float64(blocked.ID)},
		"priority": float64(0),
	})
	if res.IsError {
		t.Fatalf("set_task_priority errored: %s", resultText(t, res))
	}
	blockerDetail, _ := s.GetTask(ctx, blocker.ID, store.GetTaskOptions{})
	if blockerDetail.Priority > 0 {
		t.Errorf("blocker priority should have been promoted to <= 0, got %d", blockerDetail.Priority)
	}
}

func TestBulkTools_RemovedFromRegistry(t *testing.T) {
	c, _ := newMCPTestClient(t)
	for _, name := range []string{"bulk_update_state", "bulk_update_priority", "bulk_add_tags", "bulk_remove_tags"} {
		res, err := c.CallTool(context.Background(), mustCallToolRequest(name, map[string]any{
			"ids":   []any{float64(1)},
			"state": "Done",
		}))
		// A "tool not found" surfaces as a transport error in mcp-go's
		// in-process client. Either err != nil or a structured IsError
		// result is acceptable; what we don't want is a clean success.
		if err == nil && res != nil && !res.IsError {
			t.Errorf("removed tool %q still appears callable", name)
		}
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
			t.Logf("call %q surfaced err=%v (acceptable, just noting)", name, err)
		}
	}
}

func TestSingleIDStillWorks_AddTags(t *testing.T) {
	c, s := newMCPTestClient(t)
	a, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "A"})

	res := callTool(t, c, "add_tags", map[string]any{
		"ids":  []any{float64(a.ID)},
		"tags": []any{"solo"},
	})
	if res.IsError {
		t.Fatalf("add_tags single-id errored: %s", resultText(t, res))
	}
	detail, _ := s.GetTask(context.Background(), a.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	found := false
	for _, tag := range detail.Tags {
		if tag.Tag == "solo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected tag 'solo' on task, got %v", detail.Tags)
	}
}

// --- Boundary and atomicity tests ---

func TestSetTaskState_EmptyIDsRejected(t *testing.T) {
	c, _ := newMCPTestClient(t)
	res := callTool(t, c, "set_task_state", map[string]any{
		"ids":   []any{},
		"state": "Done",
	})
	if !res.IsError {
		t.Fatalf("expected error for empty ids; got: %s", resultText(t, res))
	}
}

func TestAddTags_EmptyTagsRejected(t *testing.T) {
	c, s := newMCPTestClient(t)
	a, _ := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "A"})
	res := callTool(t, c, "add_tags", map[string]any{
		"ids":  []any{float64(a.ID)},
		"tags": []any{},
	})
	if !res.IsError {
		t.Fatalf("expected error for empty tags; got: %s", resultText(t, res))
	}
}

func TestSetTaskState_AtomicOnInvalidID(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()
	a, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "B"})
	const missing = uint(999999)

	res := callTool(t, c, "set_task_state", map[string]any{
		"ids":   []any{float64(a.ID), float64(missing), float64(b.ID)},
		"state": "Done",
	})
	if !res.IsError {
		t.Fatalf("expected error for missing ID; got: %s", resultText(t, res))
	}
	// Cross-array atomicity: neither task A nor task B should have been
	// transitioned, since BulkUpdateState runs in a single transaction.
	for _, id := range []uint{a.ID, b.ID} {
		detail, _ := s.GetTask(ctx, id, store.GetTaskOptions{})
		if string(detail.State) == "Done" {
			t.Errorf("task %d transitioned to Done despite mid-array failure (broken atomicity)", id)
		}
	}
}

func TestSetTaskPriority_HandlesDuplicateIDs(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()
	a, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "A", Priority: 5})

	res := callTool(t, c, "set_task_priority", map[string]any{
		"ids":      []any{float64(a.ID), float64(a.ID), float64(a.ID)},
		"priority": float64(2),
	})
	if res.IsError {
		t.Fatalf("duplicate ids should be handled gracefully; got: %s", resultText(t, res))
	}
	detail, _ := s.GetTask(ctx, a.ID, store.GetTaskOptions{})
	if detail.Priority != 2 {
		t.Errorf("priority = %d, want 2", detail.Priority)
	}
}

func TestSetTaskState_BoundaryHundredIDs(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()

	ids := make([]any, 0, 100)
	for i := 0; i < 100; i++ {
		task, err := s.CreateTask(ctx, store.CreateTaskOptions{Title: "T"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		ids = append(ids, float64(task.ID))
	}

	res := callTool(t, c, "set_task_state", map[string]any{
		"ids":   ids,
		"state": "Done",
	})
	if res.IsError {
		t.Fatalf("100 ids should succeed; got: %s", resultText(t, res))
	}

	// 101 ids must fail.
	extra, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "extra"})
	idsPlus := append(ids, float64(extra.ID))
	res = callTool(t, c, "set_task_state", map[string]any{
		"ids":   idsPlus,
		"state": "Done",
	})
	if !res.IsError {
		t.Fatalf("101 ids should be rejected; got: %s", resultText(t, res))
	}
}
