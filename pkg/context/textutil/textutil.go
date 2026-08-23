// Package textutil holds the UTF-8-safe truncation helpers shared by every
// context-engineering package (context, compact, retrieval, skills,
// instructions, knowledge, repomap).
//
// Naive `s[:n]` byte slicing splits multi-byte runes and produces invalid
// UTF-8. Invalid UTF-8 tokenizes into replacement-character byte fallbacks,
// which wastes tokens and measurably degrades small-model comprehension.
// Every truncation in the context layer must go through this package.
package textutil

import (
	"strings"
	"unicode/utf8"
)

// TruncMarker is the default suffix appended by Truncate.
const TruncMarker = "\n...[truncated]"

// Clip returns at most n bytes of s, backing off to the nearest rune boundary
// so the result is always valid UTF-8. It appends nothing.
func Clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	// Back off until the byte at n starts a rune (or we hit zero).
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ClipRunes returns at most n runes of s.
func ClipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i, count := 0, 0
	for i < len(s) {
		_, size := utf8.DecodeRuneInString(s[i:])
		if count == n {
			break
		}
		i += size
		count++
	}
	return s[:i]
}

// Truncate clips s to at most n bytes total (including marker) on a rune
// boundary and appends marker when anything was dropped.
func Truncate(s string, n int, marker string) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if marker == "" {
		return Clip(s, n)
	}
	if len(marker) >= n {
		return Clip(s, n)
	}
	return Clip(s, n-len(marker)) + marker
}

// TruncateDefault is Truncate with TruncMarker.
func TruncateDefault(s string, n int) string { return Truncate(s, n, TruncMarker) }

// FirstLine returns the first non-empty line of s, clipped to maxRunes.
func FirstLine(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if maxRunes > 0 && utf8.RuneCountInString(s) > maxRunes {
		return ClipRunes(s, maxRunes) + "…"
	}
	return s
}

// HeadTail keeps the first head bytes and the last tail bytes of s, joined by
// an elision marker. Used as the fallback when relevance windowing finds no
// match: a head-only excerpt hides the end of every file, which is where most
// Go/Python code that matters lives.
func HeadTail(s string, head, tail int, marker string) string {
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	if len(s) <= head+tail {
		return s
	}
	if marker == "" {
		marker = "\n...[elided]\n"
	}
	h := Clip(s, head)
	t := s[len(s)-tail:]
	// Advance t to a rune boundary.
	for len(t) > 0 && !utf8.RuneStart(t[0]) {
		t = t[1:]
	}
	// Prefer whole lines.
	if i := strings.LastIndexByte(h, '\n'); i > head/2 {
		h = h[:i]
	}
	if i := strings.IndexByte(t, '\n'); i >= 0 && i < len(t)/2 {
		t = t[i+1:]
	}
	return h + marker + t
}
