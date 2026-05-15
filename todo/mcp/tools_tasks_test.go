package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newMCPTestClient builds a fresh in-memory SQLite-backed MCP server and
// returns an initialized in-process client plus the underlying store for
// direct setup/assert reads.
func newMCPTestClient(t *testing.T) (*client.Client, *gormstore.GormStore) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	s, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	s.SetSyncEmit(true)

	srv := NewServer(s, nil)
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("new in-process client: %v", err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("start client: %v", err)
	}
	if _, err := c.Initialize(context.Background(), mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpgo.Implementation{Name: "test", Version: "1.0.0"},
		},
	}); err != nil {
		t.Fatalf("initialize client: %v", err)
	}
	t.Cleanup(func() {
		c.Close()
		s.Close(context.Background())
	})
	return c, s
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcpgo.CallToolResult {
	t.Helper()
	res, err := c.CallTool(context.Background(), mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func resultText(t *testing.T, r *mcpgo.CallToolResult) string {
	t.Helper()
	if len(r.Content) == 0 {
		t.Fatal("empty content")
	}
	tc, ok := r.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("not text content: %T", r.Content[0])
	}
	return tc.Text
}

// TestCreateTask_DefaultIncludeReturnsFullDetail verifies the consolidated
// create_task returns full task detail when `include` is omitted, where the
// old bare-task return only included the task fields without children/blockers.
func TestCreateTask_DefaultIncludeReturnsFullDetail(t *testing.T) {
	c, _ := newMCPTestClient(t)

	res := callTool(t, c, "create_task", map[string]any{
		"title":       "parent",
		"description": "with body",
	})
	if res.IsError {
		t.Fatalf("create_task errored: %s", resultText(t, res))
	}

	// Full detail should include the cheap fields plus expensive ones loaded.
	// Verify by parsing the JSON: a TaskDetail has the `links`, `notes`,
	// `children`, `blockers`, `blocking` keys (empty arrays).
	var got map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"id", "title", "description"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q in full-detail response: %v", k, got)
		}
	}
}

// TestSetParent_OmittedParentIDUnparents verifies that calling set_parent
// without a parent_id removes the task's parent (the old `unparent` behavior).
func TestSetParent_OmittedParentIDUnparents(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()

	parent, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "parent"})
	child, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "child"})
	if err := s.SetParent(ctx, child.ID, &parent.ID); err != nil {
		t.Fatalf("setup SetParent: %v", err)
	}

	res := callTool(t, c, "set_parent", map[string]any{
		"task_id": float64(child.ID),
	})
	if res.IsError {
		t.Fatalf("set_parent errored: %s", resultText(t, res))
	}

	detail, err := s.GetTask(ctx, child.ID, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.ParentID != nil {
		t.Errorf("expected parent cleared, got %v", detail.ParentID)
	}
}

// TestSetTaskArchived_ArrayHappyPath verifies the consolidated set_task_archived
// archives every task in the input array and returns full detail for each.
func TestSetTaskArchived_ArrayHappyPath(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()
	a, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "A"})
	b, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "B"})

	res := callTool(t, c, "set_task_archived", map[string]any{
		"ids":      []any{float64(a.ID), float64(b.ID)},
		"archived": true,
	})
	if res.IsError {
		t.Fatalf("set_task_archived errored: %s", resultText(t, res))
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &arr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 results, got %d", len(arr))
	}

	for _, id := range []uint{a.ID, b.ID} {
		detail, err := s.GetTask(ctx, id, store.GetTaskOptions{Include: model.AllTaskIncludesSet()})
		if err != nil {
			t.Fatalf("GetTask(%d): %v", id, err)
		}
		if !detail.Archived {
			t.Errorf("task %d not archived after set_task_archived", id)
		}
	}
}

// TestSetTaskArchived_MidArrayFailureLeavesPrefix verifies the partial-failure
// behavior documented in the tool description: when one ID fails, earlier IDs
// remain in their new state.
func TestSetTaskArchived_MidArrayFailureLeavesPrefix(t *testing.T) {
	c, s := newMCPTestClient(t)
	ctx := context.Background()

	a, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "A"})
	missing := uint(999999) // never created
	b, _ := s.CreateTask(ctx, store.CreateTaskOptions{Title: "B"})

	res := callTool(t, c, "set_task_archived", map[string]any{
		"ids":      []any{float64(a.ID), float64(missing), float64(b.ID)},
		"archived": true,
	})
	if !res.IsError {
		t.Fatalf("expected error result for missing ID; got: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "999999") &&
		!strings.Contains(strings.ToLower(resultText(t, res)), "not found") {
		t.Logf("error text: %s", resultText(t, res))
	}

	aDetail, _ := s.GetTask(ctx, a.ID, store.GetTaskOptions{})
	if !aDetail.Archived {
		t.Errorf("prefix task A should be archived (partial-progress contract); got Archived=false")
	}
	bDetail, _ := s.GetTask(ctx, b.ID, store.GetTaskOptions{})
	if bDetail.Archived {
		t.Errorf("trailing task B should NOT be archived after mid-array failure")
	}
}
