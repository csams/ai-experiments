package gormstore_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
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

// --- UTF-8 validation tests ---

func TestValidation_TitleInvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateTask(ctx(), "hello\xff\xfeworld", "", 0, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "title" {
		t.Errorf("expected ValidationError on title for invalid UTF-8, got %v", err)
	}
}

func TestValidation_TitleNullByte(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateTask(ctx(), "hello\x00world", "", 0, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "title" {
		t.Errorf("expected ValidationError on title for null byte, got %v", err)
	}
}

func TestValidation_TitleNFCNormalization(t *testing.T) {
	s := newTestStore(t)
	// NFD: e + combining acute accent
	nfdTitle := "caf\u0065\u0301"
	task, err := s.CreateTask(ctx(), nfdTitle, "", 0, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be stored as NFC
	want := "caf\u00e9"
	if task.Title != want {
		t.Errorf("got title %q, want NFC %q", task.Title, want)
	}
}

func TestValidation_TitleRuneLimit(t *testing.T) {
	s := newTestStore(t)
	// 512 CJK characters = 1536 bytes, but only 512 runes — should pass
	title512 := strings.Repeat("\u4e16", 512)
	if utf8.RuneCountInString(title512) != 512 {
		t.Fatalf("test setup: expected 512 runes, got %d", utf8.RuneCountInString(title512))
	}
	task, err := s.CreateTask(ctx(), title512, "", 0, nil, nil)
	if err != nil {
		t.Fatalf("512 CJK chars should succeed, got %v", err)
	}
	if task.Title != title512 {
		t.Error("title should be preserved")
	}

	// 513 CJK characters should fail
	title513 := strings.Repeat("\u4e16", 513)
	_, err = s.CreateTask(ctx(), title513, "", 0, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "title" {
		t.Errorf("513 CJK chars should fail validation, got %v", err)
	}
}

func TestValidation_DescriptionInvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateTask(ctx(), "Task", "bad\xffbytes", 0, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "description" {
		t.Errorf("expected ValidationError on description, got %v", err)
	}
}

func TestValidation_DescriptionTooLong(t *testing.T) {
	s := newTestStore(t)
	longDesc := strings.Repeat("a", 100001)
	_, err := s.CreateTask(ctx(), "Task", longDesc, 0, nil, nil)
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "description" {
		t.Errorf("expected ValidationError on description for >100000 chars, got %v", err)
	}
}

func TestValidation_NoteInvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	_, err := s.AddNote(ctx(), task.ID, "bad\xffnote")
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "text" {
		t.Errorf("expected ValidationError on text for invalid UTF-8, got %v", err)
	}
}

func TestValidation_NoteTooLong(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	longNote := strings.Repeat("a", 50001)
	_, err := s.AddNote(ctx(), task.ID, longNote)
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "text" {
		t.Errorf("expected ValidationError on text for >50000 chars, got %v", err)
	}
}

func TestValidation_URLInvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	_, err := s.AddLink(ctx(), task.ID, model.LinkURL, "https://example.com/\xff")
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "url" {
		t.Errorf("expected ValidationError on url for invalid UTF-8, got %v", err)
	}
}

func TestValidation_SearchQueryInvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SearchTasks(ctx(), "bad\xffquery")
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "query" {
		t.Errorf("expected ValidationError on query for invalid UTF-8, got %v", err)
	}
}

func TestValidation_TagWithWhitespace(t *testing.T) {
	s := newTestStore(t)
	// Tag with leading/trailing whitespace should be trimmed and accepted
	task, err := s.CreateTask(ctx(), "Task", "", 0, nil, []string{" my-tag "})
	if err != nil {
		t.Fatalf("tag with whitespace should be accepted after trimming, got %v", err)
	}
	if len(task.Tags) != 1 || task.Tags[0].Tag != "my-tag" {
		t.Errorf("expected tag 'my-tag', got %v", task.Tags)
	}
}

func TestValidation_UpdateTaskNFCNormalization(t *testing.T) {
	s := newTestStore(t)
	task, err := s.CreateTask(ctx(), "Original", "", 0, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Update with NFD title
	nfdTitle := "caf\u0065\u0301"
	updated, err := s.UpdateTask(ctx(), task.ID, store.UpdateTaskOptions{Title: &nfdTitle})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	want := "caf\u00e9"
	if updated.Title != want {
		t.Errorf("UpdateTask NFC: got %q, want %q", updated.Title, want)
	}

	// Read back via GetTask to verify storage
	detail, err := s.GetTask(ctx(), task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if detail.Task.Title != want {
		t.Errorf("GetTask read-back: got %q, want %q", detail.Task.Title, want)
	}
}

func TestValidation_UpdateNoteInvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)
	note, err := s.AddNote(ctx(), task.ID, "valid note")
	if err != nil {
		t.Fatalf("add note: %v", err)
	}
	_, err = s.UpdateNote(ctx(), task.ID, note.ID, "bad\xffupdate")
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "text" {
		t.Errorf("expected ValidationError on text for invalid UTF-8, got %v", err)
	}
}

func TestValidation_RemoveTagsInvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, []string{"valid"})
	err := s.RemoveTags(ctx(), task.ID, []string{"bad\xff"})
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "tag" {
		t.Errorf("expected ValidationError on tag for invalid UTF-8, got %v", err)
	}
}

func TestValidation_BulkRemoveTagsInvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, []string{"valid"})
	err := s.BulkRemoveTags(ctx(), []uint{task.ID}, []string{"bad\xff"})
	var ve *model.ValidationError
	if !errors.As(err, &ve) || ve.Field != "tag" {
		t.Errorf("expected ValidationError on tag for invalid UTF-8, got %v", err)
	}
}

func TestValidation_NoteExactBoundary(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.CreateTask(ctx(), "Task", "", 0, nil, nil)

	// Exactly 50000 chars should pass
	note50k := strings.Repeat("a", 50000)
	_, err := s.AddNote(ctx(), task.ID, note50k)
	if err != nil {
		t.Fatalf("50000 chars should succeed, got %v", err)
	}
}

func TestValidation_NFCTitleReadBack(t *testing.T) {
	s := newTestStore(t)
	nfdTitle := "caf\u0065\u0301"
	task, err := s.CreateTask(ctx(), nfdTitle, "", 0, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := "caf\u00e9"

	// Read back from DB to verify NFC storage
	detail, err := s.GetTask(ctx(), task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if detail.Task.Title != want {
		t.Errorf("read-back: got %q, want %q", detail.Task.Title, want)
	}

	// Search should find it via NFC query
	results, err := s.SearchTasks(ctx(), "café")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected NFC search to find the task")
	}
	found := false
	for _, r := range results {
		if r.ID == task.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("search results did not include task %d", task.ID)
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
