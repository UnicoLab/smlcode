package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// TestRoleTimeoutNeverOutlivesTheRun is the regression guard for the failure
// behind three of the four fix-a-bug ceiling misses measured across v0.18.1,
// v0.18.2 and the harvest build.
//
// THE DEFECT: the per-role budget was resolved from measured latency and the
// user's task_timeout, and never looked at how much of the RUN was left. So
// with eight minutes of task timeout and six minutes of wall remaining, the
// harness would start an eight-minute attempt — one that could not possibly
// finish. The wall then arrived mid-call and killed the run outright:
// `context deadline exceeded`, engine_success=false, no QA gate, no board
// write, no summary — in runs that had already left a correct tree on disk.
func TestRoleTimeoutNeverOutlivesTheRun(t *testing.T) {
	o := objectiveOrch(t, &fakeGate{}, &countingExec{}, func(c *config.Config) {
		c.TaskTimeout = 8 * time.Minute
	})

	t.Run("a full budget is kept when the runway is ample", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()
		got := o.roleTimeoutWithin(ctx, "worker")
		if want := o.roleTimeout("worker"); got != want {
			t.Fatalf("clamped an attempt that fits: got %s, want %s", got, want)
		}
	})

	t.Run("the budget is clamped to the runway", func(t *testing.T) {
		// Six minutes left, an eight-minute budget: the attempt cannot finish.
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		got := o.roleTimeoutWithin(ctx, "worker")
		if got >= 6*time.Minute {
			t.Fatalf("attempt budget %s is not shorter than the %s of runway left — "+
				"the wall will arrive mid-call and take the finish path with it",
				got, 6*time.Minute)
		}
		// And a reserve is genuinely held back for the finish path.
		if got > 5*time.Minute {
			t.Fatalf("attempt budget %s leaves too little of the 6m runway for the "+
				"QA gate, the board write and the summary", got)
		}
	})

	t.Run("without a deadline nothing is clamped", func(t *testing.T) {
		got := o.roleTimeoutWithin(context.Background(), "worker")
		if want := o.roleTimeout("worker"); got != want {
			t.Fatalf("clamped without a wall budget to clamp against: %s vs %s", got, want)
		}
	})

	t.Run("a nil context is not a panic", func(t *testing.T) {
		//nolint:staticcheck // deliberately passing nil: callers in older paths may.
		if got := o.roleTimeoutWithin(nil, "worker"); got <= 0 {
			t.Fatalf("nil ctx produced a nonsense budget: %s", got)
		}
	})

	t.Run("past the deadline the budget is left alone", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		time.Sleep(2 * time.Millisecond)
		// The call fails instantly either way; shortening it buys nothing and a
		// negative budget would be worse than the real one.
		if got := o.roleTimeoutWithin(ctx, "worker"); got <= 0 {
			t.Fatalf("produced a non-positive budget past the deadline: %s", got)
		}
	})
}
