package orchestrator

import (
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// Harvesting the objective command out of a worker's own tool loop.
//
// THE MEASURED WASTE. On the fix-a-bug fixture the same 9B needs 8 tool calls
// and 69k prompt tokens on a good run and 32 calls and 192k on a bad one, for
// the same three-line boundary fix — and in the bad run the harness still ends
// up with a correct tree. The difference is not that the harness cannot tell it
// is done; it is that the only place it ASKS is between waves. A worker that
// fixed the bug on its tenth call kept going for another twenty-two, and
// nothing looked until the task ended.
//
// The worker verifies its own work by running the project's test command
// through ws_shell. That output already flows past the harness. This file turns
// it into two things it was not before:
//
//  1. FREE EVIDENCE. A clean exit of the exact objective command is a paid-for
//     answer to the question probeObjective spends a whole test run on. It is
//     handed to the next probe as `reuse`, so the probe costs nothing.
//  2. A TERMINAL DIRECTIVE. The model is told, in the tool result it is already
//     reading, that the acceptance criterion is now demonstrably met and that
//     the next call should be its finish call.
//
// WHY EXACT-MATCH ONLY. A green `go test ./pkg/foo` says nothing about
// `go test ./...`, and treating it as if it did would end runs on a subset of
// the suite — the precise failure mode the weak-gate rule exists to prevent.
// Matching is therefore on the normalized command text and nothing looser: a
// command that merely looks like a test run is ignored.

// shellObjectiveNotice is what the model is told when its own verification
// proved the objective. It is deliberately terminal and specific: the loop-guard
// work in wave-k established that another paragraph of advice is something a
// small model acknowledges and ignores, so this names the single next action.
const shellObjectiveNotice = "HARNESS: this is the project's acceptance command and it just " +
	"PASSED, so the objective is demonstrably met on disk. Do not run it again and do not " +
	"make further edits. Your next call must be your finish/answer call, reporting what you " +
	"changed."

// objectiveShellRun is a clean run of the objective command observed inside a
// worker's tool loop, kept so the next probe can reuse it instead of paying for
// the same answer.
type objectiveShellRun struct {
	cmd    string
	output string
	at     time.Time
	// files fingerprints the write evidence AT THE MOMENT of the run. A later
	// probe compares it: if anything has been written since, this observation
	// describes a tree that no longer exists and must not be reused.
	files string
}

// noteShellObjectiveRun observes one completed ws_shell command.
//
// Returns the advisory to append to the tool result, or "".
func (o *Orchestrator) noteShellObjectiveRun(command string, ok bool, output string) string {
	if o == nil || o.cfg == nil || !o.cfg.QAGate {
		return ""
	}
	qa := o.qaCommand()
	if !sameShellCommand(command, qa) {
		return ""
	}
	// A weak gate proves the file parses, not that the work is done, and must
	// never end anything early — the same rule probeObjective applies.
	if quality.IsWeakQACommand(qa) {
		return ""
	}
	if !ok {
		// The objective command just FAILED. Any earlier green observation
		// described a tree that has since regressed; drop it rather than let a
		// probe reuse it.
		o.mu.Lock()
		o.objectiveShell = nil
		o.mu.Unlock()
		return ""
	}
	// "No test files" is not a pass, for the same reason it is not Green in
	// classifySmoke: a toolchain that found nothing to run verified nothing.
	if qaLooksLikeNoTests(output) {
		return ""
	}

	// Fingerprint BEFORE taking the lock: changedFilesSnapshot takes o.mu
	// itself, and a Go mutex is not reentrant, so doing this inline inside the
	// critical section below deadlocks the whole run on the first green test a
	// worker happens to run.
	fingerprint := strings.Join(o.changedFilesSnapshot(), "\n")

	o.mu.Lock()
	first := o.objectiveShell == nil
	o.objectiveShell = &objectiveShellRun{
		cmd: qa, output: output, at: time.Now(), files: fingerprint,
	}
	o.mu.Unlock()

	if first {
		o.emitFull("execute", stream.KindIntervention, "harness", "",
			"objective command passed inside the worker's own loop — the run has its answer for free",
			quality.InterventionReview, "shell_objective_green")
	}
	return shellObjectiveNotice
}

// objectiveShellEvidence returns a green observation harvested from a worker's
// tool loop, if one is still valid for the tree as it stands.
//
// It is discarded when anything has been written since it was taken. That is
// the whole safety argument for reusing it: a worker's test run happens
// mid-task, and any edit after it could have broken what it proved.
func (o *Orchestrator) objectiveShellEvidence() *quality.SmokeResult {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	obs := o.objectiveShell
	o.mu.Unlock()
	if obs == nil {
		return nil
	}
	if strings.Join(o.changedFilesSnapshot(), "\n") != obs.files {
		// The tree moved under it.
		return nil
	}
	return &quality.SmokeResult{
		Ran: true, OK: true, Command: obs.cmd, Output: obs.output,
		Summary: quality.SmokePassedMarker + ": " + obs.cmd + " (observed in the worker's tool loop)",
	}
}

// sameShellCommand reports whether two command strings are the same command.
//
// Normalization is whitespace only, on purpose. Anything cleverer — dropping
// flags, matching prefixes, treating `go test ./pkg/x` as `go test ./...` —
// would let a SUBSET of the suite end a run, and the acceptance-command bugs
// fixed in v0.18.1 are what a "close enough" comparison costs.
func sameShellCommand(a, b string) bool {
	na, nb := normalizeShellCommand(a), normalizeShellCommand(b)
	return na != "" && na == nb
}

func normalizeShellCommand(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
