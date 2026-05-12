package mcp

import (
	"testing"

	"github.com/csams/todo/model"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// makeReq builds a CallToolRequest with the given arguments.
func makeReq(args map[string]any) mcpgo.CallToolRequest {
	return mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Arguments: args},
	}
}

func TestResolveTaskIncludes_Omitted(t *testing.T) {
	req := makeReq(map[string]any{})
	set, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("expected empty set, got %v", set)
	}
}

func TestResolveTaskIncludes_EmptyArray(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{}})
	set, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("expected empty set, got %v", set)
	}
}

func TestResolveTaskIncludes_NamedValues(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{"notes", "links"}})
	set, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !set["notes"] || !set["links"] {
		t.Errorf("expected notes+links, got %v", set)
	}
	if set["description"] {
		t.Errorf("description should not be in set: %v", set)
	}
}

func TestResolveTaskIncludes_StarExpansion(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{"*"}})
	set, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	for _, k := range model.TaskIncludes {
		if !set[k] {
			t.Errorf("star should include %q; got set %v", k, set)
		}
	}
}

func TestResolveTaskIncludes_StarDedupesWithNamed(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{"*", "notes"}})
	set, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(set) != len(model.TaskIncludes) {
		t.Errorf("expected %d keys (* dedupes named), got %d: %v",
			len(model.TaskIncludes), len(set), set)
	}
}

func TestResolveTaskIncludes_DuplicateNamed(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{"notes", "notes"}})
	set, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(set) != 1 || !set["notes"] {
		t.Errorf("expected {notes:true} only, got %v", set)
	}
}

func TestResolveTaskIncludes_UnknownValueErrors(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{"foo"}})
	_, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err == nil {
		t.Fatal("expected error for unknown include value")
	}
}

func TestResolveTaskIncludes_StarPlusUnknownErrors(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{"*", "foo"}})
	_, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err == nil {
		t.Fatal("expected error for unknown include value alongside *")
	}
}

func TestResolveTaskIncludes_CaseSensitive(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{"Notes"}})
	_, err := resolveTaskIncludes(req, model.TaskIncludes)
	if err == nil {
		t.Fatal("expected error for capitalized value (case-sensitive)")
	}
}

func TestResolveTaskIncludes_ListTasksRejectsBlocking(t *testing.T) {
	// list_tasks uses TaskListIncludes, which does not include "blocking".
	req := makeReq(map[string]any{"include": []any{"blocking"}})
	_, err := resolveTaskIncludes(req, model.TaskListIncludes)
	if err == nil {
		t.Fatal("expected error: list_tasks does not support blocking opt-in")
	}

	// But blocking IS valid for get_task (TaskIncludes).
	if _, err := resolveTaskIncludes(req, model.TaskIncludes); err != nil {
		t.Errorf("get_task should accept blocking: %v", err)
	}
}

func TestResolveTaskIncludes_ListTasksStarStopsAtListSet(t *testing.T) {
	req := makeReq(map[string]any{"include": []any{"*"}})
	set, err := resolveTaskIncludes(req, model.TaskListIncludes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if set["blocking"] {
		t.Errorf("list_tasks star expansion must not include blocking; got %v", set)
	}
	for _, k := range model.TaskListIncludes {
		if !set[k] {
			t.Errorf("star should include %q; got set %v", k, set)
		}
	}
}
