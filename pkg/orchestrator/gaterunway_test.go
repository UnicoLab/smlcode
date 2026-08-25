package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// TestGateStopsWhileThereIsStillTimeToReport is the regression guard for a run
// that did everything right and missed its budget by one second.
//
// MEASURED, dense Qwen3.8-27B on the honest-failure scenario: the board stopped
// itself with the full finish reserve intact — run_err was nil, so the loop's
// runway clamp worked exactly as designed — and then the QA gate spent that
// entire reserve on its own rounds. Final wall: 2401s against a 2400s budget.
//
// The gate is the LAST thing a run does. Overrunning here costs the report
// itself — the summary, the board write and the verdict all come after it — so
// a gate that runs one round too many turns a correct run into a failed one.
func TestGateStopsWhileThereIsStillTimeToReport(t *testing.T) {
	o := objectiveOrch(t, &fakeGate{}, &countingExec{}, nil)
	// A command measured at 30s: a round costs that plus a model call.
	o.noteProbeCost(30 * time.Second)

	t.Run("round 1 always runs", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		if !o.gateRoundAffordable(ctx, 1) {
			t.Fatal("refused the FIRST round; a gate that never runs is not a gate")
		}
	})

	t.Run("a later round is refused when the runway is gone", func(t *testing.T) {
		// 45s left, a 30s command: the round cannot finish and report.
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if o.gateRoundAffordable(ctx, 2) {
			t.Fatal("allowed another 30s round with 45s left — that is how a run " +
				"lands past its budget having done everything else right")
		}
	})

	t.Run("a later round is allowed with room to spare", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if !o.gateRoundAffordable(ctx, 2) {
			t.Fatal("refused a round with ten minutes left")
		}
	})

	t.Run("no deadline means no constraint", func(t *testing.T) {
		if !o.gateRoundAffordable(context.Background(), 3) {
			t.Fatal("constrained a run that has no wall budget to be constrained by")
		}
	})

	t.Run("an unpriced command is not refused", func(t *testing.T) {
		fresh := objectiveOrch(t, &fakeGate{}, &countingExec{}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if !fresh.gateRoundAffordable(ctx, 2) {
			t.Fatal("refused on an unmeasured command; nothing has priced it yet, " +
				"so there is no basis to call it unaffordable")
		}
	})
}

// notingExec stands in for a fix pass that actually writes something.
//
// It exists because the gate has TWO early exits and this test is about the
// second one. The stall guard fires when a fix pass changes nothing, and with a
// no-op exec it fires first — so the runway guard would never be reached and
// the test would pass for the wrong reason.
type notingExec struct {
	o *Orchestrator
	n int
}

func (e *notingExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.n++
	e.o.noteChangedFiles(fmt.Sprintf("fixed%d.go", e.n))
	out := make([]ggagent.SubAgentResult, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID,
			Output: `{"status":"done","summary":"patched","files_changed":["fixed.go"]}`,
		})
	}
	return out, nil
}

// TestGateSaysWhyItStoppedEarly. A gate that quietly runs fewer rounds than
// configured is indistinguishable from one that passed, and the operator needs
// to know the difference.
func TestGateSaysWhyItStoppedEarly(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL: TestStats\nFAIL\n"}
	exec := &notingExec{}
	o := objectiveOrch(t, gate, exec, nil)
	exec.o = o

	var events []string
	o.onEvent = func(e Event) { events = append(events, e.Message) }

	// Price the command high against a short runway, so round 2 is refused for
	// TIME rather than for lack of progress.
	o.noteProbeCost(30 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	_ = o.runQAGate(ctx, "implement the thing", midBoard())

	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, "not enough time left") {
		t.Fatalf("the gate stopped early without saying so:\n%s", joined)
	}
}
