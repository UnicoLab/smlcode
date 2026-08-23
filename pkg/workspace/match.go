package workspace

import (
	"fmt"
	"strings"
)

// Multi-strategy edit matching ladder (Aider's "flexible search/replace").
//
// Small models drift on trailing whitespace, indentation and blank lines when
// they re-emit a span they just read. Rather than failing the edit outright we
// try progressively more tolerant matchers, in a fixed order, and stop at the
// FIRST strategy that produces exactly ONE match. A strategy that produces two
// or more candidates is never applied — an ambiguous edit is a wrong edit.
const (
	MatchExact      = "exact"
	MatchTrailingWS = "trailing-whitespace-insensitive"
	MatchIndent     = "indentation-normalized"
	MatchBlankLine  = "blank-line-insensitive"
	MatchAnchored   = "anchored-first-last-line"
)

// matchLadder is the strategy order. Earlier = stricter = preferred.
var matchLadder = []string{MatchExact, MatchTrailingWS, MatchIndent, MatchBlankLine, MatchAnchored}

// EditMatch is a located span of the file plus how it was found.
type EditMatch struct {
	Start    int    // byte offset into content (inclusive)
	End      int    // byte offset into content (exclusive)
	Strategy string // one of the Match* constants
	// Indent is the leading whitespace that must be re-applied to the
	// replacement text when the match was found via MatchIndent.
	Indent string
	// Approx is true when the matched text is not byte-identical to the search
	// block (the caller may want to mention that in its result string).
	Approx bool
}

// MatchOutcome reports the ladder result.
type MatchOutcome struct {
	Match     EditMatch
	Found     bool
	Ambiguous bool   // some strategy matched more than once
	AmbigN    int    // how many times
	AmbigWhat string // which strategy was ambiguous
}

// FindEditMatch runs the fallback ladder over content looking for search.
// Only a unique match is ever returned.
func FindEditMatch(content, search string) MatchOutcome {
	return FindEditMatchIn(content, search, 0, len(content))
}

// FindEditMatchIn restricts the ladder to the byte window [lo,hi).
// Offsets in the returned EditMatch are absolute (relative to content).
func FindEditMatchIn(content, search string, lo, hi int) MatchOutcome {
	var out MatchOutcome
	if search == "" {
		return out
	}
	if lo < 0 {
		lo = 0
	}
	if hi > len(content) || hi <= 0 {
		hi = len(content)
	}
	if lo >= hi {
		return out
	}
	for _, strategy := range matchLadder {
		spans := matchStrategy(content, search, lo, hi, strategy)
		switch {
		case len(spans) == 1:
			out.Match = spans[0]
			out.Found = true
			return out
		case len(spans) > 1 && !out.Ambiguous:
			out.Ambiguous = true
			out.AmbigN = len(spans)
			out.AmbigWhat = strategy
		}
	}
	return out
}

func matchStrategy(content, search string, lo, hi int, strategy string) []EditMatch {
	switch strategy {
	case MatchExact:
		return exactSpans(content, search, lo, hi)
	case MatchTrailingWS:
		return lineSpans(content, search, lo, hi, MatchTrailingWS, trimTrailingWS, false, false)
	case MatchIndent:
		return indentSpans(content, search, lo, hi)
	case MatchBlankLine:
		return blankInsensitiveSpans(content, search, lo, hi)
	case MatchAnchored:
		return anchoredSpans(content, search, lo, hi)
	}
	return nil
}

func exactSpans(content, search string, lo, hi int) []EditMatch {
	var out []EditMatch
	region := content[lo:hi]
	from := 0
	for {
		i := strings.Index(region[from:], search)
		if i < 0 {
			return out
		}
		at := lo + from + i
		out = append(out, EditMatch{Start: at, End: at + len(search), Strategy: MatchExact})
		from += i + 1
		if from >= len(region) {
			return out
		}
		if len(out) > 64 { // ambiguity is already established
			return out
		}
	}
}

// lineIndex is a precomputed line table for a string.
type lineIndex struct {
	starts []int // byte offset of each line start
	ends   []int // byte offset just past each line's text (excluding \n)
	text   []string
}

func indexLines(s string) lineIndex {
	var li lineIndex
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			li.starts = append(li.starts, start)
			li.ends = append(li.ends, i)
			li.text = append(li.text, s[start:i])
			start = i + 1
			if i == len(s) {
				break
			}
		}
	}
	return li
}

func trimTrailingWS(s string) string { return strings.TrimRight(s, " \t\r") }

func trimAllWS(s string) string { return strings.TrimSpace(s) }

// lineSpans scans line-aligned windows comparing each line under norm.
// dropBlankFile/dropBlankSearch skip blank lines on the respective side.
func lineSpans(content, search string, lo, hi int, strategy string,
	norm func(string) string, dropBlankFile, dropBlankSearch bool) []EditMatch {

	fileLines := indexLines(content)
	searchLines := strings.Split(strings.TrimRight(search, "\n"), "\n")
	if dropBlankSearch {
		searchLines = dropBlanks(searchLines)
	}
	if len(searchLines) == 0 {
		return nil
	}
	var out []EditMatch
	for i := range fileLines.text {
		if fileLines.starts[i] < lo || fileLines.starts[i] >= hi {
			continue
		}
		j := i
		k := 0
		for k < len(searchLines) && j < len(fileLines.text) {
			if dropBlankFile && strings.TrimSpace(fileLines.text[j]) == "" &&
				strings.TrimSpace(searchLines[k]) != "" {
				j++
				continue
			}
			if norm(fileLines.text[j]) != norm(searchLines[k]) {
				break
			}
			j++
			k++
		}
		if k != len(searchLines) {
			continue
		}
		end := fileLines.ends[j-1]
		if end > hi {
			continue
		}
		out = append(out, EditMatch{
			Start: fileLines.starts[i], End: end, Strategy: strategy,
			Approx: content[fileLines.starts[i]:end] != strings.TrimRight(search, "\n"),
		})
		if len(out) > 8 {
			return out
		}
	}
	return out
}

func dropBlanks(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// indentSpans matches when every line agrees after stripping leading
// whitespace AND the RELATIVE indentation of the block is preserved. This
// catches a model that re-emitted a method body at the wrong nesting level.
func indentSpans(content, search string, lo, hi int) []EditMatch {
	fileLines := indexLines(content)
	searchLines := strings.Split(strings.TrimRight(search, "\n"), "\n")
	if len(searchLines) == 0 {
		return nil
	}
	sRel := relativeIndents(searchLines)
	var out []EditMatch
	for i := range fileLines.text {
		if fileLines.starts[i] < lo || fileLines.starts[i] >= hi {
			continue
		}
		if i+len(searchLines) > len(fileLines.text) {
			break
		}
		window := fileLines.text[i : i+len(searchLines)]
		ok := true
		for k := range searchLines {
			if strings.TrimSpace(window[k]) != strings.TrimSpace(searchLines[k]) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		wRel := relativeIndents(window)
		if len(wRel) != len(sRel) {
			continue
		}
		for k := range wRel {
			if wRel[k] != sRel[k] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		end := fileLines.ends[i+len(searchLines)-1]
		if end > hi {
			continue
		}
		out = append(out, EditMatch{
			Start: fileLines.starts[i], End: end, Strategy: MatchIndent,
			Indent: leadingWS(firstNonBlank(window)), Approx: true,
		})
		if len(out) > 8 {
			return out
		}
	}
	return out
}

// relativeIndents returns each line's indent width minus the block's minimum
// indent. Blank lines are recorded as -1 so they never skew the baseline.
func relativeIndents(lines []string) []int {
	min := -1
	widths := make([]int, len(lines))
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			widths[i] = -1
			continue
		}
		w := indentWidth(l)
		widths[i] = w
		if min < 0 || w < min {
			min = w
		}
	}
	if min < 0 {
		min = 0
	}
	for i := range widths {
		if widths[i] >= 0 {
			widths[i] -= min
		}
	}
	return widths
}

// indentWidth counts leading whitespace with a tab worth 4 columns so a
// tab-vs-spaces reindent still compares equal in relative terms.
func indentWidth(s string) int {
	w := 0
	for _, r := range s {
		switch r {
		case ' ':
			w++
		case '\t':
			w += 4
		default:
			return w
		}
	}
	return w
}

func leadingWS(s string) string {
	for i, r := range s {
		if r != ' ' && r != '\t' {
			return s[:i]
		}
	}
	return s
}

func firstNonBlank(lines []string) string {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	if len(lines) > 0 {
		return lines[0]
	}
	return ""
}

// blankInsensitiveSpans ignores blank lines on both sides.
func blankInsensitiveSpans(content, search string, lo, hi int) []EditMatch {
	return lineSpans(content, search, lo, hi, MatchBlankLine, trimAllWS, true, true)
}

// anchoredSpans matches on the first and last non-blank line of the search
// block with a tolerant middle. The window must have the same line count as
// the search block so we never silently swallow unrelated code.
func anchoredSpans(content, search string, lo, hi int) []EditMatch {
	searchLines := strings.Split(strings.TrimRight(search, "\n"), "\n")
	if len(searchLines) < 3 {
		// With <3 lines there is no "middle" to be tolerant about; the earlier
		// strategies already covered those cases.
		return nil
	}
	first := strings.TrimSpace(searchLines[0])
	last := strings.TrimSpace(searchLines[len(searchLines)-1])
	// The first line must be substantial enough to be a real anchor; the last
	// is very often a bare "}" or ")" and must not be rejected for that.
	if first == "" || last == "" || len(first) < 3 {
		return nil
	}
	fileLines := indexLines(content)
	n := len(searchLines)
	var out []EditMatch
	for i := range fileLines.text {
		if fileLines.starts[i] < lo || fileLines.starts[i] >= hi {
			continue
		}
		if i+n > len(fileLines.text) {
			break
		}
		if strings.TrimSpace(fileLines.text[i]) != first {
			continue
		}
		if strings.TrimSpace(fileLines.text[i+n-1]) != last {
			continue
		}
		end := fileLines.ends[i+n-1]
		if end > hi {
			continue
		}
		out = append(out, EditMatch{
			Start: fileLines.starts[i], End: end, Strategy: MatchAnchored, Approx: true,
		})
		if len(out) > 8 {
			return out
		}
	}
	return out
}

// ApplyEditMatch splices replacement into content at the located span,
// re-indenting the replacement when the indentation-normalized strategy hit.
func ApplyEditMatch(content string, m EditMatch, replacement string) string {
	if m.Strategy == MatchIndent && m.Indent != "" {
		replacement = reindent(replacement, m.Indent)
	}
	return content[:m.Start] + replacement + content[m.End:]
}

// ApplyEditReplacement splices newStr in place of the matched span, adjusting
// trailing newlines for the line-oriented strategies.
//
// MatchExact spans exactly the bytes of oldStr, so newStr is used verbatim.
// Every other strategy is line-aligned: its span stops at the end of the last
// line's TEXT (the "\n" belongs to the file, not to the match), so the
// replacement must not carry its own trailing newline.
func ApplyEditReplacement(content string, m EditMatch, oldStr, newStr string) string {
	repl := newStr
	if m.Strategy != MatchExact {
		repl = alignTrailingNewlines(oldStr, newStr)
	}
	return ApplyEditMatch(content, m, repl)
}

// alignTrailingNewlines keeps the replacement's trailing-newline count sane
// after the line-oriented ladder trimmed the search block's own trailing "\n".
func alignTrailingNewlines(oldStr, newStr string) string {
	oldTrail := len(oldStr) - len(strings.TrimRight(oldStr, "\n"))
	newTrail := len(newStr) - len(strings.TrimRight(newStr, "\n"))
	extra := newTrail - oldTrail
	if extra < 0 {
		extra = 0
	}
	return strings.TrimRight(newStr, "\n") + strings.Repeat("\n", extra)
}

// reindent rebases a replacement block onto the target's leading whitespace.
func reindent(block, indent string) string {
	lines := strings.Split(block, "\n")
	base := ""
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		base = leadingWS(l)
		break
	}
	if base == indent {
		return block
	}
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = indent + strings.TrimPrefix(l, base)
	}
	return strings.Join(lines, "\n")
}

// StrategyNote renders a short human/model-facing note about how a match was
// located, so the model learns its old_str drifted from the file.
func StrategyNote(strategy string) string {
	switch strategy {
	case "", MatchExact:
		return ""
	case MatchTrailingWS:
		return " [matched ignoring trailing whitespace — your old_str had different line endings]"
	case MatchIndent:
		return " [matched after normalizing indentation — your old_str was indented differently]"
	case MatchBlankLine:
		return " [matched ignoring blank lines — your old_str had different blank-line spacing]"
	case MatchAnchored:
		return " [matched on first+last line anchors — the middle of your old_str did not match exactly; verify the result with ws_read]"
	}
	return ""
}

// AmbiguityMessage is the model-facing refusal for a non-unique match.
func AmbiguityMessage(path string, n int, strategy string) string {
	return fmt.Sprintf(
		"Ambiguous edit refused — the search text matches %d places in %s (%s match). "+
			"Add 2–3 more surrounding lines to old_str so exactly one location matches, "+
			"or pass replace_all:true if you really mean every occurrence.",
		n, path, strategy,
	)
}
