package plan

import (
	"fmt"
	"strings"
)

// Structured acceptance criteria — the contract half of "done".
//
// Task.Acceptance is free prose, and pkg/quality has always mined it for
// runnable commands with a whitelist scan (ExtractAcceptanceCommands). That
// works, but it loses three things the harness actually needs:
//
//   - WHICH criterion a failure belongs to. The prose scan joins every command
//     it finds and stops at the first failure, so a task with three acceptance
//     conditions reports one verdict and the corrector is told "acceptance
//     failed" with no idea which condition it broke.
//   - Whether a condition is BLOCKING. "the endpoint returns 401 for a bad
//     token" and "consider adding a metric" are the same string to a regex.
//   - Whether a condition was checked AT ALL. A criterion with no runnable
//     command is invisible to the prose scan, so the harness silently treats
//     "not checked" as "fine" — the one direction this codebase never allows.
//
// A Criterion carries all three explicitly. Nothing here executes anything:
// the verify command is a STRING until pkg/quality sanitizes it against the
// same whitelist every other auto-run command passes through. A criterion can
// never widen shell scope, and a criterion the harness cannot verify is
// reported as unverified rather than quietly counted as passed.

// Criterion priorities. A criterion with an unrecognized priority normalizes to
// PriorityMust: an ambiguous requirement is treated as blocking, never waived.
const (
	// PriorityMust blocks completion. A deterministic failure fails the task.
	PriorityMust = "must"
	// PriorityShould is reported to the reviewer but never auto-blocks.
	PriorityShould = "should"
	// PriorityNice is advisory only.
	PriorityNice = "nice"
)

// MaxCriteria caps how many criteria a single task may carry.
//
// The cap is a context-budget decision, not a modeling one: every criterion
// costs prompt tokens in the worker input AND in the reviewer's evidence
// section, twice per correction round. Eight is already more conditions than
// an atomic task should have; a splitter emitting more is describing a task
// that should have been split.
const MaxCriteria = 8

// maxCriterionText bounds one criterion's prose. Long enough for a real
// condition, short enough that eight of them cannot dominate a 4k pack.
const maxCriterionText = 240

// maxVerifyCommand mirrors the command-length ceiling in
// quality.SanitizeAcceptanceCommand, so an over-long command is dropped here
// rather than traveling to the executor to be rejected there.
const maxVerifyCommand = 300

// Criterion is one testable acceptance condition for a task.
type Criterion struct {
	// ID is a stable per-task handle ("AC1"). Assigned by NormalizeCriteria
	// when the model omits it, which it frequently does.
	ID string `json:"id"`
	// Text states the condition. It is prose, and it is what a reviewer judges
	// when Verify is empty or unusable.
	Text string `json:"text"`
	// Verify is a shell command that proves the condition, or "".
	//
	// It is UNTRUSTED and unsanitized at this layer. pkg/quality decides
	// whether it may run; see quality.SafeVerifyCommand.
	Verify string `json:"verify,omitempty"`
	// Priority is must | should | nice. See NormalizePriority.
	Priority string `json:"priority,omitempty"`
}

// Blocking reports whether a deterministic failure of this criterion should
// fail the task rather than merely inform the reviewer.
func (c Criterion) Blocking() bool { return NormalizePriority(c.Priority) == PriorityMust }

// NormalizePriority maps model-authored priority text onto the three levels.
//
// Unknown values become PriorityMust deliberately. The failure directions are
// not symmetric: a "should" wrongly promoted to "must" costs one extra
// correction round, while a "must" wrongly demoted to "should" lets a real
// requirement ship unmet — which is the exact failure the gates exist to stop.
func NormalizePriority(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case PriorityShould, "optional", "recommended":
		return PriorityShould
	case PriorityNice, "nice-to-have", "nice_to_have", "advisory":
		return PriorityNice
	default:
		return PriorityMust
	}
}

// NormalizeCriteria fills in IDs, normalizes priorities, trims over-long prose
// and drops empties. It is idempotent, and safe on a nil slice.
func NormalizeCriteria(in []Criterion) []Criterion {
	if len(in) == 0 {
		return nil
	}
	out := make([]Criterion, 0, len(in))
	seenID := map[string]bool{}
	for _, c := range in {
		c.Text = clipCriterion(c.Text, maxCriterionText)
		c.Verify = clipCriterion(c.Verify, maxVerifyCommand)
		// A criterion with no prose AND no command asserts nothing.
		if c.Text == "" && c.Verify == "" {
			continue
		}
		if c.Text == "" {
			c.Text = c.Verify + " succeeds"
		}
		c.Priority = NormalizePriority(c.Priority)
		c.ID = strings.TrimSpace(c.ID)
		if c.ID == "" || seenID[c.ID] {
			c.ID = fmt.Sprintf("AC%d", len(out)+1)
		}
		seenID[c.ID] = true
		out = append(out, c)
		if len(out) >= MaxCriteria {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clipCriterion collapses whitespace and truncates to n runes.
//
// Collapsing newlines is load-bearing, not cosmetic: criterion text is
// rendered into a line-oriented evidence section, and a criterion containing
// its own newline could otherwise forge extra rows in that table.
func clipCriterion(s string, n int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n])
}

// BlockingCriteria returns only the criteria whose failure fails the task.
func BlockingCriteria(in []Criterion) []Criterion {
	var out []Criterion
	for _, c := range in {
		if c.Blocking() {
			out = append(out, c)
		}
	}
	return out
}

// CriteriaText renders criteria as the prose form every legacy consumer reads.
//
// Task.Acceptance is threaded through board markdown, worker prompts, review
// packs and the acceptance smoke scan. Rather than teach each of those about a
// new field, Normalize synthesizes Acceptance from the criteria when the model
// supplied criteria and no prose — so a structured task reads exactly like a
// prose one everywhere that never needed the structure.
func CriteriaText(in []Criterion) string {
	if len(in) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range in {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.ID)
		b.WriteString(" (")
		b.WriteString(c.Priority)
		b.WriteString("): ")
		b.WriteString(c.Text)
		if c.Verify != "" {
			b.WriteString(" — verify: ")
			b.WriteString(c.Verify)
		}
	}
	return b.String()
}

// HasVerifiable reports whether any criterion carries a candidate command.
//
// "Candidate" is the operative word: this is a cheap pre-check for callers
// deciding whether to bother with a verification pass. It says nothing about
// whether quality.SafeVerifyCommand will accept the command.
func HasVerifiable(in []Criterion) bool {
	for _, c := range in {
		if strings.TrimSpace(c.Verify) != "" {
			return true
		}
	}
	return false
}
