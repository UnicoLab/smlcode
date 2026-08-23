package cli

import (
	"fmt"
	"strings"
)

// Unified-diff computation + colored rendering.
//
// The engine is a plain LCS (Hunt–Szymanski style dynamic programming with a
// common-prefix/suffix fast path) which is O(n·m) in the worst case but linear
// for the overwhelmingly common "agent rewrote a few lines of a file" shape.
// Large inputs fall back to a line-hash bucketed match so a 20k-line file never
// blows up.

// DiffOp is one line-level edit operation.
type DiffOp struct {
	Kind    byte // ' ' context, '-' delete, '+' insert
	Text    string
	OldLine int // 1-based line number in the "before" file (0 for inserts)
	NewLine int // 1-based line number in the "after" file (0 for deletes)
}

// Hunk is a contiguous group of operations with unified-diff coordinates.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []DiffOp
}

// FileDiff is a complete diff for one path.
type FileDiff struct {
	Path      string
	OldPath   string
	Added     int
	Removed   int
	Hunks     []Hunk
	Binary    bool
	IsNew     bool
	IsDeleted bool
	ModeNote  string // e.g. "mode 100755"
}

// Empty reports whether the diff contains no changes.
func (f FileDiff) Empty() bool { return f.Added == 0 && f.Removed == 0 && !f.Binary }

// Stat renders the "±N" summary header fragment, e.g. "+12 -3".
func (f FileDiff) Stat() string {
	return fmt.Sprintf("+%d -%d", f.Added, f.Removed)
}

// splitLines splits content into lines without a trailing empty element for a
// final newline, and reports whether the input ended with a newline.
func splitLines(s string) ([]string, bool) {
	if s == "" {
		return nil, true
	}
	trailing := strings.HasSuffix(s, "\n")
	if trailing {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n"), trailing
}

// IsBinary heuristically detects binary content (NUL byte in the first 8000).
func IsBinary(s string) bool {
	limit := len(s)
	if limit > 8000 {
		limit = 8000
	}
	for i := 0; i < limit; i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// maxDiffCells bounds the LCS table; beyond it the diff degrades to a
// prefix/suffix-anchored replace so huge files stay responsive.
const maxDiffCells = 4_000_000

// Diff computes a unified diff between before and after with the given amount
// of context lines (3 when <= 0).
func Diff(path, before, after string, context int) FileDiff {
	if context <= 0 {
		context = 3
	}
	fd := FileDiff{Path: path, OldPath: path}
	if IsBinary(before) || IsBinary(after) {
		fd.Binary = before != after
		return fd
	}
	a, _ := splitLines(before)
	b, _ := splitLines(after)
	fd.IsNew = before == "" && after != ""
	fd.IsDeleted = after == "" && before != ""

	ops := diffLines(a, b)
	for _, op := range ops {
		switch op.Kind {
		case '+':
			fd.Added++
		case '-':
			fd.Removed++
		}
	}
	fd.Hunks = buildHunks(ops, context)
	return fd
}

// diffLines returns the full op list for two line slices.
func diffLines(a, b []string) []DiffOp {
	// Common prefix.
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	// Common suffix.
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	midA := a[p : len(a)-s]
	midB := b[p : len(b)-s]

	ops := make([]DiffOp, 0, len(a)+len(b))
	for i := 0; i < p; i++ {
		ops = append(ops, DiffOp{Kind: ' ', Text: a[i], OldLine: i + 1, NewLine: i + 1})
	}
	var mid []DiffOp
	if len(midA)*len(midB) > maxDiffCells {
		mid = replaceOps(midA, midB, p, p)
	} else {
		mid = lcsOps(midA, midB, p, p)
	}
	ops = append(ops, mid...)
	for i := 0; i < s; i++ {
		oi := len(a) - s + i
		ni := len(b) - s + i
		ops = append(ops, DiffOp{Kind: ' ', Text: a[oi], OldLine: oi + 1, NewLine: ni + 1})
	}
	return ops
}

// replaceOps is the degraded path: delete everything, then insert everything.
func replaceOps(a, b []string, offA, offB int) []DiffOp {
	ops := make([]DiffOp, 0, len(a)+len(b))
	for i, l := range a {
		ops = append(ops, DiffOp{Kind: '-', Text: l, OldLine: offA + i + 1})
	}
	for i, l := range b {
		ops = append(ops, DiffOp{Kind: '+', Text: l, NewLine: offB + i + 1})
	}
	return ops
}

// lcsOps computes a minimal-ish edit script via an LCS length table.
func lcsOps(a, b []string, offA, offB int) []DiffOp {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return replaceOps(a, b, offA, offB)
	}
	// table[i][j] = LCS length of a[i:] and b[j:]
	table := make([][]int32, n+1)
	buf := make([]int32, (n+1)*(m+1))
	for i := range table {
		table[i] = buf[i*(m+1) : (i+1)*(m+1)]
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	ops := make([]DiffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, DiffOp{Kind: ' ', Text: a[i], OldLine: offA + i + 1, NewLine: offB + j + 1})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, DiffOp{Kind: '-', Text: a[i], OldLine: offA + i + 1})
			i++
		default:
			ops = append(ops, DiffOp{Kind: '+', Text: b[j], NewLine: offB + j + 1})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, DiffOp{Kind: '-', Text: a[i], OldLine: offA + i + 1})
	}
	for ; j < m; j++ {
		ops = append(ops, DiffOp{Kind: '+', Text: b[j], NewLine: offB + j + 1})
	}
	return ops
}

// buildHunks groups ops into unified-diff hunks with `context` context lines.
func buildHunks(ops []DiffOp, context int) []Hunk {
	var hunks []Hunk
	changed := make([]int, 0, len(ops))
	for i, op := range ops {
		if op.Kind != ' ' {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	start := 0
	for start < len(changed) {
		end := start
		for end+1 < len(changed) && changed[end+1]-changed[end] <= 2*context+1 {
			end++
		}
		lo := changed[start] - context
		if lo < 0 {
			lo = 0
		}
		hi := changed[end] + context
		if hi > len(ops)-1 {
			hi = len(ops) - 1
		}
		h := Hunk{Ops: append([]DiffOp(nil), ops[lo:hi+1]...)}
		for _, op := range h.Ops {
			switch op.Kind {
			case ' ':
				h.OldCount++
				h.NewCount++
				if h.OldStart == 0 {
					h.OldStart = op.OldLine
				}
				if h.NewStart == 0 {
					h.NewStart = op.NewLine
				}
			case '-':
				h.OldCount++
				if h.OldStart == 0 {
					h.OldStart = op.OldLine
				}
			case '+':
				h.NewCount++
				if h.NewStart == 0 {
					h.NewStart = op.NewLine
				}
			}
		}
		if h.OldStart == 0 && h.OldCount == 0 {
			h.OldStart = 0
		} else if h.OldStart == 0 {
			h.OldStart = 1
		}
		if h.NewStart == 0 && h.NewCount == 0 {
			h.NewStart = 0
		} else if h.NewStart == 0 {
			h.NewStart = 1
		}
		hunks = append(hunks, h)
		start = end + 1
	}
	return hunks
}

// UnifiedText renders a plain (uncolored) unified diff — the `git diff` format.
func (f FileDiff) UnifiedText() string {
	var b strings.Builder
	if f.Binary {
		fmt.Fprintf(&b, "Binary files a/%s and b/%s differ\n", f.Path, f.Path)
		return b.String()
	}
	if len(f.Hunks) == 0 {
		return ""
	}
	old := "a/" + f.OldPath
	if f.IsNew {
		old = "/dev/null"
	}
	newp := "b/" + f.Path
	if f.IsDeleted {
		newp = "/dev/null"
	}
	fmt.Fprintf(&b, "--- %s\n", old)
	fmt.Fprintf(&b, "+++ %s\n", newp)
	for _, h := range f.Hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
		for _, op := range h.Ops {
			b.WriteByte(op.Kind)
			b.WriteString(op.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// DiffRenderOptions tunes the colored renderer.
type DiffRenderOptions struct {
	Width     int  // terminal width for clipping (0 = no clipping)
	LineNums  bool // show old/new line-number gutter
	WordLevel bool // highlight intra-line changes for 1:1 replacements
	MaxLines  int  // cap rendered body lines (0 = unlimited)
	NoHeader  bool // omit the "path ±N" header (caller already printed one)
}

// DefaultDiffRender is the review-UX preset.
func DefaultDiffRender(width int) DiffRenderOptions {
	return DiffRenderOptions{Width: width, LineNums: true, WordLevel: true, MaxLines: 400}
}

// RenderDiff produces the colored, human-facing diff block including the
// "path  ±N" header.
func RenderDiff(f FileDiff, opt DiffRenderOptions) string {
	var b strings.Builder
	if !opt.NoHeader {
		b.WriteString(RenderDiffHeader(f))
		b.WriteString("\n")
	}
	if f.Binary {
		b.WriteString(Dim("  (binary file — no textual diff)\n"))
		return b.String()
	}
	if len(f.Hunks) == 0 {
		b.WriteString(Dim("  (no changes)\n"))
		return b.String()
	}
	written := 0
	truncated := false
	total := countHunkLines(f.Hunks)
	for _, h := range f.Hunks {
		if opt.MaxLines > 0 && written >= opt.MaxLines {
			truncated = true
			break
		}
		b.WriteString(Cyan(fmt.Sprintf("  @@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount)))
		b.WriteString("\n")
		pairs := pairReplacements(h.Ops)
		for i := 0; i < len(h.Ops); i++ {
			if opt.MaxLines > 0 && written >= opt.MaxLines {
				truncated = true
				break
			}
			op := h.Ops[i]
			if opt.WordLevel {
				if j, ok := pairs[i]; ok && op.Kind == '-' {
					oldHL, newHL := wordHighlight(op.Text, h.Ops[j].Text)
					b.WriteString(renderDiffLine(op, oldHL, opt))
					b.WriteString("\n")
					written++
					b.WriteString(renderDiffLine(h.Ops[j], newHL, opt))
					b.WriteString("\n")
					written++
					i = j
					continue
				}
			}
			b.WriteString(renderDiffLine(op, "", opt))
			b.WriteString("\n")
			written++
		}
	}
	if truncated {
		b.WriteString(Dim(fmt.Sprintf("  … diff truncated (%d more lines) — use [v]iew full\n", maxIntDiff(total-written, 0))))
	}
	return b.String()
}

func maxIntDiff(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func countHunkLines(hs []Hunk) int {
	n := 0
	for _, h := range hs {
		n += len(h.Ops)
	}
	return n
}

// RenderDiffHeader renders "path  +N -M" with new/deleted markers.
func RenderDiffHeader(f FileDiff) string {
	tag := ""
	switch {
	case f.IsNew:
		tag = " " + Green("(new file)")
	case f.IsDeleted:
		tag = " " + Red("(deleted)")
	}
	if f.ModeNote != "" {
		tag += " " + Dim(f.ModeNote)
	}
	return Bold(Accent("▸ "+f.Path)) + "  " +
		Green(fmt.Sprintf("+%d", f.Added)) + " " + Red(fmt.Sprintf("-%d", f.Removed)) + tag
}

// pairReplacements matches each '-' op with the '+' op that replaces it inside
// a balanced run, so word-level highlighting only fires on true 1:1 rewrites.
func pairReplacements(ops []DiffOp) map[int]int {
	out := map[int]int{}
	i := 0
	for i < len(ops) {
		if ops[i].Kind != '-' {
			i++
			continue
		}
		delStart := i
		for i < len(ops) && ops[i].Kind == '-' {
			i++
		}
		delEnd := i
		insStart := i
		for i < len(ops) && ops[i].Kind == '+' {
			i++
		}
		insEnd := i
		if delEnd-delStart == insEnd-insStart && delEnd-delStart == 1 {
			out[delStart] = insStart
		}
	}
	return out
}

func renderDiffLine(op DiffOp, highlighted string, opt DiffRenderOptions) string {
	text := op.Text
	if highlighted != "" {
		text = highlighted
	}
	var gutter string
	if opt.LineNums {
		oldN, newN := "     ", "     "
		if op.OldLine > 0 {
			oldN = fmt.Sprintf("%5d", op.OldLine)
		}
		if op.NewLine > 0 {
			newN = fmt.Sprintf("%5d", op.NewLine)
		}
		gutter = Dim(oldN + " " + newN + " ")
	}
	var body string
	switch op.Kind {
	case '+':
		body = Green("+ " + text)
	case '-':
		body = Red("- " + text)
	default:
		body = Dim("  " + text)
	}
	line := "  " + gutter + body
	if opt.Width > 0 {
		line = ClipWidth(line, opt.Width)
	}
	return line
}

// wordHighlight returns the two lines with the differing spans reverse-video
// highlighted. Falls back to the plain text when the lines share too little.
func wordHighlight(oldLine, newLine string) (string, string) {
	oldTok := tokenizeLine(oldLine)
	newTok := tokenizeLine(newLine)
	if len(oldTok) == 0 || len(newTok) == 0 || len(oldTok)*len(newTok) > 40000 {
		return oldLine, newLine
	}
	ops := lcsOps(oldTok, newTok, 0, 0)
	same := 0
	for _, op := range ops {
		if op.Kind == ' ' {
			same += len(op.Text)
		}
	}
	// Not enough in common — a full-line replace reads better unhighlighted.
	if same*3 < len(oldLine) {
		return oldLine, newLine
	}
	var ob, nb strings.Builder
	for _, op := range ops {
		switch op.Kind {
		case ' ':
			ob.WriteString(op.Text)
			nb.WriteString(op.Text)
		case '-':
			ob.WriteString(Reverse(op.Text))
		case '+':
			nb.WriteString(Reverse(op.Text))
		}
	}
	return ob.String(), nb.String()
}

// tokenizeLine splits a line into word/space/punctuation runs for word-level
// diffing.
func tokenizeLine(s string) []string {
	var out []string
	var cur strings.Builder
	curClass := -1
	class := func(r rune) int {
		switch {
		case r == ' ' || r == '\t':
			return 0
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_':
			return 1
		default:
			return 2
		}
	}
	for _, r := range s {
		c := class(r)
		if c != curClass || c == 2 {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			curClass = c
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// DiffStatLine renders a one-line summary suitable for a list view.
func DiffStatLine(f FileDiff) string {
	bar := ""
	total := f.Added + f.Removed
	if total > 0 {
		n := total
		if n > 20 {
			n = 20
		}
		plus := f.Added * n / total
		bar = " " + Green(strings.Repeat("+", plus)) + Red(strings.Repeat("-", n-plus))
	}
	tag := ""
	if f.IsNew {
		tag = Dim(" (new)")
	} else if f.IsDeleted {
		tag = Dim(" (deleted)")
	}
	return fmt.Sprintf("  %s  %s%s%s", PadWidth(f.Path, 44), Dim(f.Stat()), bar, tag)
}
