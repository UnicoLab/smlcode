package loop

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// stallingExec is the failure that has cost every fix-a-bug ceiling miss,
// reproduced without a model: an agent that answers nothing and holds its slot
// until the budget it was handed runs out.
//
// It records the Timeout of every request, which is the whole point — the
// defect is not that the model stalls (nothing in the harness can prevent
// that), it is that the harness kept handing out budgets LONGER THAN THE RUN
// HAD LEFT, guaranteeing the wall arrived mid-call and took the finish path
// with it.
type stallingExec struct {
	mu      sync.Mutex
	budgets []time.Duration
}

func (e *stallingExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {

	e.mu.Lock()
	for _, r := range reqs {
		e.budgets = append(e.budgets, r.Timeout)
	}
	e.mu.Unlock()

	// Stall for exactly the budget we were given, or until the run is cut off.
	var longest time.Duration
	for _, r := range reqs {
		if r.Timeout > longest {
			longest = r.Timeout
		}
	}
	t := time.NewTimer(longest)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i := range reqs {
		out[i] = ggagent.SubAgentResult{Error: context.DeadlineExceeded}
	}
	return out, nil
}

func (e *stallingExec) handed() []time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]time.Duration(nil), e.budgets...)
}

// TestAStalledWorkerNeverOutlivesTheRun is the deterministic stand-in for four
// twenty-minute live runs.
//
// It reproduces the measured failure exactly — a worker that produces nothing
// and holds its slot — at a scale where it takes seconds, so the property can
// be asserted a hundred times instead of sampled four times. The live SLM runs
// confirm the same thing end to end; this is what makes it PROVEN rather than
// observed.
//
// The property: no agent call may be handed a budget longer than the run has
// left, because such a call cannot finish, and the deadline arriving mid-call
// is what destroys the finish path — the QA gate, the board write, the summary
// — in runs that had already put a correct tree on disk.
func TestAStalledWorkerNeverOutlivesTheRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.py"), []byte("print('hi')\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := &stallingExec{}
	r := NewRunner(fake, ggagent.NewSharedState())
	r.Root = root
	r.MaxParallel = 1
	// The shape that fails: a per-call budget far larger than the whole run.
	r.Timeout = 8 * time.Minute
	r.MaxRetries = 0
	r.PostWorkerSmoke = false
	r.StaticQuality = false
	r.ClaimsGate = false
	r.RequireSmoke = false
	r.WorkerCritique = false
	r.MaxWaves = 2

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Stall", Role: plan.RoleWorker,
		Acceptance: "file exists", Files: []string{"hello.py"},
		Column: plan.ColReadyToDev, Description: "noop",
	}}}

	// TWELVE SECONDS, NOT SIX, AND THE REASON IS THE TEST'S OWN HONESTY.
	//
	// The property is "the finish reserve survives a stalled worker". The
	// reserve is a fifth of the runway, so the test only holds if the loop's
	// post-call work — persisting the board, filing the refused review,
	// escalating the task — fits inside it. Measured under `-count=20` that
	// work is usually ~0.2s but tails out past 1.9s when the machine is busy,
	// so a 6s runway (1.2s reserve) failed about one run in ten on load alone.
	//
	// Raising the runway makes the reserve comfortably larger than that tail
	// instead of loosening the assertion. A flaky timing test is worse than no
	// test: it trains everyone to re-run the gate until it goes green.
	const runway = 12 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), runway)
	defer cancel()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		_ = r.RunBoard(ctx, board)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(runway + 6*time.Second):
		t.Fatal("RunBoard did not return within the runway plus slack — the run " +
			"was still inside an agent call when its wall expired")
	}
	elapsed := time.Since(start)

	handed := fake.handed()
	if len(handed) == 0 {
		t.Fatal("no agent call was dispatched; the fixture proves nothing")
	}
	// THE ASSERTION. Every budget handed out must be shorter than the runway
	// that remained, or the call is one the run cannot afford to wait for.
	for i, b := range handed {
		if b >= runway {
			t.Fatalf("dispatch %d was handed %s with only %s of run left — an "+
				"unclamped budget, which is exactly how the wall lands mid-call",
				i, b, runway)
		}
		if b <= 0 {
			t.Fatalf("dispatch %d was handed a non-positive budget %s", i, b)
		}
	}
	// And the run must have left time for its finish path rather than being
	// killed on the deadline.
	if elapsed >= runway {
		t.Fatalf("RunBoard used the entire %s runway (%s) and left nothing for the "+
			"QA gate, the board write and the summary", runway, elapsed)
	}
	t.Logf("stalled worker contained: %d dispatch(es), longest budget %s, "+
		"returned after %s of a %s runway", len(handed), maxDur(handed), elapsed, runway)
}

func maxDur(ds []time.Duration) time.Duration {
	var m time.Duration
	for _, d := range ds {
		if d > m {
			m = d
		}
	}
	return m
}
