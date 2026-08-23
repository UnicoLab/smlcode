package contextstore

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// Excerpt defaults.
const (
	// DefaultWindowLines is the ±N lines emitted around each match.
	DefaultWindowLines = 25
	// DefaultHeadLines is the always-included prologue (package/imports).
	DefaultHeadLines = 15
	// DefaultMaxWindows caps how many separate regions are emitted.
	DefaultMaxWindows = 6
	// DefaultTailLines is the fallback tail kept when nothing matches.
	DefaultTailLines = 40
)

var identifierRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)

// ExcerptOptions configures relevance windowing.
type ExcerptOptions struct {
	MaxBytes   int // hard cap on the produced excerpt
	Window     int // ± lines around each match (default DefaultWindowLines)
	HeadLines  int // always-included prologue (default DefaultHeadLines)
	MaxWindows int // maximum separate regions (default DefaultMaxWindows)
	TailLines  int // fallback tail size (default DefaultTailLines)
}

func (o ExcerptOptions) normalized() ExcerptOptions {
	if o.Window <= 0 {
		o.Window = DefaultWindowLines
	}
	if o.HeadLines <= 0 {
		o.HeadLines = DefaultHeadLines
	}
	if o.MaxWindows <= 0 {
		o.MaxWindows = DefaultMaxWindows
	}
	if o.TailLines <= 0 {
		o.TailLines = DefaultTailLines
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 4000
	}
	return o
}

// ExtractTerms pulls candidate identifiers out of task text (title,
// description, acceptance criteria, query). These are what the worker is
// actually being asked to change, so they are what the excerpt should center on.
func ExtractTerms(texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, tok := range identifierRe.FindAllString(t, -1) {
			lower := strings.ToLower(tok)
			if len(tok) < 3 || excerptStopWord(lower) || seen[lower] {
				continue
			}
			seen[lower] = true
			out = append(out, tok)
		}
		// Bare file paths in the task text are strong hints too.
		for _, f := range strings.Fields(t) {
			f = strings.Trim(f, "`'\",;:()[]{}")
			if strings.Contains(f, ".") && !strings.HasSuffix(f, ".") {
				base := f
				if i := strings.LastIndexByte(base, '/'); i >= 0 {
					base = base[i+1:]
				}
				if i := strings.IndexByte(base, '.'); i > 2 {
					base = base[:i]
				}
				lower := strings.ToLower(base)
				if len(base) >= 3 && !seen[lower] && !excerptStopWord(lower) {
					seen[lower] = true
					out = append(out, base)
				}
			}
		}
	}
	return out
}

// Excerpt returns a relevance-windowed view of content.
//
// Head truncation (the historical behavior) shows the model the license
// header, the package clause and the imports, never the function it must edit,
// and then asks it for an exact old_str match. This instead:
//   - always keeps the first HeadLines lines (package/imports orientation),
//   - scores every line by how many task terms it contains,
//   - emits merged ±Window regions around the best matches,
//   - stamps REAL line numbers on every emitted line,
//   - marks every gap as "… lines A-B elided",
//   - and falls back to head+TAIL (never head-only) when nothing matches.
func Excerpt(content string, terms []string, opts ExcerptOptions) string {
	opts = opts.normalized()
	if content == "" {
		return ""
	}
	if len(content) <= opts.MaxBytes && countLines(content) <= opts.HeadLines+opts.Window {
		return content
	}
	lines := strings.Split(content, "\n")
	n := len(lines)

	matches := scoreLines(lines, terms)
	if len(matches) == 0 {
		return fallbackHeadTail(lines, opts)
	}

	// Take the strongest matches, then merge their windows.
	if len(matches) > opts.MaxWindows {
		matches = matches[:opts.MaxWindows]
	}
	regions := make([][2]int, 0, len(matches)+1)
	if opts.HeadLines > 0 {
		end := opts.HeadLines
		if end > n {
			end = n
		}
		regions = append(regions, [2]int{0, end})
	}
	for _, m := range matches {
		lo := m.line - opts.Window
		if lo < 0 {
			lo = 0
		}
		hi := m.line + opts.Window + 1
		if hi > n {
			hi = n
		}
		regions = append(regions, [2]int{lo, hi})
	}
	regions = mergeRegions(regions)
	out := renderRegions(lines, regions)
	if len(out) <= opts.MaxBytes {
		return out
	}
	// Too big: drop the weakest windows until it fits, always keeping the head.
	for len(matches) > 1 && len(out) > opts.MaxBytes {
		matches = matches[:len(matches)-1]
		regions = regions[:0]
		if opts.HeadLines > 0 {
			end := opts.HeadLines
			if end > n {
				end = n
			}
			regions = append(regions, [2]int{0, end})
		}
		for _, m := range matches {
			lo := m.line - opts.Window
			if lo < 0 {
				lo = 0
			}
			hi := m.line + opts.Window + 1
			if hi > n {
				hi = n
			}
			regions = append(regions, [2]int{lo, hi})
		}
		regions = mergeRegions(regions)
		out = renderRegions(lines, regions)
	}
	return textutil.Truncate(out, opts.MaxBytes, "\n// … truncated\n")
}

type lineMatch struct {
	line  int // 0-based
	score int
}

func scoreLines(lines []string, terms []string) []lineMatch {
	if len(terms) == 0 {
		return nil
	}
	lowered := make([]string, len(terms))
	for i, t := range terms {
		lowered[i] = strings.ToLower(t)
	}
	var out []lineMatch
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		low := strings.ToLower(line)
		score := 0
		for j, t := range lowered {
			if !strings.Contains(low, t) {
				continue
			}
			score += 2
			// Exact-case hit on an identifier boundary is a stronger signal.
			if strings.Contains(line, terms[j]) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		// A declaration line is worth more than a mention inside a body.
		if isDeclarationLine(line) {
			score += 3
		}
		out = append(out, lineMatch{line: i, score: score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score == out[j].score {
			return out[i].line < out[j].line
		}
		return out[i].score > out[j].score
	})
	return out
}

var declPrefixes = []string{
	"func ", "type ", "const ", "var ", "class ", "def ", "async def ",
	"interface ", "struct ", "enum ", "impl ", "fn ", "pub fn ", "export ",
	"public ", "private ", "protected ", "trait ", "module ",
}

func isDeclarationLine(line string) bool {
	t := strings.TrimSpace(line)
	for _, p := range declPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func mergeRegions(regions [][2]int) [][2]int {
	if len(regions) == 0 {
		return nil
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i][0] < regions[j][0] })
	out := [][2]int{regions[0]}
	for _, r := range regions[1:] {
		last := &out[len(out)-1]
		// Merge when overlapping or separated by a gap too small to be worth
		// an elision marker (the marker itself would cost more than the lines).
		if r[0] <= last[1]+2 {
			if r[1] > last[1] {
				last[1] = r[1]
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func renderRegions(lines []string, regions [][2]int) string {
	var b strings.Builder
	prevEnd := 0
	for _, r := range regions {
		if r[0] > prevEnd {
			fmt.Fprintf(&b, "// … lines %d-%d elided\n", prevEnd+1, r[0])
		}
		for i := r[0]; i < r[1] && i < len(lines); i++ {
			fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
		}
		prevEnd = r[1]
	}
	if prevEnd < len(lines) {
		fmt.Fprintf(&b, "// … lines %d-%d elided\n", prevEnd+1, len(lines))
	}
	return b.String()
}

func fallbackHeadTail(lines []string, opts ExcerptOptions) string {
	n := len(lines)
	head := opts.HeadLines + opts.Window
	if head > n {
		head = n
	}
	tail := opts.TailLines
	if head+tail >= n {
		return renderRegions(lines, [][2]int{{0, n}})
	}
	regions := [][2]int{{0, head}, {n - tail, n}}
	out := renderRegions(lines, mergeRegions(regions))
	return textutil.Truncate(out, opts.MaxBytes, "\n// … truncated\n")
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

var excerptStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "into": true, "are": true, "was": true,
	"have": true, "has": true, "not": true, "but": true, "you": true,
	"your": true, "our": true, "all": true, "any": true, "can": true,
	"will": true, "should": true, "must": true, "add": true, "use": true,
	"make": true, "when": true, "then": true, "than": true, "code": true,
	"file": true, "files": true, "func": true, "function": true, "test": true,
	"tests": true, "please": true, "change": true, "update": true, "fix": true,
	"implement": true, "ensure": true, "return": true, "returns": true,
	"string": true, "error": true, "value": true, "task": true, "new": true,
}

func excerptStopWord(lower string) bool { return excerptStopWords[lower] }
