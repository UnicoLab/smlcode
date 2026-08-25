package loop

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// readRunnerSource reads runner.go so the wiring guard below can assert on the
// source itself — the only way to catch a NEW dispatch site added later.
func readRunnerSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatalf("read runner.go: %v", err)
	}
	return string(b)
}

func countOccurrences(s, sub string) int { return strings.Count(s, sub) }

// TestCallTimeoutNeverOutlivesTheRun guards the defect behind every fix-a-bug
// ceiling miss measured across v0.18.1, v0.18.2 and the harvest build: the loop
// dispatched a full-length agent call with less wall-clock left than the call
// was allowed to take. The deadline then arrived mid-call and killed the run
// outright — no QA gate, no board write, no summary — in runs that had already
// left a correct tree on disk.
func TestCallTimeoutNeverOutlivesTheRun(t *testing.T) {
	r := &Runner{Timeout: 8 * time.Minute}

	t.Run("ample runway keeps the full budget", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		if got := r.callTimeout(ctx); got != r.Timeout {
			t.Fatalf("clamped a call that fits: got %s, want %s", got, r.Timeout)
		}
	})

	t.Run("a short runway clamps the call", func(t *testing.T) {
		// Six minutes left, an eight-minute call: it cannot finish.
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		// RunBoard fixes the reserve on entry; a bare Runner has none, and
		// without it there is nothing to hold back. Doing this explicitly keeps
		// the test honest about the real lifecycle rather than asserting a
		// property the production path establishes elsewhere.
		short := &Runner{Timeout: 8 * time.Minute}
		short.noteRunway(ctx)
		got := short.callTimeout(ctx)
		if got >= 6*time.Minute {
			t.Fatalf("call budget %s is not shorter than the %s left — the wall "+
				"arrives mid-call and takes the finish path with it", got, 6*time.Minute)
		}
		if got > 5*time.Minute {
			t.Fatalf("call budget %s leaves too little of the 6m runway for the "+
				"finish path", got)
		}
		if got <= 0 {
			t.Fatalf("non-positive call budget: %s", got)
		}
	})

	t.Run("the reserve is fixed once, not recomputed per call", func(t *testing.T) {
		// THE BUG THIS REPLACED: a reserve taken as a fraction of what REMAINS
		// is geometric and reserves nothing. Measured on a 6s runway, successive
		// 4/5 clamps handed out 4.8s, 0.96s, 0.19s … summing back to the whole
		// 6s. Each call was individually affordable; together they ate the run.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rr := &Runner{Timeout: time.Hour}
		rr.noteRunway(ctx)
		first := rr.finishReserve()
		if first <= 0 {
			t.Fatal("no reserve was established")
		}
		// A later board in the same run must not shrink it.
		time.Sleep(20 * time.Millisecond)
		rr.noteRunway(ctx)
		if got := rr.finishReserve(); got != first {
			t.Fatalf("the reserve was recomputed: %s then %s — that is the "+
				"geometric bug returning", first, got)
		}
	})

	t.Run("dispatch stops once only the reserve is left", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		rr := &Runner{Timeout: time.Hour}
		rr.noteRunway(ctx)
		if rr.runwaySpent(ctx) {
			t.Fatal("declared out of runway immediately")
		}
		time.Sleep(260 * time.Millisecond)
		if !rr.runwaySpent(ctx) {
			t.Fatal("still scheduling agent work with only the finish reserve left — " +
				"clamping each call is not enough on its own, dispatch has to stop")
		}
	})

	t.Run("no deadline means nothing to clamp against", func(t *testing.T) {
		if got := r.callTimeout(context.Background()); got != r.Timeout {
			t.Fatalf("clamped without a wall budget: %s", got)
		}
	})

	t.Run("nil context and nil runner are safe", func(t *testing.T) {
		//nolint:staticcheck // deliberately nil: this runs on every dispatch path.
		if got := r.callTimeout(nil); got != r.Timeout {
			t.Fatalf("nil ctx changed the budget: %s", got)
		}
		var nilRunner *Runner
		if got := nilRunner.callTimeout(context.Background()); got != 0 {
			t.Fatalf("nil runner returned %s", got)
		}
	})

	t.Run("past the deadline the budget is left alone", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		time.Sleep(2 * time.Millisecond)
		if got := r.callTimeout(ctx); got <= 0 {
			t.Fatalf("produced a non-positive budget past the deadline: %s", got)
		}
	})
}

// TestEveryAgentDispatchIsClamped is the wiring guard. The orchestrator's own
// role calls were made runway-aware first and it changed NOTHING measurable,
// because the calls that burn a run's budget are dispatched here on the flat
// r.Timeout. A future dispatch site added with `Timeout: r.Timeout` would
// silently reopen the hole, so this counts the source itself.
func TestEveryAgentDispatchIsClamped(t *testing.T) {
	src := readRunnerSource(t)
	if n := countOccurrences(src, "Timeout: r.Timeout,"); n != 0 {
		t.Fatalf("%d agent dispatch site(s) still use the unclamped r.Timeout — "+
			"they can outlive the run and kill it mid-call", n)
	}
	if n := countOccurrences(src, "Timeout:    r.Timeout,"); n != 0 {
		t.Fatalf("%d aligned dispatch site(s) still use the unclamped r.Timeout", n)
	}
	if n := countOccurrences(src, "r.callTimeout(ctx)"); n < 6 {
		t.Fatalf("only %d clamped dispatch site(s) found; the six known ones must "+
			"all go through callTimeout", n)
	}
}
