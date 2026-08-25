package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// TestWorkerVerificationIsHarvested is the measured motivation: on fix-a-bug the
// same 9B needs 8 tool calls on a good run and 32 on a bad one for the same
// three-line fix, and the harness only ever ASKED whether the objective was met
// between waves. A worker that fixed the bug on call ten kept going for another
// twenty-two while its own `go test` sat there, green, in the transcript.
func TestWorkerVerificationIsHarvested(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL: TestStats\nFAIL\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	board := midBoard()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// The worker runs the project's acceptance command itself, and it passes.
	advice := o.noteShellObjectiveRun(objectiveCmd, true, "ok\tstats\t0.4s\n")
	if !strings.Contains(advice, "PASSED") || !strings.Contains(advice, "finish") {
		t.Fatalf("the model was told nothing actionable: %q", advice)
	}

	// The next probe must now answer from that observation and pay NOTHING.
	// The fake gate is scripted RED, so if the probe runs the command itself
	// the run does not stop — which is exactly what used to happen.
	stop, reason := o.objectiveMetBetweenWaves(ctx, board)
	if !stop {
		t.Fatal("the run did not stop although its own worker had already proved the objective")
	}
	if !strings.Contains(reason, "objective already met") {
		t.Fatalf("stop reason=%q", reason)
	}
	if n := gate.count(objectiveCmd); n != 0 {
		t.Fatalf("the probe spent %d command run(s) re-proving what the worker "+
			"had already proved for free", n)
	}
}

// TestHarvestedEvidenceIsDroppedWhenTheTreeMoves is the safety argument for
// reusing a mid-task observation at all. The worker's test run happens WHILE the
// task is still going, so any edit after it could have broken what it proved.
func TestHarvestedEvidenceIsDroppedWhenTheTreeMoves(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL\nFAIL\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)

	o.noteShellObjectiveRun(objectiveCmd, true, "ok\n")
	if o.objectiveShellEvidence() == nil {
		t.Fatal("a fresh observation was not usable")
	}
	// The worker keeps editing after its green test run.
	o.noteChangedFiles("stats.go", "extra.go")
	if ev := o.objectiveShellEvidence(); ev != nil {
		t.Fatalf("an observation taken before a later write was still offered as "+
			"evidence: %+v", ev)
	}
}

// TestHarvestRejectsWhatItCannotTrust: each of these would end a run on
// something that does not prove the objective.
func TestHarvestRejectsWhatItCannotTrust(t *testing.T) {
	t.Run("a failing run retracts an earlier green", func(t *testing.T) {
		o := objectiveOrch(t, &fakeGate{}, &countingExec{}, nil)
		o.noteShellObjectiveRun(objectiveCmd, true, "ok\n")
		if o.objectiveShellEvidence() == nil {
			t.Fatal("precondition: green not recorded")
		}
		o.noteShellObjectiveRun(objectiveCmd, false, "--- FAIL: TestStats\nFAIL\n")
		if o.objectiveShellEvidence() != nil {
			t.Fatal("a regression left the earlier green answer in place")
		}
	})

	t.Run("a different command proves nothing", func(t *testing.T) {
		o := objectiveOrch(t, &fakeGate{}, &countingExec{}, nil)
		// A SUBSET of the suite. Accepting this would end runs on a partial
		// verification — the failure the weak-gate rule exists to prevent.
		if adv := o.noteShellObjectiveRun("go test ./chunk", true, "ok\n"); adv != "" {
			t.Fatalf("a subset of the suite was accepted as the objective: %q", adv)
		}
		if o.objectiveShellEvidence() != nil {
			t.Fatal("a subset run was recorded as objective evidence")
		}
	})

	t.Run("no test files is not a pass", func(t *testing.T) {
		o := objectiveOrch(t, &fakeGate{}, &countingExec{}, nil)
		if adv := o.noteShellObjectiveRun(objectiveCmd, true,
			"?   \tfixture/chunk\t[no test files]\n"); adv != "" {
			t.Fatalf("a toolchain that ran nothing was treated as proof: %q", adv)
		}
		if o.objectiveShellEvidence() != nil {
			t.Fatal("a no-test-files run was recorded as objective evidence")
		}
	})

	t.Run("a weak gate never ends a run", func(t *testing.T) {
		o := objectiveOrch(t, &fakeGate{}, &countingExec{}, func(c *config.Config) {
			c.QAGateCommand = "python -m compileall ."
		})
		if adv := o.noteShellObjectiveRun("python -m compileall .", true, "ok\n"); adv != "" {
			t.Fatalf("a syntax-only gate was treated as the objective: %q", adv)
		}
	})

	t.Run("whitespace differences are still the same command", func(t *testing.T) {
		o := objectiveOrch(t, &fakeGate{}, &countingExec{}, nil)
		spaced := "  " + strings.Join(strings.Fields(objectiveCmd), "   ") + " "
		if adv := o.noteShellObjectiveRun(spaced, true, "ok\n"); adv == "" {
			t.Fatalf("re-spacing the same command defeated the match: %q", spaced)
		}
	})
}

// TestHarvestIsClearedBetweenRuns: both a fresh run and a stale observation
// start with an empty changed-file set, so the fingerprint guard alone would
// consider them the same tree.
func TestHarvestIsClearedBetweenRuns(t *testing.T) {
	o := objectiveOrch(t, &fakeGate{}, &countingExec{}, nil)
	o.noteShellObjectiveRun(objectiveCmd, true, "ok\n")
	if o.objectiveShellEvidence() == nil {
		t.Fatal("precondition: nothing recorded")
	}
	o.resetObjectiveProbes()
	if o.objectiveShellEvidence() != nil {
		t.Fatal("a previous run's green answer survived into the next run")
	}
}
