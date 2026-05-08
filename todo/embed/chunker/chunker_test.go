package chunker

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkText_Empty(t *testing.T) {
	if got := ChunkText("", 100, 10); len(got) != 0 {
		t.Errorf("empty input: got %d chunks, want 0", len(got))
	}
	if got := ChunkText("   \n\t  ", 100, 10); len(got) != 0 {
		t.Errorf("whitespace-only input: got %d chunks, want 0", len(got))
	}
}

func TestChunkText_ShortInputSingleChunk(t *testing.T) {
	body := "this is short"
	got := ChunkText(body, 100, 10)
	if len(got) != 1 {
		t.Fatalf("got %d chunks, want 1", len(got))
	}
	if got[0] != body {
		t.Errorf("chunk = %q, want %q", got[0], body)
	}
}

func TestChunkText_LongInputMultipleChunks(t *testing.T) {
	// Build ~9000 runes of plain text so we get multiple chunks at maxRunes=3000.
	body := strings.Repeat("word ", 1800) // ~9000 chars
	got := ChunkText(body, 3000, 200)
	if len(got) < 3 {
		t.Fatalf("got %d chunks, want >= 3", len(got))
	}
	for i, c := range got {
		if utf8.RuneCountInString(c) > 3000 {
			t.Errorf("chunk %d has %d runes, exceeds maxRunes=3000", i, utf8.RuneCountInString(c))
		}
	}
}

func TestChunkText_OverlapPresent(t *testing.T) {
	body := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta iota kappa ", 200)
	got := ChunkText(body, 1000, 200)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(got))
	}
	// Last 100 runes of chunk[0] should appear somewhere in chunk[1] (overlap=200,
	// allowing for trim-whitespace variation).
	prevTail := []rune(got[0])
	tailStart := len(prevTail) - 100
	if tailStart < 0 {
		tailStart = 0
	}
	tail := string(prevTail[tailStart:])
	tail = strings.TrimSpace(tail)
	if tail == "" {
		t.Skip("tail empty, skipping")
	}
	if !strings.Contains(got[1], tail[:min(50, len(tail))]) {
		t.Errorf("chunk[1] does not contain prefix of chunk[0] tail; overlap not working")
	}
}

func TestChunkText_RespectsParagraphBoundary(t *testing.T) {
	first := strings.Repeat("foo ", 600)  // 2400 chars
	second := strings.Repeat("bar ", 600) // 2400 chars
	body := first + "\n\n" + second
	got := ChunkText(body, 3000, 200)
	if len(got) < 2 {
		t.Fatalf("got %d chunks, want >= 2", len(got))
	}
	// First chunk should end where the paragraph ends, before "bar" starts.
	if strings.Contains(got[0], "bar") {
		t.Errorf("chunk[0] crossed paragraph boundary: contains 'bar'")
	}
	if !strings.Contains(got[1], "bar") {
		t.Errorf("chunk[1] does not contain second paragraph")
	}
}

func TestChunkText_UTF8NotSplitMidRune(t *testing.T) {
	// Repeat a 3-byte rune to force chunking and verify each chunk decodes cleanly.
	body := strings.Repeat("漢字テスト", 1000) // many multi-byte runes, ~5000 runes
	got := ChunkText(body, 1000, 100)
	if len(got) < 4 {
		t.Fatalf("got %d chunks, want >= 4", len(got))
	}
	for i, c := range got {
		if !utf8.ValidString(c) {
			t.Errorf("chunk %d is not valid UTF-8 (split mid-rune)", i)
		}
	}
}

func TestChunkText_HardCutWhenNoBoundary(t *testing.T) {
	// Text with no whitespace forces a hard rune cut.
	body := strings.Repeat("a", 5000)
	got := ChunkText(body, 1000, 100)
	if len(got) < 5 {
		t.Fatalf("got %d chunks, want >= 5", len(got))
	}
	for i, c := range got {
		if utf8.RuneCountInString(c) > 1000 {
			t.Errorf("chunk %d has %d runes, exceeds maxRunes", i, utf8.RuneCountInString(c))
		}
	}
}

func TestRuneCount(t *testing.T) {
	if got := RuneCount("hello"); got != 5 {
		t.Errorf("RuneCount(\"hello\") = %d, want 5", got)
	}
	if got := RuneCount("漢字"); got != 2 {
		t.Errorf("RuneCount(\"漢字\") = %d, want 2", got)
	}
	if got := RuneCount(""); got != 0 {
		t.Errorf("RuneCount(\"\") = %d, want 0", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
