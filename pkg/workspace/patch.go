package workspace

import (
	"fmt"
	"strconv"
	"strings"
)

// AnchorWindowLines is how far from the @@ line number a hunk is searched
// before falling back to a whole-file (uniqueness-checked) search.
const AnchorWindowLines = 20

// ApplyPatch applies a SEARCH/REPLACE block or a unified diff to content.
// Supported forms:
//  1. <<<<<<< SEARCH ... ======= ... >>>>>>> REPLACE
//  2. One or more @@ hunks with -/+/space lines (line numbers are USED)
//  3. A bare -/+ block with no @@ header (treated as a single anchorless hunk)
func ApplyPatch(content, patch string) (next string, summary string, err error) {
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	patch = strings.TrimSpace(patch)
	if patch == "" {
		return "", "", fmt.Errorf("empty patch — supply a unified diff hunk or a <<<<<<< SEARCH/======= />>>>>>> REPLACE block")
	}
	// Reject obvious junk / wandering payloads early (anti-wander for SLMs).
	if looksLikeJunkPatch(patch) {
		return "", "", fmt.Errorf("rejected bad patch format — use a single SEARCH/REPLACE or one unified diff hunk (no prose, no multiple files)")
	}

	if strings.Contains(patch, "<<<<<<<") {
		return applySearchReplace(content, patch)
	}
	// SEARCH / REPLACE without the git conflict markers: "SEARCH\n…\n=======\n…"
	// with or without a REPLACE header or a ">>>>>>> REPLACE" trailer. This
	// form used to be routed to applySearchReplace (which insists on <<<<<<<)
	// whenever the words SEARCH/REPLACE appeared, so it was unreachable.
	if oldStr, newStr, ok := splitBareSearchReplace(patch); ok {
		return applyExact(content, oldStr, newStr)
	}
	if strings.Contains(patch, "@@") || strings.HasPrefix(patch, "---") || looksLikeDiff(patch) {
		return applyUnifiedHunks(content, patch)
	}
	return "", "", fmt.Errorf("unrecognized patch format — use <<<<<<< SEARCH / ======= / >>>>>>> REPLACE, or a unified diff with @@ hunks")
}

func applySearchReplace(content, patch string) (string, string, error) {
	start := strings.Index(patch, "<<<<<<<")
	mid := strings.Index(patch, "=======")
	end := strings.Index(patch, ">>>>>>>")
	if start < 0 || mid < 0 || end < 0 || mid < start || end < mid {
		return "", "", fmt.Errorf("malformed SEARCH/REPLACE markers — expected exactly:\n<<<<<<< SEARCH\n<old text>\n=======\n<new text>\n>>>>>>> REPLACE")
	}
	oldBlock := patch[start:mid]
	// drop marker line
	if i := strings.Index(oldBlock, "\n"); i >= 0 {
		oldBlock = oldBlock[i+1:]
	} else {
		oldBlock = ""
	}
	newBlock := patch[mid:end]
	if i := strings.Index(newBlock, "\n"); i >= 0 {
		newBlock = newBlock[i+1:]
	} else {
		newBlock = ""
	}
	oldBlock = strings.TrimRight(oldBlock, "\n")
	newBlock = strings.TrimRight(newBlock, "\n")
	return applyExact(content, oldBlock, newBlock)
}

// splitBareSearchReplace parses the marker-less SEARCH/REPLACE form. The
// separator is a line that is exactly "=======" (the FIRST one — a later one
// may be a Markdown rule inside the replacement). An optional "SEARCH" header
// line, an optional "REPLACE" header line after the separator and an optional
// ">>>>>>> REPLACE" trailer are all tolerated and dropped.
func splitBareSearchReplace(patch string) (oldStr, newStr string, ok bool) {
	lines := strings.Split(patch, "\n")
	sep := -1
	for i, ln := range lines {
		// Exactly "=======" at column 0: a unified-diff context line reads
		// " =======" and a removed one "-=======", neither is a separator.
		if strings.TrimRight(ln, " \t\r") == "=======" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return "", "", false
	}
	before := lines[:sep]
	after := lines[sep+1:]
	isHeader := func(ln, word string) bool {
		t := strings.ToUpper(strings.TrimSpace(ln))
		return t == word || t == word+":"
	}
	if len(before) > 0 && isHeader(before[0], "SEARCH") {
		before = before[1:]
	}
	if len(after) > 0 && isHeader(after[0], "REPLACE") {
		after = after[1:]
	}
	if n := len(after); n > 0 {
		t := strings.TrimSpace(after[n-1])
		if strings.HasPrefix(t, ">>>>>>>") || strings.EqualFold(t, "REPLACE") {
			after = after[:n-1]
		}
	}
	// The word SEARCH or REPLACE somewhere in the text is not enough on its
	// own: a unified diff that happens to touch a line saying "=======" must
	// still be a unified diff.
	if strings.Contains(patch, "@@") && !strings.Contains(strings.ToUpper(patch), "SEARCH") {
		return "", "", false
	}
	oldStr = strings.TrimRight(strings.Join(before, "\n"), "\n")
	newStr = strings.TrimRight(strings.Join(after, "\n"), "\n")
	return oldStr, newStr, true
}

// applyExact locates oldStr with the full match ladder and refuses to apply a
// non-unique match. The old behavior (strings.Replace ..., 1 — first
// occurrence wins) silently patched the wrong function.
func applyExact(content, oldStr, newStr string) (string, string, error) {
	if oldStr == "" {
		if content != "" {
			return "", "", fmt.Errorf(
				"empty SEARCH block is only valid for creating a new empty file, but the file already has %d bytes. "+
					"Put the exact text you want to replace between <<<<<<< SEARCH and =======", len(content))
		}
		return newStr, fmt.Sprintf("create %d bytes", len(newStr)), nil
	}
	if n := strings.Count(content, oldStr); n > 1 {
		return "", "", fmt.Errorf(
			"SEARCH block matches %d places in the file — refusing an ambiguous patch. "+
				"Add 2–3 more surrounding lines to the SEARCH block so exactly one location matches", n)
	}
	res := FindEditMatch(content, oldStr)
	if !res.Found {
		if res.Ambiguous {
			return "", "", fmt.Errorf(
				"SEARCH block matches %d places (%s match) — refusing an ambiguous patch. "+
					"Add 2–3 more surrounding lines so exactly one location matches",
				res.AmbigN, res.AmbigWhat)
		}
		return "", "", fmt.Errorf(
			"SEARCH block not found in file. ws_read the exact span first and copy the text WITHOUT the line-number prefix, then retry")
	}
	next := ApplyEditReplacement(content, res.Match, oldStr, newStr)
	return next, diffSnippet(oldStr, newStr) + StrategyNote(res.Match.Strategy), nil
}

func looksLikeDiff(patch string) bool {
	hasMinus, hasPlus := false, false
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			hasMinus = true
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			hasPlus = true
		}
	}
	return hasMinus || hasPlus
}

// hunk is one @@ block of a unified diff.
type hunk struct {
	OldStart int // 1-based; 0 when the @@ header carried no numbers
	OldLen   int
	NewStart int
	NewLen   int
	Old      []string // context + removed lines
	New      []string // context + added lines
	Header   string
}

// ParseUnifiedHunks splits a unified diff into hunks, keeping @@ line numbers.
//
// A line is only treated as a FILE HEADER when it appears before the first @@
// and looks like one ("--- a/x", "+++ b/x", "--- /dev/null"). Once a hunk is
// open, "---" and "+++" are ordinary removed/added content — the old parser
// swallowed Python docstrings and Markdown rules as headers.
func ParseUnifiedHunks(patch string) ([]hunk, error) {
	lines := strings.Split(patch, "\n")
	var hunks []hunk
	var cur *hunk
	seenHunk := false
	// pending holds unprefixed lines that have not yet been proven to be
	// context. An unprefixed line FOLLOWED by a real diff line is context a
	// model forgot to prefix (blank lines especially); an unprefixed line at
	// the END of a hunk is trailing prose ("This adds the check.") or a
	// closing code fence, and the old parser turned both into context lines
	// that could never match, failing the whole hunk.
	var pending []string
	flush := func() {
		if cur == nil {
			return
		}
		for _, l := range pending {
			cur.Old = append(cur.Old, l)
			cur.New = append(cur.New, l)
		}
		pending = nil
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			pending = nil // trailing prose of the previous hunk
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			h := parseHunkHeader(line)
			cur = &h
			seenHunk = true
			continue
		}
		if !seenHunk && isDiffFileHeader(line) {
			continue
		}
		if line == `\ No newline at end of file` {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			// A Markdown fence is never file content on either side.
			continue
		}
		switch {
		case strings.HasPrefix(line, "-"), strings.HasPrefix(line, "+"), strings.HasPrefix(line, " "):
			if cur == nil {
				// Bare -/+ block with no @@ header: open an anchorless hunk.
				cur = &hunk{Header: "(no @@ header)"}
				seenHunk = true
				// Leading prose ("Here is the diff:") is not context; a
				// leading unprefixed CODE line is kept, as before.
				pending = dropLeadingProse(pending)
			}
			flush()
			switch line[0] {
			case '-':
				cur.Old = append(cur.Old, line[1:])
			case '+':
				cur.New = append(cur.New, line[1:])
			default:
				cur.Old = append(cur.Old, line[1:])
				cur.New = append(cur.New, line[1:])
			}
		default:
			// An unprefixed line: blank context a model emitted as "" instead
			// of " ", forgotten-prefix context, or prose. Decided when we see
			// what follows it.
			pending = append(pending, line)
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("no diff hunks found — a unified diff needs @@ or -/+ prefixed lines")
	}
	// Drop a trailing all-empty hunk produced by a trailing newline.
	for len(hunks) > 0 {
		last := hunks[len(hunks)-1]
		if len(last.Old) == 0 && len(last.New) == 0 {
			hunks = hunks[:len(hunks)-1]
			continue
		}
		break
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("diff hunk produced empty change — nothing to apply")
	}
	return hunks, nil
}

func isDiffFileHeader(line string) bool {
	switch {
	case line == "---", line == "+++":
		return true
	case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
		return true
	case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
		return true
	}
	return false
}

// parseHunkHeader reads "@@ -12,7 +12,9 @@ optional trailing context".
func parseHunkHeader(line string) hunk {
	h := hunk{Header: strings.TrimSpace(line)}
	body := line
	if i := strings.Index(body[2:], "@@"); i >= 0 {
		body = body[2 : 2+i]
	} else {
		body = strings.TrimPrefix(body, "@@")
	}
	for _, f := range strings.Fields(body) {
		if len(f) < 2 {
			continue
		}
		sign := f[0]
		if sign != '-' && sign != '+' {
			continue
		}
		start, length := parseRange(f[1:])
		if sign == '-' {
			h.OldStart, h.OldLen = start, length
		} else {
			h.NewStart, h.NewLen = start, length
		}
	}
	return h
}

func parseRange(s string) (start, length int) {
	length = 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			length = n
		}
		s = s[:i]
	}
	if n, err := strconv.Atoi(s); err == nil {
		start = n
	}
	return start, length
}

// hunkResult records what happened to one hunk (for the per-hunk report).
type hunkResult struct {
	Index    int
	Total    int
	OK       bool
	Strategy string
	Detail   string
}

func (r hunkResult) String() string {
	if r.OK {
		return fmt.Sprintf("hunk %d/%d ok (%s)", r.Index, r.Total, r.Strategy)
	}
	return fmt.Sprintf("hunk %d/%d FAILED: %s", r.Index, r.Total, r.Detail)
}

// applyUnifiedHunks applies every hunk independently, anchored on its @@ line
// number, and reports per-hunk status.
//
// The whole patch is all-or-nothing: if ANY hunk fails, nothing is written.
// A half-applied diff is worse than no diff — the model then has to reason
// about a file that matches neither its old nor its new mental model.
func applyUnifiedHunks(content, patch string) (string, string, error) {
	hunks, err := ParseUnifiedHunks(patch)
	if err != nil {
		return "", "", err
	}
	cur := content
	var results []hunkResult
	var strategies []string
	lineDelta := 0
	failed := false
	for i, h := range hunks {
		res := hunkResult{Index: i + 1, Total: len(hunks)}
		oldStr := strings.Join(h.Old, "\n")
		newStr := strings.Join(h.New, "\n")
		if oldStr == "" && newStr == "" {
			res.OK = true
			res.Strategy = "empty"
			results = append(results, res)
			continue
		}
		if strings.TrimSpace(oldStr) == "" && newStr != "" {
			// Pure insertion.
			next, at := insertHunk(cur, h, newStr, lineDelta)
			cur = next
			lineDelta += len(h.New) - len(h.Old)
			res.OK = true
			res.Strategy = fmt.Sprintf("insert at line %d", at)
			results = append(results, res)
			strategies = append(strategies, "insert")
			continue
		}
		match, strategy, ferr := locateHunk(cur, h, oldStr, lineDelta)
		if ferr != nil {
			res.Detail = ferr.Error()
			results = append(results, res)
			failed = true
			continue
		}
		cur = ApplyEditReplacement(cur, match, oldStr, newStr)
		lineDelta += len(h.New) - len(h.Old)
		res.OK = true
		res.Strategy = strategy
		results = append(results, res)
		strategies = append(strategies, strategy)
	}
	if failed {
		var b strings.Builder
		b.WriteString("patch not applied — no changes were written (all-or-nothing).\n")
		for _, r := range results {
			b.WriteString("  ")
			b.WriteString(r.String())
			b.WriteString("\n")
		}
		b.WriteString(
			"RECOVERY: ws_read the exact line span, then retry with ws_edit (old_str/new_str) " +
				"for the single hunk that failed — one hunk per call is far more reliable than a multi-hunk diff.")
		return "", "", fmt.Errorf("%s", b.String())
	}
	summary := fmt.Sprintf("%d hunk(s) applied", len(hunks))
	if len(strategies) > 0 {
		summary += " [" + strings.Join(uniqueStrings(strategies), ", ") + "]"
	}
	return cur, summary, nil
}

// locateHunk finds the hunk's old block, preferring a window around the @@
// line number (±AnchorWindowLines) before considering the whole file.
func locateHunk(content string, h hunk, oldStr string, lineDelta int) (EditMatch, string, error) {
	if h.OldStart > 0 {
		lo, hi, from, to := anchorWindow(content, h.OldStart+lineDelta, len(h.Old))
		if lo < hi {
			res := FindEditMatchIn(content, oldStr, lo, hi)
			if res.Found {
				return res.Match, fmt.Sprintf("anchored@%d..%d %s", from, to, res.Match.Strategy), nil
			}
		}
	}
	res := FindEditMatch(content, oldStr)
	if res.Found {
		if h.OldStart > 0 {
			return res.Match, "whole-file " + res.Match.Strategy, nil
		}
		return res.Match, res.Match.Strategy, nil
	}
	if res.Ambiguous {
		return EditMatch{}, "", fmt.Errorf(
			"context matches %d places in the file (%s) near line %d — ambiguous, add more context lines to the hunk",
			res.AmbigN, res.AmbigWhat, h.OldStart)
	}
	near := h.OldStart + lineDelta
	detail := fmt.Sprintf("context not found in file (hunk header %s)", h.Header)
	if h.OldStart > 0 {
		detail = fmt.Sprintf("context not found near line %d (hunk header %s)", near, h.Header)
	}
	if closest := closestTextHint(content, h.Old, near); closest != "" {
		detail += " — closest text is:\n" + closest
	}
	return EditMatch{}, "", fmt.Errorf("%s", detail)
}

// anchorWindow returns the byte range covering [start-N, start+len+N] lines.
func anchorWindow(content string, start, oldLen int) (lo, hi, fromLine, toLine int) {
	li := indexLines(content)
	if len(li.starts) == 0 {
		return 0, 0, 0, 0
	}
	from := start - AnchorWindowLines
	if from < 1 {
		from = 1
	}
	to := start + oldLen + AnchorWindowLines
	if to > len(li.starts) {
		to = len(li.starts)
	}
	if from > len(li.starts) {
		return 0, 0, 0, 0
	}
	lo = li.starts[from-1]
	hi = li.ends[to-1]
	if hi < lo {
		return 0, 0, 0, 0
	}
	return lo, hi, from, to
}

// closestTextHint shows the file lines nearest the expected location, WITHOUT
// line-number prefixes — the model must be able to copy them verbatim.
func closestTextHint(content string, oldLines []string, near int) string {
	li := indexLines(content)
	if len(li.text) == 0 {
		return ""
	}
	// Prefer a real fuzzy hit on the hunk's first non-blank removed line.
	key := ""
	for _, l := range oldLines {
		if strings.TrimSpace(l) != "" {
			key = strings.TrimSpace(l)
			break
		}
	}
	best := -1
	if len(key) >= 4 {
		for i, l := range li.text {
			if strings.Contains(squashWS(l), squashWS(key)) {
				best = i
				break
			}
		}
	}
	if best < 0 {
		if near < 1 || near > len(li.text) {
			return ""
		}
		best = near - 1
	}
	lo := best - 1
	if lo < 0 {
		lo = 0
	}
	hi := best + 3
	if hi > len(li.text) {
		hi = len(li.text)
	}
	return strings.Join(li.text[lo:hi], "\n") +
		fmt.Sprintf("\n(lines %d–%d — copy this text verbatim, no line numbers)", lo+1, hi)
}

// dropLeadingProse trims blank lines and sentence-shaped lines ("Here is the
// diff:") from the front of an anchorless hunk's unprefixed prefix, keeping a
// leading code line the model merely forgot to prefix.
func dropLeadingProse(pending []string) []string {
	for len(pending) > 0 {
		t := strings.TrimSpace(pending[0])
		if t == "" || (strings.HasSuffix(t, ":") && strings.Contains(t, " ")) {
			pending = pending[1:]
			continue
		}
		break
	}
	return pending
}

// insertHunk places a pure-addition hunk, or appends when it has no anchor.
//
// A hunk with no removed lines ("@@ -N,0 +M,K @@") has unified-diff semantics
// of "insert AFTER line N" (N = 0 means at the very top). It used to be
// inserted BEFORE line N, one line too early — an import added "after line 3"
// landed between lines 2 and 3. When the hunk does carry old lines that are
// all blank (blank context around the insertion), those blank lines are
// replaced in place when the file has them at the anchor, otherwise the block
// goes before the anchor as before. The returned int is the 1-based line the
// new text now starts on (0 for an unanchored append).
func insertHunk(content string, h hunk, newStr string, lineDelta int) (string, int) {
	appendAtEnd := func() string {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + newStr + "\n"
	}
	if h.NewStart <= 0 && h.OldStart <= 0 && !strings.HasPrefix(h.Header, "@@") {
		// Headerless (anchorless) additions: nowhere to anchor, append.
		return appendAtEnd(), 0
	}
	li := indexLines(content)
	if len(h.Old) == 0 {
		// Pure insertion: after line N.
		after := h.OldStart + lineDelta
		if after < 0 {
			after = 0
		}
		if after >= len(li.starts) || (after == len(li.starts)-1 && li.text[after] == "" && strings.HasSuffix(content, "\n")) {
			return appendAtEnd(), after + 1
		}
		off := li.starts[after]
		return content[:off] + newStr + "\n" + content[off:], after + 1
	}
	// Blank context lines on the old side: replace them in place when they
	// really are blank in the file at the anchor.
	at := h.OldStart + lineDelta
	if at <= 0 {
		at = h.NewStart
	}
	if at < 1 {
		at = 1
	}
	if at > len(li.starts) {
		return appendAtEnd(), len(li.starts) + 1
	}
	if end := at - 1 + len(h.Old); end <= len(li.starts) {
		blank := true
		for i := at - 1; i < end; i++ {
			if strings.TrimSpace(li.text[i]) != "" {
				blank = false
				break
			}
		}
		if blank {
			lo := li.starts[at-1]
			hi := li.ends[end-1]
			return content[:lo] + newStr + content[hi:], at
		}
	}
	off := li.starts[at-1]
	return content[:off] + newStr + "\n" + content[off:], at
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func looksLikeJunkPatch(patch string) bool {
	lower := strings.ToLower(patch)
	// Multiple file headers in one patch → agents wandering across files.
	if strings.Count(patch, "\n+++ ") > 1 || strings.Count(patch, "\n--- ") > 1 {
		return true
	}
	// Prose wrappers without any patch markers.
	hasMarkers := strings.Contains(patch, "<<<<<<<") || strings.Contains(patch, "=======") ||
		strings.Contains(patch, "@@") || strings.HasPrefix(patch, "---") || looksLikeDiff(patch)
	if !hasMarkers {
		return true
	}
	// Extremely large patches are almost always full-file rewrites / wander.
	if len(patch) > 80_000 {
		return true
	}
	// Common SLM failure: "Here is the patch:" essays.
	if strings.Contains(lower, "here is the") && !strings.Contains(patch, "<<<<<<<") && !strings.Contains(patch, "@@") {
		return true
	}
	return false
}

func truncateSnippet(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func diffSnippet(oldStr, newStr string) string {
	o := truncateSnippet(oldStr, 160)
	n := truncateSnippet(newStr, 160)
	if o == "" {
		return "+ " + n
	}
	return "- " + o + "\n+ " + n
}
