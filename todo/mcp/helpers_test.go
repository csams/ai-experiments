package mcp

import (
	"strings"
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

// --- getLinkInputs ---

func TestGetLinkInputs_Missing(t *testing.T) {
	req := makeReq(map[string]any{})
	got, err := getLinkInputs(req, "links")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != nil {
		t.Errorf("missing → nil, got %v", got)
	}
}

func TestGetLinkInputs_Empty(t *testing.T) {
	req := makeReq(map[string]any{"links": []any{}})
	got, err := getLinkInputs(req, "links")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != nil {
		t.Errorf("empty → nil, got %v", got)
	}
}

func TestGetLinkInputs_Full(t *testing.T) {
	req := makeReq(map[string]any{"links": []any{
		map[string]any{"type": "pr", "url": "https://x/1", "description": "first"},
		map[string]any{"type": "jira", "url": "https://y/2"},
	}})
	got, err := getLinkInputs(req, "links")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Type != model.LinkPR || got[0].URL != "https://x/1" || got[0].Description != "first" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Type != model.LinkJira || got[1].URL != "https://y/2" || got[1].Description != "" {
		t.Errorf("got[1] = %+v", got[1])
	}
}

func TestGetLinkInputs_NotAnObject(t *testing.T) {
	req := makeReq(map[string]any{"links": []any{"not-an-object"}})
	_, err := getLinkInputs(req, "links")
	if err == nil {
		t.Fatal("expected error for non-object entry")
	}
}

func TestGetLinkInputs_MissingType(t *testing.T) {
	req := makeReq(map[string]any{"links": []any{
		map[string]any{"url": "https://x"},
	}})
	_, err := getLinkInputs(req, "links")
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestGetLinkInputs_MissingURL(t *testing.T) {
	req := makeReq(map[string]any{"links": []any{
		map[string]any{"type": "pr"},
	}})
	_, err := getLinkInputs(req, "links")
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestGetLinkInputs_WrongTypeForType(t *testing.T) {
	req := makeReq(map[string]any{"links": []any{
		map[string]any{"type": 42, "url": "https://x"},
	}})
	_, err := getLinkInputs(req, "links")
	if err == nil {
		t.Fatal("expected error for non-string type")
	}
	if !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("error = %q, want substring 'must be a string'", err.Error())
	}
}

func TestGetLinkInputs_WrongTypeForURL(t *testing.T) {
	req := makeReq(map[string]any{"links": []any{
		map[string]any{"type": "pr", "url": 42},
	}})
	_, err := getLinkInputs(req, "links")
	if err == nil {
		t.Fatal("expected error for non-string url")
	}
}

// --- requireUint ---

func TestRequireUint_WithIntValue(t *testing.T) {
	req := makeReq(map[string]any{"task_id": int(42)})
	v, err := requireUint(req, "task_id")
	if err != nil || v != 42 {
		t.Errorf("requireUint(int(42)) = %v, %v; want 42, nil", v, err)
	}
}

func TestRequireUint_WithFloat64Value(t *testing.T) {
	req := makeReq(map[string]any{"task_id": float64(42)})
	v, err := requireUint(req, "task_id")
	if err != nil || v != 42 {
		t.Errorf("requireUint(float64(42)) = %v, %v; want 42, nil", v, err)
	}
}

func TestRequireUint_Missing(t *testing.T) {
	req := makeReq(map[string]any{})
	if _, err := requireUint(req, "task_id"); err == nil {
		t.Error("requireUint on missing key must error")
	}
}

func TestRequireUint_Zero(t *testing.T) {
	req := makeReq(map[string]any{"task_id": float64(0)})
	if _, err := requireUint(req, "task_id"); err == nil {
		t.Error("requireUint(0) must error (must be >= 1)")
	}
}

// --- getUintSlice / requireUintSlice ---

func TestGetUintSlice_MixedTypes(t *testing.T) {
	req := makeReq(map[string]any{"ids": []any{float64(1), int(2), "3"}})
	got := getUintSlice(req, "ids")
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("getUintSlice mixed types = %v; want [1 2 3]", got)
	}
}

func TestRequireUintSlice_Empty(t *testing.T) {
	req := makeReq(map[string]any{"ids": []any{}})
	if _, err := requireUintSlice(req, "ids"); err == nil {
		t.Error("requireUintSlice on empty array must error")
	}
}

func TestRequireUintSlice_UnconvertibleElement(t *testing.T) {
	req := makeReq(map[string]any{"ids": []any{float64(1), "bad"}})
	if _, err := requireUintSlice(req, "ids"); err == nil {
		t.Error("requireUintSlice with unconvertible element must error")
	}
}

func TestRequireUintSlice_ZeroIDRejected(t *testing.T) {
	req := makeReq(map[string]any{"ids": []any{float64(1), float64(0), float64(2)}})
	if _, err := requireUintSlice(req, "ids"); err == nil {
		t.Error("requireUintSlice with ID=0 must error (strict: no silent drops)")
	}
}

// --- getOptInt ---

func TestGetOptInt_Absent(t *testing.T) {
	req := makeReq(map[string]any{})
	if getOptInt(req, "priority") != nil {
		t.Error("absent key must return nil")
	}
}

func TestGetOptInt_PresentZero(t *testing.T) {
	req := makeReq(map[string]any{"priority": float64(0)})
	v := getOptInt(req, "priority")
	if v == nil || *v != 0 {
		t.Errorf("present-zero must return &0, got %v", v)
	}
}

func TestGetOptInt_PresentNull(t *testing.T) {
	req := makeReq(map[string]any{"priority": nil})
	if getOptInt(req, "priority") != nil {
		t.Error("present-null must return nil")
	}
}

// --- getOptUint ---

func TestGetOptUint_Absent(t *testing.T) {
	req := makeReq(map[string]any{})
	if getOptUint(req, "parent_id") != nil {
		t.Error("absent key must return nil")
	}
}

func TestGetOptUint_PresentPositive(t *testing.T) {
	req := makeReq(map[string]any{"parent_id": float64(5)})
	v := getOptUint(req, "parent_id")
	if v == nil || *v != 5 {
		t.Errorf("present positive must return &5, got %v", v)
	}
}

func TestGetOptUint_PresentZero(t *testing.T) {
	req := makeReq(map[string]any{"parent_id": float64(0)})
	if getOptUint(req, "parent_id") != nil {
		t.Error("present-zero must return nil (below Min(1))")
	}
}

func TestGetOptUint_PresentNull(t *testing.T) {
	req := makeReq(map[string]any{"parent_id": nil})
	if getOptUint(req, "parent_id") != nil {
		t.Error("present-null must return nil")
	}
}

// --- getOptStr ---

func TestGetOptStr_AbsentVsEmpty(t *testing.T) {
	absent := makeReq(map[string]any{})
	if getOptStr(absent, "description") != nil {
		t.Error("absent key must return nil")
	}
	present := makeReq(map[string]any{"description": ""})
	v := getOptStr(present, "description")
	if v == nil || *v != "" {
		t.Errorf("present-empty must return &\"\", got %v", v)
	}
}
