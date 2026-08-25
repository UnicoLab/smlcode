package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// TestCanceledQAGateReportsNoVerdict is the regression guard for a run that
// spends model calls on its way out and leaves a false annotation behind.
//
// THE DEFECT: runQAGate returns a bool meaning "the gate failed", and every
// cancellation path returned TRUE. finalizeAfterExecute reads that as
// QAFailed, sets TesterRejected, and feeds the board a SYNTHESIZED tester
// verdict — `{"passed":false,...,"qa_gate red"}` — through applyTesterFeedback,
// which is a planner call. So pressing Ctrl-C, or a scenario budget expiring,
// bought an extra LLM round-trip during shutdown and annotated a done task with
// "QA gate still failing: <cmd>" about a gate that was never allowed to run.
func TestCanceledQAGateReportsNoVerdict(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL: TestStats\nFAIL\n"}
	o := objectiveOrch(t, gate, &countingExec{}, nil)

	var events []string
	o.onEvent = func(e Event) { events = append(events, e.Message) }

	board := midBoard()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the run is over before the gate starts

	failed := o.runQAGate(ctx, "implement the thing", board)

	if failed {
		t.Fatal("a canceled gate reported QAFailed=true, which fabricates a " +
			"tester rejection and spends a planner call during shutdown")
	}
	// The command must not have run at all — there was no run left to spend.
	if n := gate.count(objectiveCmd); n != 0 {
		t.Fatalf("a canceled gate still executed the objective command %d time(s)", n)
	}
	// And it must not leave a verdict behind, in the board or in the events.
	for _, tk := range board.Tasks {
		if strings.Contains(tk.Notes, "QA gate still failing") {
			t.Fatalf("a canceled gate annotated %s with a failure it never established", tk.ID)
		}
	}
	joined := strings.Join(events, "\n")
	if strings.Contains(joined, "still red after") {
		t.Fatalf("a canceled gate announced a red verdict:\n%s", joined)
	}
	if !strings.Contains(joined, "no verdict") {
		t.Fatalf("a canceled gate said nothing about why it stopped:\n%s", joined)
	}
}

// TestStalledFixPassStopsInsteadOfReRunningAnUnchangedTree pins the progress
// rule: if the fix pass wrote nothing, the next round would run the same
// command against the same tree and cannot get a different answer.
//
// On a slow suite each wasted round is minutes; the objective probe has refused
// this since it was written, and the gate did not.
func TestStalledFixPassStopsInsteadOfReRunningAnUnchangedTree(t *testing.T) {
	gate := &fakeGate{ok: false, out: "--- FAIL: TestStats\nFAIL\n"}
	// A worker that never writes: qaDiagnoseAndFix will produce no file change.
	o := objectiveOrch(t, gate, &countingExec{}, nil)

	var events []string
	o.onEvent = func(e Event) { events = append(events, e.Message) }

	rounds := o.qaGateRounds()
	if rounds < 2 {
		t.Skipf("qa_gate_rounds is %d — this test needs room for a second round", rounds)
	}

	failed := o.runQAGate(context.Background(), "implement the thing", midBoard())

	if !failed {
		t.Fatal("a red gate that stalled must still report red — stopping early " +
			"is about not paying twice for the same answer, not about passing")
	}
	// Round 1 runs the command once. Every later run would be against a tree
	// nothing wrote to, including the post-loop final verification.
	if n := gate.count(objectiveCmd); n != 1 {
		t.Fatalf("the objective command ran %d time(s) across %d round(s) with a "+
			"fix pass that wrote nothing, want exactly 1", n, rounds)
	}
	if joined := strings.Join(events, "\n"); !strings.Contains(joined, "wrote nothing") {
		t.Fatalf("the gate stalled silently — an operator cannot tell this from "+
			"a gate that ran its rounds:\n%s", joined)
	}
}
