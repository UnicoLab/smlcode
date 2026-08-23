package server

import "strings"

// DiffOp is a single line operation in a unified diff.
type DiffOp struct {
	// Type is "equal", "insert" or "delete".
	Type string `json:"type"`
	// OldLine / NewLine are 1-based line numbers, 0 when not applicable.
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
	Text    string `json:"text"`
}

// DiffHunk groups contiguous operations with a few lines of context.
type DiffHunk struct {
	OldStart int      `json:"old_start"`
	OldLines int      `json:"old_lines"`
	NewStart int      `json:"new_start"`
	NewLines int      `json:"new_lines"`
	Ops      []DiffOp `json:"ops"`
}

// DiffStat summarizes a change.
type DiffStat struct {
	Added   int  `json:"added"`
	Removed int  `json:"removed"`
	Binary  bool `json:"binary"`
}

// DiffResult is the full comparison of two file versions.
type DiffResult struct {
	Stat  DiffStat   `json:"stat"`
	Hunks []DiffHunk `json:"hunks"`
	// Truncated is set when either side exceeded the line budget and the diff
	// was computed on a prefix only.
	Truncated bool `json:"truncated,omitempty"`
}

// maxDiffLines bounds the O(n*m) LCS table so a huge generated file cannot
// stall the Studio API goroutine.
const maxDiffLines = 4000

// ComputeDiff produces a unified, hunked line diff between before and after.
// context is the number of unchanged lines kept around each change.
func ComputeDiff(before, after string, context int) DiffResult {
	if context < 0 {
		context = 0
	}
	if isBinary(before) || isBinary(after) {
		return DiffResult{Stat: DiffStat{Binary: true}}
	}
	a := splitLines(before)
	b := splitLines(after)
	truncated := false
	if len(a) > maxDiffLines {
		a = a[:maxDiffLines]
		truncated = true
	}
	if len(b) > maxDiffLines {
		b = b[:maxDiffLines]
		truncated = true
	}

	ops := lineOps(a, b)
	stat := DiffStat{}
	for _, op := range ops {
		switch op.Type {
		case "insert":
			stat.Added++
		case "delete":
			stat.Removed++
		}
	}
	return DiffResult{Stat: stat, Hunks: buildHunks(ops, context), Truncated: truncated}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	// A trailing newline produces a final empty element that is not a line.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isBinary(s string) bool {
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

// lineOps runs a classic LCS diff after trimming the common prefix/suffix,
// which keeps the quadratic core small for the typical "one edited region"
// agent patch.
func lineOps(a, b []string) []DiffOp {
	var ops []DiffOp
	oldNo, newNo := 1, 1

	// Common prefix.
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		ops = append(ops, DiffOp{Type: "equal", OldLine: oldNo, NewLine: newNo, Text: a[p]})
		oldNo++
		newNo++
		p++
	}
	// Common suffix.
	sa, sb := len(a), len(b)
	for sa > p && sb > p && a[sa-1] == b[sb-1] {
		sa--
		sb--
	}

	midA, midB := a[p:sa], b[p:sb]
	table := lcsTable(midA, midB)
	i, j := 0, 0
	for i < len(midA) && j < len(midB) {
		switch {
		case midA[i] == midB[j]:
			ops = append(ops, DiffOp{Type: "equal", OldLine: oldNo, NewLine: newNo, Text: midA[i]})
			oldNo++
			newNo++
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, DiffOp{Type: "delete", OldLine: oldNo, Text: midA[i]})
			oldNo++
			i++
		default:
			ops = append(ops, DiffOp{Type: "insert", NewLine: newNo, Text: midB[j]})
			newNo++
			j++
		}
	}
	for ; i < len(midA); i++ {
		ops = append(ops, DiffOp{Type: "delete", OldLine: oldNo, Text: midA[i]})
		oldNo++
	}
	for ; j < len(midB); j++ {
		ops = append(ops, DiffOp{Type: "insert", NewLine: newNo, Text: midB[j]})
		newNo++
	}

	// Trailing common suffix.
	for k := sa; k < len(a); k++ {
		ops = append(ops, DiffOp{Type: "equal", OldLine: oldNo, NewLine: newNo, Text: a[k]})
		oldNo++
		newNo++
	}
	return ops
}

// lcsTable[i][j] is the LCS length of a[i:] and b[j:].
func lcsTable(a, b []string) [][]int {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	return table
}

// buildHunks slices the op list into hunks with `context` unchanged lines
// around every run of changes.
func buildHunks(ops []DiffOp, context int) []DiffHunk {
	var hunks []DiffHunk
	n := len(ops)
	i := 0
	for i < n {
		if ops[i].Type == "equal" {
			i++
			continue
		}
		start := i - context
		if start < 0 {
			start = 0
		}
		end := i
		for end < n {
			if ops[end].Type != "equal" {
				end++
				continue
			}
			// Extend across short equal runs so adjacent edits share a hunk.
			run := 0
			for end+run < n && ops[end+run].Type == "equal" {
				run++
			}
			if run > context*2 || end+run >= n {
				break
			}
			end += run
		}
		stop := end + context
		if stop > n {
			stop = n
		}
		h := DiffHunk{Ops: append([]DiffOp(nil), ops[start:stop]...)}
		for _, op := range h.Ops {
			if op.OldLine > 0 {
				if h.OldStart == 0 {
					h.OldStart = op.OldLine
				}
				h.OldLines++
			}
			if op.NewLine > 0 {
				if h.NewStart == 0 {
					h.NewStart = op.NewLine
				}
				h.NewLines++
			}
		}
		hunks = append(hunks, h)
		i = stop
	}
	if hunks == nil {
		hunks = []DiffHunk{}
	}
	return hunks
}
