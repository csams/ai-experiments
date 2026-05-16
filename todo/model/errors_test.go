package model_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/csams/todo/model"
)

// TestErrorSentinels_IsAndAs pins the dual-use pattern: every typed
// error in the model package can be matched by errors.Is against its
// category sentinel AND by errors.As when the caller wants the
// structured payload. Wrapped errors (the gormstore returns these
// inside fmt.Errorf("...: %w", err)) must still satisfy both.
func TestErrorSentinels_IsAndAs(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		sentinel error
		// extract returns true if errors.As recovers the typed payload.
		extract func(err error) bool
	}{
		{
			name:     "validation",
			err:      &model.ValidationError{Field: "title", Message: "required"},
			sentinel: model.ErrValidation,
			extract: func(err error) bool {
				var ve *model.ValidationError
				return errors.As(err, &ve) && ve.Field == "title"
			},
		},
		{
			name:     "blocking_external",
			err:      &model.BlockingExternalError{BlockingTaskID: 1, BlockedTaskID: 2},
			sentinel: model.ErrBlockingExternal,
			extract: func(err error) bool {
				var be *model.BlockingExternalError
				return errors.As(err, &be) && be.BlockingTaskID == 1 && be.BlockedTaskID == 2
			},
		},
		{
			name:     "cycle",
			err:      &model.CycleDetectedError{Path: []uint{1, 2, 1}},
			sentinel: model.ErrCycle,
			extract: func(err error) bool {
				var ce *model.CycleDetectedError
				return errors.As(err, &ce) && len(ce.Path) == 3
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.sentinel) {
				t.Errorf("errors.Is direct match failed: %v not %v", tc.err, tc.sentinel)
			}
			if !tc.extract(tc.err) {
				t.Errorf("errors.As did not recover payload from direct error")
			}
			// Same checks with fmt.Errorf wrapping — gormstore returns
			// errors this way to add caller-context (task ID, etc.).
			wrapped := fmt.Errorf("task 42: %w", tc.err)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is failed through fmt.Errorf wrap: %v not %v", wrapped, tc.sentinel)
			}
			if !tc.extract(wrapped) {
				t.Errorf("errors.As did not recover payload through wrap")
			}
		})
	}
}

// TestErrorSentinels_DistinctSentinels — guard against a future refactor
// that points two Unwrap()s at the same sentinel and accidentally makes
// errors.Is return true for unrelated categories.
func TestErrorSentinels_DistinctSentinels(t *testing.T) {
	ve := &model.ValidationError{}
	be := &model.BlockingExternalError{}
	ce := &model.CycleDetectedError{}

	if errors.Is(ve, model.ErrCycle) {
		t.Error("ValidationError must not satisfy ErrCycle")
	}
	if errors.Is(be, model.ErrValidation) {
		t.Error("BlockingExternalError must not satisfy ErrValidation")
	}
	if errors.Is(ce, model.ErrBlockingExternal) {
		t.Error("CycleDetectedError must not satisfy ErrBlockingExternal")
	}
}
