package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// Regression guards for the second live defect: the repeated-tool-call
// intervention did not break the loop.
//
// Observed on a 9B: two identical tool calls, two identical "QUALITY MONITOR:
// You just made the exact same tool call…" nudges, the model paraphrasing the
// refusal back in its own words and immediately re-issuing the call. The guard
// refused the duplicate — but that was the whole of the intervention. Every
// escalation rung was another paragraph of advice aimed at a model that had
// just demonstrated it does not act on advice, and pkg/evolve's
// force_different_action repair resolves to guidance text too
// (loop.applyAdviceAction), so routing it there would have produced a third
// paragraph.

// counted wraps an executor so a test can tell "refused with a message" from
// "actually ran".
type counted struct {
	runs map[string]int
	fns  map[string]tools.ToolExecutor
}

func newCounted(tr *CallTracker, names ...string) *counted {
	c := &counted{runs: map[string]int{}, fns: map[string]tools.ToolExecutor{}}
	for _, n := range names {
		name := n
		c.fns[name] = tr.Wrap(name, func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			c.runs[name]++
			return "REAL RESULT", nil
		})
	}
	return c
}

// call returns (result, ran).
func (c *counted) call(ctx context.Context, name string, args map[string]interface{}) (string, bool) {
	before := c.runs[name]
	out, _ := c.fns[name](ctx, args)
	s, _ := out.(string)
	return s, c.runs[name] > before
}

// TestRepeatedCallEscalatesFromTextToTheToolNotRunning is the load-bearing one.
//
// After the nudge has been given once and ignored, the intervention must stop
// being a message: the tool is withdrawn, and — the part a nudge can never do —
// it stays withdrawn for DIFFERENT arguments too. Varying the path is the exact
// escape a stuck model reaches for next, and until this existed it worked: the
// guard only ever matched the verbatim call, so ws_read("a.go") →
// ws_read("a.go") → ws_read("b.go") sailed straight through the intervention
// and the loop resumed one file over.
func TestRepeatedCallEscalatesFromTextToTheToolNotRunning(t *testing.T) {
	tr := NewCallTracker()
	c := newCounted(tr, "ws_read")
	ctx := WithTaskID(context.Background(), "T1")
	same := map[string]interface{}{"path": "a.go"}

	if _, ran := c.call(ctx, "ws_read", same); !ran {
		t.Fatal("the first call must run")
	}
	// Repeat 1: one warning, as before.
	msg, ran := c.call(ctx, "ws_read", same)
	if ran {
		t.Fatal("a verbatim repeat must not execute")
	}
	if !strings.Contains(msg, "QUALITY MONITOR") {
		t.Fatalf("first repeat should be nudged, got %q", msg)
	}

	// Repeat 2: the nudge was ignored. Something other than more text must
	// happen here.
	msg, _ = c.call(ctx, "ws_read", same)
	if !strings.Contains(msg, "DISABLED") {
		t.Errorf("second repeat produced another nudge and nothing else:\n%q\n"+
			"a model that ignored the first nudge is not fixed by a second one", msg)
	}

	// The structural half: the tool no longer answers for ANY arguments.
	other := map[string]interface{}{"path": "b.go"}
	msg, ran = c.call(ctx, "ws_read", other)
	if ran {
		t.Errorf("ws_read still ran for different arguments after being withdrawn: %q\n"+
			"the intervention only removed one call, not the stuck strategy", msg)
	}
	if !strings.Contains(msg, "DISABLED") {
		t.Errorf("a withdrawn tool must say so, got %q", msg)
	}
}

// TestAlternatingRepeatsCannotDodgeTheEscalation pins the counter defect.
//
// The old ladder escalated on `consecutive` refusals and reset that counter to
// zero after ANY call that executed. A model alternating between two calls it
// is stuck on therefore reset the ladder every other turn and never escalated
// at all — the guard switched itself off exactly when a model was looping,
// which is the same failure mode assess() was already fixed for once.
func TestAlternatingRepeatsCannotDodgeTheEscalation(t *testing.T) {
	tr := NewCallTracker()
	c := newCounted(tr, "ws_read", "ws_grep")
	ctx := WithTaskID(context.Background(), "T1")
	readArgs := map[string]interface{}{"path": "a.go"}

	// Prime both calls, then loop: repeat the read, do a fresh grep that
	// succeeds (resetting the old consecutive counter), repeat the read again.
	_, _ = c.call(ctx, "ws_read", readArgs)
	for i, pattern := range []string{"alpha", "beta", "gamma"} {
		msg, ran := c.call(ctx, "ws_read", readArgs)
		if ran {
			t.Fatalf("round %d: the verbatim repeat must never execute", i)
		}
		if i > 0 && !strings.Contains(msg, "DISABLED") {
			t.Fatalf("round %d: still only nudging after %d ignored warnings: %q\n"+
				"an unrelated successful call must not rewind the escalation ladder", i, i, msg)
		}
		if _, ran := c.call(ctx, "ws_grep", map[string]interface{}{"pattern": pattern}); !ran {
			t.Fatalf("round %d: a genuinely new call must still run", i)
		}
	}
}

// TestWithdrawalIsLiftedByRealProgress is the containment check.
//
// A withdrawal that outlived the loop it broke would starve the corrector,
// which shares the task's tool history: it would open with a ws_read and be
// refused for something the WORKER did. Real progress — a state-changing call
// that actually executed — hands every tool back, which is the same rule
// assess() already uses to decide when a repeat becomes legitimate again.
func TestWithdrawalIsLiftedByRealProgress(t *testing.T) {
	tr := NewCallTracker()
	c := newCounted(tr, "ws_read", "ws_edit")
	ctx := WithTaskID(context.Background(), "T1")
	same := map[string]interface{}{"path": "a.go"}

	_, _ = c.call(ctx, "ws_read", same)
	_, _ = c.call(ctx, "ws_read", same) // nudge
	_, _ = c.call(ctx, "ws_read", same) // withdrawn
	if _, ran := c.call(ctx, "ws_read", same); ran {
		t.Fatal("fixture is wrong: ws_read should be withdrawn here")
	}

	if _, ran := c.call(ctx, "ws_edit",
		map[string]interface{}{"path": "a.go", "old_str": "x", "new_str": "y"}); !ran {
		t.Fatal("an edit must always be reachable — withdrawing it would make the task impossible")
	}
	if _, ran := c.call(ctx, "ws_read", same); !ran {
		t.Error("a real state change must hand the withdrawn tools back")
	}
}

// TestEditingToolsAreNeverWithdrawn: a task whose editor is taken away cannot
// be completed by any strategy, so that rung gets the terminal finish directive
// instead — but still on the second repeat, not the third.
func TestEditingToolsAreNeverWithdrawn(t *testing.T) {
	tr := NewCallTracker()
	c := newCounted(tr, "ws_edit")
	ctx := WithTaskID(context.Background(), "T1")
	stuck := map[string]interface{}{"path": "a.go", "old_str": "nope", "new_str": "y"}

	_, _ = c.call(ctx, "ws_edit", stuck)
	_, _ = c.call(ctx, "ws_edit", stuck) // nudge
	msg, _ := c.call(ctx, "ws_edit", stuck)
	if !strings.Contains(msg, "HARD STOP") {
		t.Errorf("second ignored repeat of an edit should be terminal, got %q", msg)
	}
	if strings.Contains(msg, "DISABLED") {
		t.Error("an editing tool must never be withdrawn")
	}
	// A genuinely different edit is a different attempt and must still run.
	if _, ran := c.call(ctx, "ws_edit",
		map[string]interface{}{"path": "a.go", "old_str": "real", "new_str": "y"}); !ran {
		t.Error("varying old_str is real progress, not a repeat — it must still execute")
	}
}

// TestResetTaskClearsWithdrawals: a new task starts with every tool available.
func TestResetTaskClearsWithdrawals(t *testing.T) {
	tr := NewCallTracker()
	c := newCounted(tr, "ws_read")
	ctx := WithTaskID(context.Background(), "T1")
	same := map[string]interface{}{"path": "a.go"}

	_, _ = c.call(ctx, "ws_read", same)
	_, _ = c.call(ctx, "ws_read", same)
	_, _ = c.call(ctx, "ws_read", same)
	tr.ResetTask("T1")
	if _, ran := c.call(ctx, "ws_read", same); !ran {
		t.Error("ResetTask must clear the withdrawal along with the history")
	}
}
