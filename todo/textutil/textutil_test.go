package textutil

import (
	"strings"
	"testing"
)

func TestSanitize_ValidASCII(t *testing.T) {
	out, err := Sanitize("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Errorf("got %q, want %q", out, "hello world")
	}
}

func TestSanitize_ValidMultiByteUTF8(t *testing.T) {
	cases := []string{
		"こんにちは",           // CJK
		"café",              // accented
		"hello 🌍",           // emoji
		"Ünïcödé",           // various accents
		"\u4e16\u754c",      // 世界
	}
	for _, s := range cases {
		out, err := Sanitize(s)
		if err != nil {
			t.Errorf("Sanitize(%q): unexpected error: %v", s, err)
		}
		if out == "" {
			t.Errorf("Sanitize(%q): got empty string", s)
		}
	}
}

func TestSanitize_InvalidUTF8(t *testing.T) {
	invalid := "hello\xff\xfeworld"
	_, err := Sanitize(invalid)
	if err != ErrInvalidUTF8 {
		t.Errorf("got err=%v, want ErrInvalidUTF8", err)
	}
}

func TestSanitize_NullByte(t *testing.T) {
	_, err := Sanitize("hello\x00world")
	if err != ErrNullByte {
		t.Errorf("got err=%v, want ErrNullByte", err)
	}
}

func TestSanitize_NFDToNFC(t *testing.T) {
	// e + combining acute accent (NFD) -> é (NFC)
	nfd := "caf\u0065\u0301"
	out, err := Sanitize(nfd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "caf\u00e9"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestSanitize_AlreadyNFC(t *testing.T) {
	nfc := "caf\u00e9"
	out, err := Sanitize(nfc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nfc {
		t.Errorf("got %q, want %q", out, nfc)
	}
}

func TestSanitize_TrimsWhitespace(t *testing.T) {
	out, err := Sanitize("  hello  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello" {
		t.Errorf("got %q, want %q", out, "hello")
	}
}

func TestSanitize_EmptyAfterTrim(t *testing.T) {
	out, err := Sanitize("   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("got %q, want empty", out)
	}
}

func TestSanitize_Idempotent(t *testing.T) {
	inputs := []string{
		"hello world",
		"caf\u0065\u0301",
		"  trimmed  ",
		"こんにちは",
	}
	for _, s := range inputs {
		first, err := Sanitize(s)
		if err != nil {
			t.Fatalf("Sanitize(%q): %v", s, err)
		}
		second, err := Sanitize(first)
		if err != nil {
			t.Fatalf("Sanitize(%q) second pass: %v", first, err)
		}
		if first != second {
			t.Errorf("not idempotent: first=%q, second=%q", first, second)
		}
	}
}

func TestValidateRuneLength(t *testing.T) {
	// ASCII: 5 bytes = 5 runes
	if !ValidateRuneLength("hello", 5) {
		t.Error("expected 'hello' to fit in 5 runes")
	}
	if ValidateRuneLength("hello!", 5) {
		t.Error("expected 'hello!' to NOT fit in 5 runes")
	}

	// CJK: each character is 3 bytes but 1 rune
	cjk := "世界你好吗" // 5 runes, 15 bytes
	if !ValidateRuneLength(cjk, 5) {
		t.Error("expected 5 CJK chars to fit in 5 runes")
	}
	if ValidateRuneLength(cjk, 4) {
		t.Error("expected 5 CJK chars to NOT fit in 4 runes")
	}

	// Empty string
	if !ValidateRuneLength("", 0) {
		t.Error("expected empty string to fit in 0 runes")
	}
}

func TestTruncateRunes_Short(t *testing.T) {
	out := TruncateRunes("hi", 10, "...")
	if out != "hi" {
		t.Errorf("got %q, want %q", out, "hi")
	}
}

func TestTruncateRunes_ExactLength(t *testing.T) {
	out := TruncateRunes("hello", 5, "...")
	if out != "hello" {
		t.Errorf("got %q, want %q", out, "hello")
	}
}

func TestTruncateRunes_ASCII(t *testing.T) {
	out := TruncateRunes("abcdefgh", 5, "...")
	if out != "ab..." {
		t.Errorf("got %q, want %q", out, "ab...")
	}
}

func TestTruncateRunes_MultiByte(t *testing.T) {
	// 6 CJK characters, truncate to 5 total with "..."
	s := "世界你好吗呢" // 6 runes
	out := TruncateRunes(s, 5, "...")
	// keep 2 + "..." = 5 runes
	if out != "世界..." {
		t.Errorf("got %q, want %q", out, "世界...")
	}
}

func TestTruncateRunes_SuffixLongerThanMax(t *testing.T) {
	out := TruncateRunes("abcdef", 2, "...")
	// suffix is 3 runes but max is 2, so truncate suffix to 2
	if out != ".." {
		t.Errorf("got %q, want %q", out, "..")
	}
}

func TestTruncateRunes_50Chars(t *testing.T) {
	// Mimic the cmdutil title truncation: 50 max with "..." suffix
	title := strings.Repeat("a", 55)
	out := TruncateRunes(title, 50, "...")
	if len(out) != 50 {
		t.Errorf("got length %d, want 50", len(out))
	}
	want := strings.Repeat("a", 47) + "..."
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestTruncateRunes_EmptyString(t *testing.T) {
	out := TruncateRunes("", 5, "...")
	if out != "" {
		t.Errorf("got %q, want %q", out, "")
	}
}

func TestTruncateRunes_ZeroMax(t *testing.T) {
	out := TruncateRunes("hello", 0, "...")
	if out != "" {
		t.Errorf("got %q, want %q", out, "")
	}
}

func TestSanitize_LoneCombiningCharacter(t *testing.T) {
	// A lone combining accent is valid UTF-8 — should pass
	out, err := Sanitize("\u0301")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "\u0301" {
		t.Errorf("got %q, want %q", out, "\u0301")
	}
}

func TestSanitize_ControlCharactersPass(t *testing.T) {
	// Non-null control characters are valid UTF-8 — should pass
	out, err := Sanitize("hello\x01world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello\x01world" {
		t.Errorf("got %q, want %q", out, "hello\x01world")
	}
}
