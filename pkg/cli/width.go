package cli

import (
	"strings"
	"unicode/utf8"
)

// Terminal width math that is ANSI-escape aware and counts *display cells*,
// not bytes. Every box border in the TUI depends on this being correct: a
// byte-based count under-pads by 2 for each 3-byte rune (●, ▸, ─, ⚠ …) and
// byte-slicing splits runes into replacement characters.

// wideRange is a compact East-Asian Wide / Fullwidth + emoji table. Kept small
// and hand-maintained on purpose so no extra dependency is required.
var wideRanges = [][2]rune{
	{0x1100, 0x115F}, // Hangul Jamo init
	{0x2E80, 0x303E}, // CJK radicals, Kangxi, CJK symbols
	{0x3041, 0x33FF}, // Hiragana … CJK compatibility
	{0x3400, 0x4DBF}, // CJK ext A
	{0x4E00, 0x9FFF}, // CJK unified
	{0xA000, 0xA4CF}, // Yi
	{0xA960, 0xA97F}, // Hangul Jamo ext A
	{0xAC00, 0xD7A3}, // Hangul syllables
	{0xF900, 0xFAFF}, // CJK compatibility ideographs
	{0xFE10, 0xFE19}, // vertical forms
	{0xFE30, 0xFE6F}, // CJK compatibility forms
	{0xFF00, 0xFF60}, // fullwidth forms
	{0xFFE0, 0xFFE6}, // fullwidth signs
	{0x1F300, 0x1F64F},
	{0x1F680, 0x1F6FF},
	{0x1F900, 0x1F9FF},
	{0x1FA70, 0x1FAFF},
	{0x20000, 0x2FFFD},
	{0x30000, 0x3FFFD},
}

// zeroRanges are combining marks / joiners / variation selectors that occupy no
// display cell of their own.
var zeroRanges = [][2]rune{
	{0x0300, 0x036F},
	{0x0483, 0x0489},
	{0x0591, 0x05BD},
	{0x0610, 0x061A},
	{0x064B, 0x065F},
	{0x0670, 0x0670},
	{0x06D6, 0x06DC},
	{0x0900, 0x0903},
	{0x093A, 0x093C},
	{0x200B, 0x200F},
	{0x2060, 0x2064},
	{0x20D0, 0x20F0},
	{0xFE00, 0xFE0F},
	{0xFE20, 0xFE2F},
	{0xE0100, 0xE01EF},
}

func inRanges(r rune, table [][2]rune) bool {
	lo, hi := 0, len(table)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < table[mid][0]:
			hi = mid - 1
		case r > table[mid][1]:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// RuneWidth returns the number of terminal cells a rune occupies (0, 1 or 2).
func RuneWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32 || (r >= 0x7F && r < 0xA0):
		return 0 // control characters render nothing useful
	case r < 0x300:
		return 1 // fast path: ASCII + Latin-1
	case inRanges(r, zeroRanges):
		return 0
	case inRanges(r, wideRanges):
		return 2
	default:
		return 1
	}
}

// StringWidth counts display cells of a plain (escape-free) string.
func StringWidth(s string) int {
	w := 0
	for _, r := range s {
		w += RuneWidth(r)
	}
	return w
}

// VisibleWidth counts display cells, skipping ANSI CSI/OSC escape sequences.
func VisibleWidth(s string) int {
	w := 0
	forEachVisibleRune(s, func(r rune, _, _ int) bool {
		w += RuneWidth(r)
		return true
	})
	return w
}

// StripANSI removes escape sequences, leaving the printable text.
func StripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	forEachVisibleRune(s, func(r rune, _, _ int) bool {
		b.WriteRune(r)
		return true
	})
	return b.String()
}

// forEachVisibleRune walks s and calls fn for every printable rune with its
// byte offset and size. Returning false stops the walk.
func forEachVisibleRune(s string, fn func(r rune, off, size int) bool) {
	i := 0
	for i < len(s) {
		if s[i] == 0x1b { // ESC
			i += escapeLen(s[i:])
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			return
		}
		if !fn(r, i, size) {
			return
		}
		i += size
	}
}

// escapeLen returns the byte length of the escape sequence at the head of s
// (s[0] must be ESC). Unknown sequences consume just the ESC so the walker
// always makes progress.
func escapeLen(s string) int {
	if len(s) < 2 {
		return 1
	}
	switch s[1] {
	case '[': // CSI … final byte in @–~
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']': // OSC … terminated by BEL or ST (ESC \)
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	case 'P', 'X', '^', '_': // DCS/SOS/PM/APC … ST terminated
		for i := 2; i < len(s); i++ {
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	default:
		return 2
	}
}

const resetSeq = "\033[0m"

// TruncateWidth cuts s to at most n display cells on a rune boundary, keeping
// every escape sequence it passed through and appending a reset when the input
// contained styling. Returns s unchanged when it already fits.
func TruncateWidth(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if VisibleWidth(s) <= n {
		return s
	}
	var b strings.Builder
	w := 0
	styled := false
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			l := escapeLen(s[i:])
			b.WriteString(s[i : i+l])
			styled = true
			i += l
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			break
		}
		rw := RuneWidth(r)
		if w+rw > n {
			break
		}
		b.WriteRune(r)
		w += rw
		i += size
	}
	out := b.String()
	if styled {
		out += resetSeq
	}
	return out
}

// ClipWidth truncates to n cells with a trailing ellipsis when it does not fit.
// Unlike Clip it never collapses whitespace, so deliberate indentation is kept.
func ClipWidth(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if VisibleWidth(s) <= n {
		return s
	}
	if n < 2 {
		return TruncateWidth(s, n)
	}
	return TruncateWidth(s, n-1) + "…"
}

// PadWidth right-pads s with spaces to exactly n display cells, truncating when
// it is already wider.
func PadWidth(s string, n int) string {
	w := VisibleWidth(s)
	if w >= n {
		return TruncateWidth(s, n)
	}
	return s + strings.Repeat(" ", n-w)
}

// PadMinWidth pads s to at least n display cells but never truncates — for
// key/value columns where a long key must stay readable.
func PadMinWidth(s string, n int) string {
	w := VisibleWidth(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// PadLeftWidth left-pads s to n display cells.
func PadLeftWidth(s string, n int) string {
	w := VisibleWidth(s)
	if w >= n {
		return TruncateWidth(s, n)
	}
	return strings.Repeat(" ", n-w) + s
}
