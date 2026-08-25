package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// Establishing the objective baseline at run start, concurrently.
//
// WHY THE HARNESS HAS TO DO THIS ITSELF. The baseline — "was the project's test
// command already passing before we touched anything?" — decides whether a green
// result at the end means anything at all. Without it, every success on a
// project whose suite already passes is unverified, and the harness cannot tell
// that from a success it earned.
//
// The first version harvested the baseline opportunistically, from a worker that
// happened to run the tests before its first edit. MEASURED: it did not fire.
// On honest-failure the model made seven tool calls, went straight to editing,
// and the harness reported success for an impossible task exactly as before.
// Waiting for the model to volunteer the one fact the verdict depends on is not
// a design, it is a hope.
//
// WHY CONCURRENTLY. Running the command inline at run start would put a whole
// test suite on the critical path of every run — seconds on a fixture, minutes
// on a real project, paid before any work begins. But a run does not start with
// execution: it starts with context, explore, plan and split, which are model
// calls. Measured on this suite those phases take 40-90 seconds even on the
// fast models. Launching the baseline alongside them costs nothing in wall
// clock and is finished long before the first worker writes.
//
// If it has NOT finished by the time it is consulted, nothing waits and nothing
// is claimed: the baseline stays unknown, and an unknown baseline never
// withholds success. Not knowing must not become an accusation.

// baselineProbeShare bounds the baseline against the run's own budget. A run
// with twenty minutes may spend one on learning whether its verification signal
// means anything; a run with two minutes may not.
const baselineProbeShare = 20

// startBaselineProbe measures the objective command once, in the background,
// before any agent has written anything.
//
// Safe to call unconditionally: it returns immediately when there is no gate,
// no command, or a weak one.
func (o *Orchestrator) startBaselineProbe(ctx context.Context) {
	if o == nil || o.cfg == nil || !o.cfg.QAGate {
		return
	}
	cmd := o.qaCommand()
	if strings.TrimSpace(cmd) == "" || quality.IsWeakQACommand(cmd) {
		// A syntax-only gate proves nothing either way, so its baseline is not
		// worth a command run.
		return
	}

	budget := o.baselineBudget(ctx)
	if budget <= 0 {
		return
	}

	go func() {
		// Detached from the caller's cancellation only in the sense that it
		// carries its own bound; a canceled run still cancels this, which is
		// what stops a doomed run paying for a measurement it will never read.
		bctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()

		started := time.Now()
		sr := o.runSmokeIn(bctx, cmd, budget)
		if !sr.Ran {
			return
		}
		// Price the command while we are here — the probe budget and the QA
		// gate's own round guard both want it, and this is the first time
		// anyone has run it this run.
		o.noteProbeCost(sr.Duration)

		green := sr.OK && !qaLooksLikeNoTests(sr.Output)

		o.mu.Lock()
		// A worker may have volunteered a pre-edit observation in the meantime.
		// First writer wins: both describe the same untouched tree, and
		// re-deciding would only add a way to disagree with ourselves.
		already := o.objective.baselineKnown
		if !already {
			o.objective.baselineKnown = true
			o.objective.baselineGreen = green
		}
		o.mu.Unlock()
		if already {
			return
		}

		if green {
			o.emitFull("init", stream.KindIntervention, "harness", "",
				"the project's test command already passes — a green result at the "+
					"end will not prove this run accomplished anything, so the outcome "+
					"will say so rather than claim success",
				quality.InterventionReview, "objective_green_at_baseline")
			return
		}
		o.emit("init", "baseline: "+cmd+" fails before any change — a green result "+
			"will be real evidence ("+time.Since(started).Round(time.Millisecond).String()+")", "")
	}()
}

// baselineBudget is how long the baseline measurement may take.
//
// Bounded by a share of the run's own wall budget, so the fact-finding can
// never become the thing that runs out of time. With no run deadline it falls
// back to the task timeout, which is the harness's general "one operation"
// bound.
func (o *Orchestrator) baselineBudget(ctx context.Context) time.Duration {
	fallback := 2 * time.Minute
	if o != nil && o.cfg != nil && o.cfg.TaskTimeout > 0 {
		fallback = o.cfg.TaskTimeout
	}
	if ctx == nil {
		return fallback
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return fallback
	}
	runway := time.Until(deadline)
	if runway <= 0 {
		return 0
	}
	share := runway / baselineProbeShare
	if share > fallback {
		return fallback
	}
	return share
}
