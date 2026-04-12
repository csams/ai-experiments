package gormstore_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/csams/todo/model"
)

func TestValidation_IDZero(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetTask(ctx(), 0)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for ID=0, got %T: %v", err, err)
	}
}

func TestValidation_TitleTooLong(t *testing.T) {
	s := newTestStore(t)
	longTitle := strings.Repeat("a", 513)
	_, err := s.CreateTask(ctx(), longTitle, "", 0, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "title" {
		t.Errorf("expected ValidationError on title, got %v", err)
	}
}

func TestValidation_LongDescriptionAllowed(t *testing.T) {
	s := newTestStore(t)
	longDesc := strings.Repeat("a", 100000)
	task, err := s.CreateTask(ctx(), "Task", longDesc, 0, nil, nil)
	if err != nil {
		t.Fatalf("expected no error for long description, got %v", err)
	}
	if task.Description != longDesc {
		t.Errorf("expected description to be preserved, got length %d", len(task.Description))
	}
}

func TestValidation_InvalidTagChars(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateTask(ctx(), "Task", "", 0, nil, []string{"invalid tag!"})
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "tag" {
		t.Errorf("expected ValidationError on tag, got %v", err)
	}
}

func TestValidation_EmptyTag(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateTask(ctx(), "Task", "", 0, nil, []string{""})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for empty tag, got %v", err)
	}
}

func TestValidation_EmptyNoteText(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	_, err := s.AddNote(ctx(), task.ID, "")
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for empty note, got %v", err)
	}
}

func TestValidation_EmptyLinkURL(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	_, err := s.AddLink(ctx(), task.ID, model.LinkURL, "")
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for empty URL, got %v", err)
	}
}

func TestValidation_EmptySearchQuery(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SearchTasks(ctx(), "")
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for empty query, got %v", err)
	}
}

func TestValidation_BulkMaxIDs(t *testing.T) {
	s := newTestStore(t)
	ids := make([]uint, 101)
	for i := range ids {
		ids[i] = uint(i + 1)
	}
	_, err := s.BulkUpdateState(ctx(), ids, model.StateProgressing)
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for >100 IDs, got %v", err)
	}
}

func TestValidation_TagsMax50(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	// Add 50 tags
	tags := make([]string, 50)
	for i := range tags {
		tags[i] = strings.Repeat("a", 1) + strings.Repeat("0", i) // unique tags
	}
	if err := s.AddTags(ctx(), task.ID, tags); err != nil {
		t.Fatalf("add 50 tags: %v", err)
	}

	// Adding one more should fail
	err := s.AddTags(ctx(), task.ID, []string{"overflow"})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError for >50 tags, got %v", err)
	}
}
