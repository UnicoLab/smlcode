package loop

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// ------------------------------------------------------------------------
// The loop side of the ws_shell scope guard.
//
// pkg/workspace detects that an opaque shell command wrote outside the task's
// focus (shellscope.go) and parks the finding on the Workspace. Draining that
// ledger and turning it into (a) reviewer evidence and (b) a scope verdict is
// this file's job.
//
// Two properties are load-bearing and both are pinned by tests:
//
//  1. ATTRIBUTION. The ledger is per-WORKSPACE, the gates are per-TASK, and the
//     shipped default runs four workers in parallel against one Workspace. Every
//     drain therefore files each event under its own ShellScopeEvent.TaskID
//     instead of crediting it to whoever happened to drain first.
//  2. NON-EVIDENCE. An out-of-scope write is evidence AGAINST the task. The
//     rendered bullets must never satisfy hasDiskEvidenceSection, or a worker
//     that did nothing but scribble on a file it does not own would be handed
//     the write evidence that unlocks fast-path approval.
// ------------------------------------------------------------------------

// Budget for the "## Disk evidence" contribution. The neighboring sections cap
// themselves the same way (diskEvidenceHint stops at 8 in-scope git lines and
// 16 lines overall) because all of this lands in a small model's context, where
// a long tail of paths crowds out the task itself.
const (
	maxShellScopeEvidenceLines = 6
	maxShellScopeEvidenceBytes = 800
)

// evidentialMarkerRe matches the write-evidence markers hasDiskEvidenceSection
// looks for. It is built FROM evidentialDiskMarkers so the two can never drift.
var evidentialMarkerRe = regexp.MustCompile(`(?i)(?:` +
	strings.Join(quotedEvidentialMarkers(), "|") + `)`)

func quotedEvidentialMarkers() []string {
	out := make([]string, 0, len(evidentialDiskMarkers))
	for _, m := range evidentialDiskMarkers {
		out = append(out, regexp.QuoteMeta(m))
	}
	return out
}

// taskScopeLog is one task's accumulated out-of-scope shell writes.
//
// rendered counts how many of them have already been put into a "## Disk
// evidence" section. runGates runs several times per task (worker turn,
// self-critique, review-time insurance) and each pass reports only what is new,
// so a single stray write is not re-billed to the model's context four times.
type taskScopeLog struct {
	events   []workspace.ShellScopeEvent
	rendered int
}

// harvestShellScope moves everything currently on the workspace ledger into the
// per-task logs. fallbackTaskID owns any event that carries no task id of its
// own — a ws_shell call made under an untagged context, which in a live run
// only happens outside a wave dispatch.
//
// It is safe (and cheap) to call from any gate: draining is what guarantees the
// next task's drain cannot inherit this task's events.
func (r *Runner) harvestShellScope(fallbackTaskID string) {
	if r == nil || r.TakeShellScope == nil {
		return
	}
	events := r.TakeShellScope()
	if len(events) == 0 {
		return
	}
	r.scopeMu.Lock()
	defer r.scopeMu.Unlock()
	if r.scopeLog == nil {
		r.scopeLog = map[string]*taskScopeLog{}
	}
	for _, e := range events {
		id := strings.TrimSpace(e.TaskID)
		if id == "" {
			id = fallbackTaskID
		}
		log := r.scopeLog[id]
		if log == nil {
			log = &taskScopeLog{}
			r.scopeLog[id] = log
		}
		log.events = append(log.events, e)
	}
}

// shellScopeEvidence renders the not-yet-reported out-of-scope shell writes for
// one task as "## Disk evidence" bullets, and marks them reported. Returns ""
// when there is nothing new.
func (r *Runner) shellScopeEvidence(taskID string) string {
	if r == nil {
		return ""
	}
	r.harvestShellScope(taskID)
	r.scopeMu.Lock()
	log := r.scopeLog[taskID]
	if log == nil || log.rendered >= len(log.events) {
		r.scopeMu.Unlock()
		return ""
	}
	fresh := append([]workspace.ShellScopeEvent(nil), log.events[log.rendered:]...)
	log.rendered = len(log.events)
	r.scopeMu.Unlock()
	return renderShellScopeEvidence(fresh)
}

// renderShellScopeEvidence formats events within the section's byte budget.
//
// workspace.ShellScopeEvidenceLines owns the wording and the sort order; this
// only bounds the result and strips write-evidence markers out of it.
func renderShellScopeEvidence(events []workspace.ShellScopeEvent) string {
	lines := workspace.ShellScopeEvidenceLines(events)
	if len(lines) == 0 {
		return ""
	}
	var kept []string
	used := 0
	for _, line := range lines {
		line = scrubEvidentialMarkers(line)
		if len(kept) >= maxShellScopeEvidenceLines || used+len(line)+1 > maxShellScopeEvidenceBytes {
			break
		}
		kept = append(kept, line)
		used += len(line) + 1
	}
	if len(kept) == 0 {
		// Even one line did not fit the budget: say the fact without the paths
		// rather than emitting a truncated path a reviewer would misread.
		return fmt.Sprintf("- %d out-of-scope shell write(s) recorded (too long to list)", len(lines))
	}
	if rest := len(lines) - len(kept); rest > 0 {
		kept = append(kept, fmt.Sprintf("  …and %d more out-of-scope shell write(s)", rest))
	}
	return strings.Join(kept, "\n")
}

// scrubEvidentialMarkers guarantees property 2 above.
//
// workspace picked bullet prefixes that avoid evidentialDiskMarkers on purpose,
// but the bullet also quotes the COMMAND, and the command is model-controlled
// text: `echo "modified: main.go"` would otherwise hand a task the very marker
// that proves it wrote something. Dropping the marker's colon defuses the match
// while leaving the line readable — the same trade DefuseHarnessMarkers makes.
func scrubEvidentialMarkers(line string) string {
	return evidentialMarkerRe.ReplaceAllStringFunc(line, func(m string) string {
		return strings.TrimSuffix(m, ":")
	})
}

// shellScopeViolations returns every PROTECTED-path shell write recorded for a
// task. These are the ones scopeOK treats as a scope failure: a build writes
// caches and generated code outside the focus set all the time, but nothing
// legitimate writes a path the task was explicitly told not to touch, or the
// harness's own control state.
func (r *Runner) shellScopeViolations(taskID string) []workspace.ShellScopeEvent {
	if r == nil {
		return nil
	}
	r.harvestShellScope(taskID)
	r.scopeMu.Lock()
	defer r.scopeMu.Unlock()
	log := r.scopeLog[taskID]
	if log == nil {
		return nil
	}
	var out []workspace.ShellScopeEvent
	for _, e := range log.events {
		if e.Protected {
			out = append(out, e)
		}
	}
	return out
}

// shellScopeReason is scopeOK's rejection text for a protected-path shell
// write. Like every other refusal in the harness it states the decisive fact
// and ends with one concrete next action, because a bare complaint makes a
// small model re-run the command harder.
func shellScopeReason(events []workspace.ShellScopeEvent) string {
	if len(events) == 0 {
		return ""
	}
	paths := make([]string, 0, len(events))
	seen := map[string]bool{}
	for _, e := range events {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		paths = append(paths, e.Path)
		if len(paths) >= 5 {
			break
		}
	}
	return "protected-path shell write: " + strings.Join(paths, ", ") +
		" — a ws_shell command changed a path this task is forbidden to touch. " +
		"Restore it with ws_edit and report what the command did; do not re-run the command."
}
