package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/quality"
)

// scriptedGate answers red for the first `redRuns` executions and green after,
// recording how long each answer claimed to take.
//
// It exists to model the one thing a fixed-count probe budget cannot survive:
// work that becomes correct LATER IN THE RUN than the budget lasts. fakeGate is
// pinned to a single verdict for its whole life, so it can express "already
// done" and "never done" but not "done at wave 5" — which is every real run
// that the harness is supposed to cut short.
type scriptedGate struct {
	mu       sync.Mutex
	runs     int
	redRuns  int
	duration time.Duration
}

func (s *scriptedGate) run(_ context.Context, _, cmd string, _ time.Duration) quality.SmokeResult {
	s.mu.Lock()
	s.runs++
	green := s.runs > s.redRuns
	s.mu.Unlock()

	sr := quality.SmokeResult{Ran: true, Command: cmd, OK: green}
	if green {
		sr.Output = "ok\tstats\t0.2s\n"
		sr.Summary = quality.SmokePassedMarker + ": " + cmd
	} else {
		sr.Output = "--- FAIL: TestStats\nFAIL\n"
		sr.Summary = quality.SmokeFailedMarker
	}
	if s.duration > 0 {
		sr.Duration = s.duration
	}
	return sr
}

func (s *scriptedGate) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}

// TestProbeStillFiresAfterTheWorkTakesSeveralWaves is the regression guard for
// the measured `fix-a-bug` defect: 1 run in 3 burned its whole 20-minute
// ceiling with failed_tasks == 0, i.e. with nothing wrong except that nobody
// asked whether the work was done.
//
// THE DEFECT: the probe budget was a fixed count of two, and it was charged for
// RED answers. Between-waves probing spends the budget on the earliest waves —
// exactly when the answer is most reliably "not yet", because the worker has
// only just started. By wave 3 the run is blind, and if the implementation lands
// at wave 5 nothing will ever notice. The two probes are spent on the two least
// informative moments of the run.
//
// Note what this test would NOT have caught: a gate that is green from the
// start, which is what every other test in the suite exercises, passes happily
// on the broken budget because its single probe fires at wave 1.
func TestProbeStillFiresAfterTheWorkTakesSeveralWaves(t *testing.T) {
	// Red for four waves, green from the fifth: an ordinary bug fix that took a
	// few corrective rounds to land.
	gate := &scriptedGate{redRuns: 4, duration: 2 * time.Second}
	o := objectiveOrch(t, gate, &countingExec{}, nil)
	board := midBoard()

	// A real run's context carries the wall ceiling. 20 minutes is the budget
	// the live e2e scenarios use.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	stoppedAt := -1
	for i := 0; i < 12; i++ {
		// Every wave writes something new, so the fingerprint gate — which is a
		// correct and separate bound — never refuses.
		o.noteChangedFiles(fmt.Sprintf("wave%d.go", i))
		if stop, reason := o.objectiveMetBetweenWaves(ctx, board); stop {
			stoppedAt = i
			if reason == "" {
				t.Fatal("stopped without a reason for the operator")
			}
			break
		}
	}

	if stoppedAt < 0 {
		t.Fatalf("the run never noticed the objective went green: the gate was "+
			"asked %d time(s) across 12 waves and answered green from run 5 on. "+
			"This is the run that burns its whole ceiling with nothing wrong.",
			gate.count())
	}
	// Wave 4 is the first wave whose probe can see green (runs 1-4 are red).
	if stoppedAt != 4 {
		t.Fatalf("stopped at wave %d, want wave 4 — the first one that could see green", stoppedAt)
	}
}

// TestProbeBudgetScalesWithTheCostOfAsking pins the replacement rule.
//
// The budget is no longer a count, because a count cannot be right for both a
// 200ms unit suite and a 6-minute integration suite. It is an ECONOMIC test:
// asking is worth it when the time it costs is small against the time a green
// answer would save. A cheap command may be asked many times; an expensive one
// is asked only while there is enough runway for the answer to pay for itself.
func TestProbeBudgetScalesWithTheCostOfAsking(t *testing.T) {
	t.Run("a cheap command is asked on every wave that wrote", func(t *testing.T) {
		gate := &scriptedGate{redRuns: 99, duration: 200 * time.Millisecond}
		o := objectiveOrch(t, gate, &countingExec{}, nil)
		board := midBoard()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		for i := 0; i < 10; i++ {
			o.noteChangedFiles(fmt.Sprintf("wave%d.go", i))
			if stop, _ := o.objectiveMetBetweenWaves(ctx, board); stop {
				t.Fatal("a red gate stopped the board")
			}
		}
		if n := gate.count(); n != 10 {
			t.Fatalf("a 200ms command in a 20-minute run was asked %d time(s) across "+
				"10 waves, want all 10 — it costs 0.02%% of the budget", n)
		}
	})

	t.Run("an expensive command stops being asked as the runway shortens", func(t *testing.T) {
		// Six minutes per ask, and only eight minutes of wall left. One ask is
		// affordable; a second would leave no runway to act on the answer.
		gate := &scriptedGate{redRuns: 99, duration: 6 * time.Minute}
		o := objectiveOrch(t, gate, &countingExec{}, nil)
		board := midBoard()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()

		for i := 0; i < 10; i++ {
			o.noteChangedFiles(fmt.Sprintf("wave%d.go", i))
			if stop, _ := o.objectiveMetBetweenWaves(ctx, board); stop {
				t.Fatal("a red gate stopped the board")
			}
		}
		if n := gate.count(); n != 1 {
			t.Fatalf("a 6-minute command with 8 minutes left was asked %d time(s), "+
				"want exactly 1 — the first ask is free because nothing has measured "+
				"it yet, and the second cannot pay for itself", n)
		}
	})

	t.Run("without a deadline it falls back to a bounded count", func(t *testing.T) {
		gate := &scriptedGate{redRuns: 99, duration: time.Second}
		o := objectiveOrch(t, gate, &countingExec{}, nil)
		board := midBoard()
		ctx := context.Background() // no deadline: nothing to reason about

		for i := 0; i < 40; i++ {
			o.noteChangedFiles(fmt.Sprintf("wave%d.go", i))
			if stop, _ := o.objectiveMetBetweenWaves(ctx, board); stop {
				t.Fatal("a red gate stopped the board")
			}
		}
		n := gate.count()
		if n > maxProbesWithoutDeadline {
			t.Fatalf("unbounded probing with no deadline: %d runs, ceiling is %d",
				n, maxProbesWithoutDeadline)
		}
		if n < 3 {
			t.Fatalf("the no-deadline fallback is as blind as the bug it replaced: %d runs", n)
		}
	})
}
