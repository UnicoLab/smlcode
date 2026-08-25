package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// waitForBaseline polls until the background probe has recorded, or gives up.
// The probe is deliberately asynchronous, so a test that asserts on it has to
// wait for it rather than assume an ordering.
func waitForBaseline(t *testing.T, o *Orchestrator, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		known := o.objective.baselineKnown
		o.mu.Unlock()
		if known {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestBaselineIsEstablishedWithoutTheModelsHelp is the fix for a fix.
//
// The first version of this guard harvested the baseline opportunistically,
// from a worker that happened to run the tests before its first edit. MEASURED,
// Qwen3-Coder-30B on honest-failure: it did not fire. The model made seven tool
// calls, went straight to editing, and the harness reported success=true for an
// impossible task exactly as before. Waiting for the model to volunteer the one
// fact the verdict depends on is not a design.
func TestBaselineIsEstablishedWithoutTheModelsHelp(t *testing.T) {
	gate := &fakeGate{ok: true, out: "ok\tmathx\t0.2s\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	atRunStart(o)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	o.startBaselineProbe(ctx)

	if !waitForBaseline(t, o, 5*time.Second) {
		t.Fatal("no baseline was established; the model was never asked and the " +
			"harness did not look either")
	}
	o.mu.Lock()
	green := o.objective.baselineGreen
	o.mu.Unlock()
	if !green {
		t.Fatal("a passing command was not recorded as a green baseline")
	}

	// And the verdict follows: changes against an already-green suite are
	// unverified, with no help from the model at all.
	o.noteChangedFiles("mathx/add.go")
	if !o.objectiveUnverified() {
		t.Fatal("the run was still eligible for success despite a green baseline")
	}
}

// TestBaselineRedMeansGreenIsRealEvidence is the control. implement-from-tests
// and fix-a-bug both start RED, and there a green result is exactly the proof
// the harness should act on.
func TestBaselineRedMeansGreenIsRealEvidence(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL: TestMedian\nFAIL\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	atRunStart(o)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	o.startBaselineProbe(ctx)

	if !waitForBaseline(t, o, 5*time.Second) {
		t.Fatal("no baseline established for a failing command")
	}
	o.noteChangedFiles("stats/median.go")
	if o.objectiveUnverified() {
		t.Fatal("a red baseline was treated as unverified; red-then-green is the " +
			"case where the command genuinely proves something")
	}
}

// TestBaselineProbePricesTheCommand: the run needs the command's cost for the
// probe budget and the QA gate's round guard, and the baseline is the first
// time anyone runs it. Throwing that measurement away would mean paying for it
// twice.
func TestBaselineProbePricesTheCommand(t *testing.T) {
	o := objectiveOrch(t, &fakeGate{ok: true, out: "ok\n"}, &countingExec{}, nil)
	atRunStart(o)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	o.startBaselineProbe(ctx)
	if !waitForBaseline(t, o, 5*time.Second) {
		t.Fatal("baseline never completed")
	}
	o.mu.Lock()
	cost := o.objective.cost
	o.mu.Unlock()
	if cost <= 0 {
		t.Fatal("the baseline ran the command and recorded no cost for it")
	}
}

// TestBaselineRefusesWhatItCannotLearnFrom. Each of these would spend a command
// run to learn nothing.
func TestBaselineRefusesWhatItCannotLearnFrom(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	t.Run("a weak gate", func(t *testing.T) {
		gate := &fakeGate{ok: true, out: "ok\n"}
		o := objectiveOrch(t, gate, &countingExec{}, func(c *config.Config) {
			c.QAGateCommand = "python -m compileall ."
		})
		atRunStart(o)
		o.startBaselineProbe(ctx)
		if waitForBaseline(t, o, 500*time.Millisecond) {
			t.Fatal("spent a run on a syntax-only gate, which proves nothing either way")
		}
	})

	t.Run("qa_gate disabled", func(t *testing.T) {
		gate := &fakeGate{ok: true, out: "ok\n"}
		o := objectiveOrch(t, gate, &countingExec{}, func(c *config.Config) {
			c.QAGate = false
		})
		atRunStart(o)
		o.startBaselineProbe(ctx)
		if waitForBaseline(t, o, 500*time.Millisecond) {
			t.Fatal("measured a baseline for a run with no gate to measure against")
		}
	})
}

// TestBaselineBudgetIsBoundedByTheRun: fact-finding must never become the thing
// that runs out of time.
func TestBaselineBudgetIsBoundedByTheRun(t *testing.T) {
	o := objectiveOrch(t, &fakeGate{}, &countingExec{}, func(c *config.Config) {
		c.TaskTimeout = 8 * time.Minute
	})

	t.Run("a share of a long run", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		got := o.baselineBudget(ctx)
		if got <= 0 || got > time.Minute+time.Second {
			t.Fatalf("budget %s for a 20m run; want about a twentieth", got)
		}
	})

	t.Run("never more than one operation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Hour)
		defer cancel()
		if got := o.baselineBudget(ctx); got > 8*time.Minute {
			t.Fatalf("budget %s exceeds the task timeout", got)
		}
	})

	t.Run("past the deadline, nothing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		time.Sleep(2 * time.Millisecond)
		if got := o.baselineBudget(ctx); got != 0 {
			t.Fatalf("budget %s for a run that is already over", got)
		}
	})
}
