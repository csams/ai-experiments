package gormstore_test

import (
	"context"
	"testing"

	"github.com/csams/todo/store"
)

// TestEmit_PopulatesActorFromContext — when a mutation runs with an
// actor-carrying context (the bearer-auth middleware sets it), the
// emitted StoreEvent must carry that label so downstream observers
// (audit logger, vector syncer) can attribute the action.
func TestEmit_PopulatesActorFromContext(t *testing.T) {
	s := newTestStore(t)
	obs := &recordingObserver{}
	s.AddObserver(obs)

	actorCtx := store.SetActorContext(context.Background(), "alice")
	if _, err := s.CreateTask(actorCtx, store.CreateTaskOptions{Title: "T"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if len(obs.events) == 0 {
		t.Fatal("no events emitted")
	}
	// The task.created event from this mutation must carry actor="alice".
	for _, e := range obs.events {
		if e.Type != "task.created" {
			continue
		}
		if e.Actor != "alice" {
			t.Errorf("event.Actor = %q, want %q", e.Actor, "alice")
		}
		return
	}
	t.Errorf("task.created event missing from %v", obs.events)
}

// TestEmit_NoActorOnPlainContext — CLI calls and stdio-MCP calls don't
// stamp an actor; the event must surface an empty Actor field rather
// than fabricating one.
func TestEmit_NoActorOnPlainContext(t *testing.T) {
	s := newTestStore(t)
	obs := &recordingObserver{}
	s.AddObserver(obs)

	if _, err := s.CreateTask(context.Background(), store.CreateTaskOptions{Title: "T"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, e := range obs.events {
		if e.Actor != "" {
			t.Errorf("plain-context event picked up an actor: %q", e.Actor)
		}
	}
}
