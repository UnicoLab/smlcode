package quality

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Per-criterion verification — the executable half of plan.Criterion.
//
// RunAcceptanceSmokeWithPolicy mines a prose blob for whitelisted commands and
// stops at the first failure. That is the right shape for prose and the wrong
// shape for a contract: it cannot say WHICH condition failed, cannot tell a
// blocking condition from an advisory one, and — the part that matters most
// here — cannot distinguish "checked and fine" from "never checked".
//
// This file runs the same commands through the same sanitizer and the same
// executor, but keeps each criterion's verdict attached to the criterion. The
// three outcomes are deliberately three, not two:
//
//	PASSED      a real command ran in this repo and exited 0
//	FAILED      a real command ran and did not
//	UNVERIFIED  nothing ran — no command, or one the whitelist refused
//
// UNVERIFIED never counts as PASSED. That is the whole point: the prose scan's
// silent third state is exactly how "the harness did not check" becomes "the
// harness says it is fine", and this package exists to make that impossible.

// Criterion verdicts as they appear in the evidence section.
const (
	// CriterionPassed marks a criterion whose command ran and exited 0.
	CriterionPassed = SmokePassedMarker
	// CriterionFailed marks a criterion whose command ran and did not exit 0.
	CriterionFailed = SmokeFailedMarker
	// CriterionUnverified marks a criterion nothing was run for.
	CriterionUnverified = "UNVERIFIED"
)

// CriteriaBlockedMarker is the section-level verdict that fails the task.
//
// It is a distinct token rather than a count of FAILED rows because a failing
// `should` criterion also prints FAILED, and only a `must` may block. Like
// every other *FailedInOutput predicate in this package this one is a plain
// string match: forging it can only make the harness STRICTER, and matching it
// after a process restart (when the section was persisted to the board by an
// earlier run) is a requirement, not a bug.
const CriteriaBlockedMarker = "MUST-FAILED"

// CriteriaPassedMarker is the section-level verdict when nothing blocking failed.
const CriteriaPassedMarker = "CRITERIA-OK"

// maxCriterionOutput bounds how much of one failing command's stdout is quoted
// into the evidence section. Eight criteria at 1500 bytes would blow a small
// model's whole review pack on test output nobody reads past the first lines.
const maxCriterionOutput = 700

// CriterionOutcome is one criterion's verdict plus the evidence for it.
type CriterionOutcome struct {
	Criterion plan.Criterion
	// Verdict is CriterionPassed, CriterionFailed or CriterionUnverified.
	Verdict string
	// Command is the sanitized command that ran, or "" when nothing ran.
	Command string
	// Output is the command's stdout/stderr, already truncated. Untrusted.
	Output string
	// Reason explains an UNVERIFIED verdict in operator language.
	Reason string
}

// Blocking reports whether this outcome should fail the task.
//
// Only a real, reproduced failure of a `must` criterion blocks. An UNVERIFIED
// `must` does NOT block — the harness has no evidence either way, and turning
// "I could not check" into "you failed" would deadlock every task whose
// acceptance is genuinely not expressible as a whitelisted command. It is
// surfaced to the reviewer instead, which is precisely the case where a
// judgment call is the right instrument.
func (o CriterionOutcome) Blocking() bool {
	return o.Verdict == CriterionFailed && o.Criterion.Blocking()
}

// CriteriaReport is the outcome of verifying every criterion on a task.
type CriteriaReport struct {
	Outcomes []CriterionOutcome
	// Ran reports whether at least one command actually executed. A report
	// with Ran=false produces no evidence section.
	Ran bool
}

// Counts summarizes the report for logs and gate decisions.
func (r CriteriaReport) Counts() (passed, failed, unverified, blocked int) {
	for _, o := range r.Outcomes {
		switch o.Verdict {
		case CriterionPassed:
			passed++
		case CriterionFailed:
			failed++
		default:
			unverified++
		}
		if o.Blocking() {
			blocked++
		}
	}
	return passed, failed, unverified, blocked
}

// Blocked reports whether any must-criterion deterministically failed.
func (r CriteriaReport) Blocked() bool {
	_, _, _, blocked := r.Counts()
	return blocked > 0
}

// FirstBlocking returns the first blocking outcome, for the reject reason.
func (r CriteriaReport) FirstBlocking() (CriterionOutcome, bool) {
	for _, o := range r.Outcomes {
		if o.Blocking() {
			return o, true
		}
	}
	return CriterionOutcome{}, false
}

// SafeVerifyCommand returns cmd if the acceptance whitelist admits it, else "".
//
// This is the ONLY door a criterion's verify command has into the executor,
// and it is the same door prose acceptance commands use: the command must
// begin with a whitelisted tool and survive SanitizeAcceptanceCommand, which
// rejects every shell metacharacter and every token that is not a flag, a path
// or a plain identifier. A criterion therefore cannot widen shell scope by one
// character over what the prose path already allowed.
func SafeVerifyCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	lower := strings.ToLower(cmd)
	for _, prefix := range safeAcceptancePrefixes {
		p := strings.ToLower(strings.TrimSpace(prefix))
		// The command must START with a whitelisted tool. indexAtWordStart is
		// used elsewhere to find a command inside prose; here the criterion
		// field is supposed to BE the command, so a tool name buried in the
		// middle is a malformed criterion, not a command to extract.
		if !strings.HasPrefix(lower, p) {
			continue
		}
		if out := SanitizeAcceptanceCommand(cmd, prefix); out != "" {
			return out
		}
	}
	return ""
}

// VerifyCriteria runs every verifiable criterion on a task and reports each
// verdict separately.
//
// Identical commands run ONCE. A splitter that writes `go test ./...` as the
// verify for all three of its criteria is describing one check, and paying for
// it three times is wall-clock a local model does not have; the single result
// is fanned back out to every criterion that named it.
func VerifyCriteria(ctx context.Context, root string, t plan.Task,
	timeout time.Duration, policy BootstrapPolicy) CriteriaReport {
	rep := CriteriaReport{}
	if root == "" || len(t.Criteria) == 0 {
		return rep
	}
	// command → result, so a command named by several criteria runs once.
	cache := map[string]SmokeResult{}
	bootstrapped := false
	pending := ""

	for _, c := range t.Criteria {
		out := CriterionOutcome{Criterion: c, Verdict: CriterionUnverified}
		raw := strings.TrimSpace(c.Verify)
		switch raw {
		case "":
			out.Reason = "no verify command — reviewer must judge this criterion"
			rep.Outcomes = append(rep.Outcomes, out)
			continue
		default:
			safe := SafeVerifyCommand(raw)
			if safe == "" {
				// Named explicitly so an operator can see WHY, and so a model
				// re-reading its own evidence learns which commands are usable.
				out.Reason = "verify command not on the allowed list — reviewer must judge"
				rep.Outcomes = append(rep.Outcomes, out)
				continue
			}
			sr, cached := cache[safe]
			if !cached {
				if !bootstrapped {
					bootstrapped = true
					if bp := PlanBootstrap(root, safe, policy); bp.Command != "" || bp.Reason != "" {
						switch {
						case bp.Run:
							_ = RunSmoke(ctx, root, bp.Command, timeout) // approved by policy
						case bp.Reason != "":
							pending = bp.Reason
						}
					}
				}
				sr = RunSmoke(ctx, root, safe, timeout)
				cache[safe] = sr
			}
			out.Command = safe
			if !sr.Ran {
				out.Reason = "harness declined to run the command: " + sr.Summary
				rep.Outcomes = append(rep.Outcomes, out)
				continue
			}
			rep.Ran = true
			if sr.OK {
				out.Verdict = CriterionPassed
			} else {
				out.Verdict = CriterionFailed
				out.Output = truncateCriterionOutput(sr.Output)
			}
			if pending != "" {
				out.Reason = pending
			}
			rep.Outcomes = append(rep.Outcomes, out)
		}
	}
	return rep
}

// truncateCriterionOutput bounds one failing command's quoted stdout.
func truncateCriterionOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxCriterionOutput {
		return s
	}
	return strings.TrimSpace(s[:maxCriterionOutput]) + "\n… (truncated)"
}

// FormatCriteriaSection renders a report as the harness evidence section the
// reviewer reads and the gates match.
//
// It renders whenever there is anything to say — including a report where
// nothing ran at all. That is the opposite of FormatSmokeSection's contract
// and it is deliberate: an all-UNVERIFIED report is the single most important
// thing this gate can tell a reviewer, because it means every stated condition
// is still an open question. Suppressing it would recreate the silent third
// state this file exists to remove.
func FormatCriteriaSection(rep CriteriaReport) string {
	if len(rep.Outcomes) == 0 {
		return ""
	}
	passed, failed, unverified, blocked := rep.Counts()

	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(CriteriaSectionHeader)
	b.WriteString("\n")
	if blocked > 0 {
		b.WriteString(CriteriaBlockedMarker)
	} else {
		b.WriteString(CriteriaPassedMarker)
	}
	fmt.Fprintf(&b, ": %d passed, %d failed, %d unverified\n", passed, failed, unverified)

	for _, o := range rep.Outcomes {
		fmt.Fprintf(&b, "- %s [%s] %s: %s\n",
			o.Criterion.ID,
			o.Criterion.Priority,
			o.Verdict,
			DefuseHarnessMarkers(o.Criterion.Text))
		if o.Command != "" {
			b.WriteString("  cmd: ")
			b.WriteString(DefuseHarnessMarkers(o.Command))
			b.WriteString("\n")
		}
		if o.Reason != "" {
			b.WriteString("  why: ")
			b.WriteString(DefuseHarnessMarkers(o.Reason))
			b.WriteString("\n")
		}
		if o.Output != "" {
			// The command's stdout is the project's own test suite talking —
			// untrusted text joining a string the gates scan.
			b.WriteString(indentBlock(DefuseHarnessMarkers(o.Output)))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// indentBlock indents every line by two spaces so command output cannot be
// mistaken for a criterion row.
func indentBlock(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = "  " + ln
	}
	return strings.Join(lines, "\n")
}

// CriteriaBlockedInOutput reports whether a task output carries a criteria
// section in which a must-criterion deterministically failed.
func CriteriaBlockedInOutput(output string) bool {
	idx := strings.Index(output, CriteriaSectionHeader)
	if idx < 0 {
		return false
	}
	return strings.Contains(output[idx:], CriteriaBlockedMarker)
}

// CriteriaUnverifiedInOutput reports whether any criterion went unverified.
//
// This never blocks anything. It exists so the reviewer path can tell the
// difference between "the gates cleared this" and "the gates had nothing to
// say about it", which is the difference between skipping the reviewer LLM and
// needing it.
func CriteriaUnverifiedInOutput(output string) bool {
	idx := strings.Index(output, CriteriaSectionHeader)
	if idx < 0 {
		return false
	}
	sec := output[idx:]
	if end := strings.Index(sec[len(CriteriaSectionHeader):], "\n## "); end >= 0 {
		sec = sec[:len(CriteriaSectionHeader)+end]
	}
	return strings.Contains(sec, CriterionUnverified)
}
