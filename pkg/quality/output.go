package quality

import (
	"fmt"
	"regexp"
	"strings"
)

// Command output is truncated HEAD+TAIL, never head-only.
//
// Every runner this harness drives puts its verdict at the END: pytest prints
// `=== FAILURES ===` and its short summary last, `go test ./...` prints the
// FAIL lines last, tsc and cargo emit their error summary last. Head-only
// truncation therefore handed the corrector several thousand characters of
// collection noise with the actual assertion cut off, then asked it to make the
// command pass — so it guessed, edited blindly, and looped.
//
// FailureExcerpt goes one step further and PINS the lines that look like the
// failure to the top, so the error survives even a later head-only cut made by
// a caller this package does not control.
const (
	// MaxSmokeOutput bounds one command's captured output.
	MaxSmokeOutput = 20_000
	// MaxSectionOutput bounds output embedded into a prompt section.
	MaxSectionOutput = 2_000
	// MaxPinnedFailureLines caps the pinned block so a 10k-line failure cannot
	// crowd out the surrounding context.
	MaxPinnedFailureLines = 24
)

// failureLine matches a line that carries the actual error. Matching is done
// against the line with leading whitespace trimmed, so pytest's indented
// "E       assert 1 == 2" and a bare "assert" both hit.
var failureLine = regexp.MustCompile(`(?i)^(FAIL|ERROR|E\s|.*: error:|assert|Traceback|panic:)`)

// FailureLines extracts, in order and deduplicated, the lines of out that look
// like the failure itself. Returns nil when nothing matches.
func FailureLines(out string, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = MaxPinnedFailureLines
	}
	var pinned []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(strings.TrimLeft(raw, " \t"), " \t\r")
		if line == "" || seen[line] {
			continue
		}
		if !failureLine.MatchString(line) {
			continue
		}
		seen[line] = true
		pinned = append(pinned, clipLine(line, 400))
		if len(pinned) >= maxLines {
			break
		}
	}
	return pinned
}

// TruncateOutput bounds s to max characters keeping BOTH ends — the same
// head+tail strategy pkg/workspace uses for tool output, for the same reason.
func TruncateOutput(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head - 40
	if tail < 80 {
		tail = 80
		head = max - tail - 40
	}
	if head < 100 {
		return s[:max] + "\n...[truncated]"
	}
	return s[:head] +
		fmt.Sprintf("\n...[%d chars truncated]...\n", len(s)-head-tail) +
		s[len(s)-tail:]
}

// FailureExcerpt bounds command output to max characters while guaranteeing
// the failure lines are in it — pinned to the TOP, ahead of a head+tail
// truncation of the full output.
//
// Output that already fits is returned untouched: pinning is only worth its
// duplication when something is about to be thrown away.
func FailureExcerpt(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	pinned := FailureLines(s, MaxPinnedFailureLines)
	if len(pinned) == 0 {
		return TruncateOutput(s, max)
	}
	block := "[key failure lines]\n" + strings.Join(pinned, "\n") + "\n[full output, truncated]\n"
	budget := max / 3
	if len(block) > budget {
		block = TruncateOutput(block, budget)
		if !strings.HasSuffix(block, "\n") {
			block += "\n"
		}
	}
	rest := max - len(block)
	if rest < 200 {
		return block
	}
	return block + TruncateOutput(s, rest)
}

func clipLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
