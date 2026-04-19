package textutil

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var (
	ErrInvalidUTF8 = errors.New("invalid UTF-8")
	ErrNullByte    = errors.New("contains null byte")
)

// Sanitize validates and normalizes a string for storage:
//  1. Rejects invalid UTF-8 sequences
//  2. Rejects null bytes
//  3. Applies NFC normalization
//  4. Trims leading/trailing whitespace
func Sanitize(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrInvalidUTF8
	}
	if strings.ContainsRune(s, 0) {
		return "", ErrNullByte
	}
	s = norm.NFC.String(s)
	s = strings.TrimSpace(s)
	return s, nil
}

// ValidateRuneLength reports whether s has at most maxRunes Unicode code points.
// Should be called after Sanitize.
func ValidateRuneLength(s string, maxRunes int) bool {
	return utf8.RuneCountInString(s) <= maxRunes
}

// TruncateRunes truncates s to at most maxRunes Unicode code points total
// (including the suffix). If s is already within the limit, it is returned
// unchanged. If the suffix itself is longer than maxRunes, only the suffix
// (truncated to maxRunes runes) is returned.
func TruncateRunes(s string, maxRunes int, suffix string) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	suffixLen := utf8.RuneCountInString(suffix)
	keep := maxRunes - suffixLen
	if keep <= 0 {
		// Suffix alone exceeds or equals max; return truncated suffix.
		return truncateToNRunes(suffix, maxRunes)
	}
	return truncateToNRunes(s, keep) + suffix
}

func truncateToNRunes(s string, n int) string {
	i := 0
	for count := 0; count < n && i < len(s); count++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i]
}
