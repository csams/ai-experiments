// Package chunker splits long text into overlapping windows sized to fit
// within an embedder's context limit.
package chunker

import (
	"strings"
	"unicode"
)

// ChunkText splits body into chunks of at most maxRunes runes, with successive
// chunks overlapping by `overlap` runes. Splits prefer paragraph boundaries
// (\n\n), then sentence terminators (. ! ?), then whitespace, falling back to
// a hard rune cut. Returns an empty slice for empty / whitespace-only input.
//
// All sizing is rune-based — never use byte-length on multibyte UTF-8 input.
func ChunkText(body string, maxRunes, overlap int) []string {
	if maxRunes <= 0 {
		return nil
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxRunes {
		overlap = maxRunes / 2
	}

	runes := []rune(body)
	if len(runes) == 0 {
		return nil
	}
	if strings.TrimSpace(string(runes)) == "" {
		return nil
	}

	if len(runes) <= maxRunes {
		return []string{strings.TrimSpace(string(runes))}
	}

	var chunks []string
	step := maxRunes - overlap
	start := 0
	for start < len(runes) {
		end := start + maxRunes
		if end >= len(runes) {
			piece := strings.TrimSpace(string(runes[start:]))
			if piece != "" {
				chunks = append(chunks, piece)
			}
			break
		}

		// Soft floor for sentence/whitespace splits: prefer breaks past `start+step`
		// so chunks aren't tiny. Paragraph splits use a lower floor (half-window)
		// since respecting structural breaks matters more than maximizing fill.
		minBreak := start + step
		if minBreak < start+1 {
			minBreak = start + 1
		}
		paraFloor := start + maxRunes/2
		if paraFloor < start+1 {
			paraFloor = start + 1
		}

		breakAt := findBreak(runes, minBreak, paraFloor, end)
		piece := strings.TrimSpace(string(runes[start:breakAt]))
		if piece != "" {
			chunks = append(chunks, piece)
		}

		next := breakAt - overlap
		if next <= start {
			// Avoid pathological no-progress on tiny windows.
			next = start + 1
		}
		start = next
	}
	return chunks
}

// findBreak returns the best split index in (minBreak, max] runes for sentence
// and whitespace splits, and (paraFloor, max] for paragraph splits. Preference:
// paragraph break, sentence terminator, whitespace, then a hard cut at max.
func findBreak(runes []rune, minBreak, paraFloor, max int) int {
	if max > len(runes) {
		max = len(runes)
	}
	if minBreak >= max {
		return max
	}

	// Paragraph break: \n\n — searched in a wider window so structural breaks win.
	for i := max - 1; i > paraFloor; i-- {
		if runes[i] == '\n' && i-1 >= 0 && runes[i-1] == '\n' {
			return i + 1
		}
	}

	// Sentence terminator followed by whitespace: ". ", "! ", "? ", or terminator + newline.
	for i := max - 1; i > minBreak; i-- {
		if isSentenceEnd(runes[i-1]) && (unicode.IsSpace(runes[i])) {
			return i + 1
		}
	}

	// Any whitespace.
	for i := max - 1; i > minBreak; i-- {
		if unicode.IsSpace(runes[i]) {
			return i + 1
		}
	}

	// Hard cut.
	return max
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?':
		return true
	}
	return false
}

// RuneCount returns the rune count of s. Provided so callers don't have to
// import unicode/utf8 just for this.
func RuneCount(s string) int {
	return len([]rune(s))
}
