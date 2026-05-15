package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/csams/todo/store"
	"github.com/csams/todo/store/gormstore"
	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Compile-time assertion: if store.SemanticSearcher grows, this fails fast
// instead of producing obscure runtime test failures.
var _ store.SemanticSearcher = (*fakeSearcher)(nil)

// fakeSearcher records the last call dispatched to either SemanticSearch or
// SemanticSearchContext so tests can assert which mode the unified
// semantic_search tool chose.
type fakeSearcher struct {
	calledSearch  bool
	calledContext bool
	lastQuery     string
	lastTaskID    uint
	lastOpts      store.SemanticSearchOptions
	results       []store.SemanticSearchResult
	err           error
}

func (f *fakeSearcher) SemanticSearch(_ context.Context, query string, opts store.SemanticSearchOptions) ([]store.SemanticSearchResult, error) {
	f.calledSearch = true
	f.lastQuery = query
	f.lastOpts = opts
	return f.results, f.err
}

func (f *fakeSearcher) SemanticSearchContext(_ context.Context, taskID uint, opts store.SemanticSearchOptions) ([]store.SemanticSearchResult, error) {
	f.calledContext = true
	f.lastTaskID = taskID
	f.lastOpts = opts
	return f.results, f.err
}

// newMCPTestClientWithSearcher mirrors newMCPTestClient but registers the
// semantic_search tool with the supplied fake searcher.
func newMCPTestClientWithSearcher(t *testing.T, ss store.SemanticSearcher) (*client.Client, *gormstore.GormStore) {
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

	srv := NewServer(s, ss)
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

func TestSemanticSearch_QueryModeDispatchesSearch(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := newMCPTestClientWithSearcher(t, f)

	res := callTool(t, c, "semantic_search", map[string]any{
		"query":            "authentication",
		"limit":            float64(20),
		"type":             "task",
		"include_archived": true,
	})
	if res.IsError {
		t.Fatalf("query-mode errored: %s", resultText(t, res))
	}
	if !f.calledSearch || f.calledContext {
		t.Errorf("expected SemanticSearch path, got search=%v context=%v", f.calledSearch, f.calledContext)
	}
	if f.lastQuery != "authentication" {
		t.Errorf("query = %q, want %q", f.lastQuery, "authentication")
	}
	if f.lastOpts.Limit != 20 {
		t.Errorf("Limit = %d, want 20", f.lastOpts.Limit)
	}
	if f.lastOpts.Type != "task" {
		t.Errorf("Type = %q, want %q", f.lastOpts.Type, "task")
	}
	if !f.lastOpts.IncludeArchived {
		t.Errorf("IncludeArchived not forwarded")
	}

	// Verify task_id scope is forwarded in query mode.
	f2 := &fakeSearcher{}
	c2, _ := newMCPTestClientWithSearcher(t, f2)
	_ = callTool(t, c2, "semantic_search", map[string]any{
		"query":   "auth",
		"task_id": float64(42),
	})
	if f2.lastOpts.TaskID == nil || *f2.lastOpts.TaskID != 42 {
		t.Errorf("task_id scope not forwarded: %+v", f2.lastOpts.TaskID)
	}
}

func TestSemanticSearch_RelatedDispatchesContext(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := newMCPTestClientWithSearcher(t, f)

	res := callTool(t, c, "semantic_search", map[string]any{
		"related_to_task_id": float64(7),
		"limit":              float64(5),
		"type":               "note",
		"include_archived":   true,
	})
	if res.IsError {
		t.Fatalf("context-mode errored: %s", resultText(t, res))
	}
	if !f.calledContext || f.calledSearch {
		t.Errorf("expected SemanticSearchContext path, got search=%v context=%v", f.calledSearch, f.calledContext)
	}
	if f.lastTaskID != 7 {
		t.Errorf("source task_id = %d, want 7", f.lastTaskID)
	}
	if f.lastOpts.TaskID != nil {
		t.Errorf("context mode should not forward scope task_id, got %v", *f.lastOpts.TaskID)
	}
	if f.lastOpts.Type != "note" {
		t.Errorf("Type = %q, want %q", f.lastOpts.Type, "note")
	}
	if !f.lastOpts.IncludeArchived {
		t.Errorf("IncludeArchived not forwarded in context mode")
	}
}

func TestSemanticSearch_BothModesRejected(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := newMCPTestClientWithSearcher(t, f)

	res := callTool(t, c, "semantic_search", map[string]any{
		"query":              "auth",
		"related_to_task_id": float64(7),
	})
	if !res.IsError {
		t.Fatalf("expected error when both query and related_to_task_id are set; got: %s", resultText(t, res))
	}
	if !strings.Contains(strings.ToLower(resultText(t, res)), "exactly one") {
		t.Errorf("error should explain mutual exclusivity, got: %s", resultText(t, res))
	}
	if f.calledSearch || f.calledContext {
		t.Errorf("no searcher call should have happened, got search=%v context=%v", f.calledSearch, f.calledContext)
	}
}

func TestSemanticSearch_NeitherModeRejected(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := newMCPTestClientWithSearcher(t, f)

	res := callTool(t, c, "semantic_search", map[string]any{
		"limit": float64(10),
	})
	if !res.IsError {
		t.Fatalf("expected error when neither query nor related_to_task_id is set; got: %s", resultText(t, res))
	}
	if !strings.Contains(strings.ToLower(resultText(t, res)), "exactly one") {
		t.Errorf("error should explain mutual exclusivity, got: %s", resultText(t, res))
	}
}

func TestSemanticSearch_LimitCapped(t *testing.T) {
	f := &fakeSearcher{}
	c, _ := newMCPTestClientWithSearcher(t, f)

	_ = callTool(t, c, "semantic_search", map[string]any{
		"query": "anything",
		"limit": float64(500),
	})
	if f.lastOpts.Limit != 100 {
		t.Errorf("limit should be capped at 100, got %d", f.lastOpts.Limit)
	}
}

// TestSemanticSearch_ResultsPassThrough confirms the handler JSON-marshals
// results from the underlying searcher faithfully.
func TestSemanticSearch_ResultsPassThrough(t *testing.T) {
	f := &fakeSearcher{
		results: []store.SemanticSearchResult{
			{ID: "task:1", Text: "hello", Score: 0.9},
		},
	}
	c, _ := newMCPTestClientWithSearcher(t, f)

	res := callTool(t, c, "semantic_search", map[string]any{"query": "x"})
	if res.IsError {
		t.Fatalf("errored: %s", resultText(t, res))
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0]["ID"] != "task:1" {
		t.Errorf("unexpected results: %v", got)
	}
}
