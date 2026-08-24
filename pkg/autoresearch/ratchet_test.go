package autoresearch

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRatchet(t *testing.T, root string, ev Evaluator, mutate func(*RatchetOptions)) *Ratchet {
	t.Helper()
	opts := RatchetOptions{
		Surface:   mustReflect(t, root),
		Evaluator: ev,
		Proposer:  NewDeterministicProposer(1),
		Budget:    Budget{MaxExperiments: 4, MaxWallClock: time.Minute, MaxTokens: 1 << 30},
	}
	if mutate != nil {
		mutate(&opts)
	}
	r, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// fixedProposer proposes one scripted change per call, so a ratchet test can
// state exactly which knob moves and in what order.
type fixedProposer struct {
	changes []Change
	n       int
}

func (p *fixedProposer) Propose(ctx context.Context, s *Surface, h History) (Change, error) {
	if p.n >= len(p.changes) {
		return Change{}, ErrNoProposal
	}
	c := p.changes[p.n]
	p.n++
	if k, ok := s.Knob(c.KnobID); ok {
		c.Before = k.Value
	}
	return c, nil
}

func TestRatchetKeepsAnImprovementAndRevertsARegression(t *testing.T) {
	root := newTestProject(t)
	agentPath := filepath.Join(root, ".slmcode", "agents", "worker.yaml")

	prop := &fixedProposer{changes: []Change{
		{KnobID: "agent:worker.temperature", After: "0.35"}, // improves → keep
		{KnobID: "agent:worker.temperature", After: "0.75"}, // regresses → revert
	}}
	ev := &scriptEvaluator{scores: []Score{
		healthy(0.50), // baseline
		healthy(0.70), // trial 1
		healthy(0.60), // trial 2 — better than baseline, worse than the champion
	}}

	r := newRatchet(t, root, ev, func(o *RatchetOptions) { o.Proposer = prop })
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(res.Trials) != 2 {
		t.Fatalf("ran %d trials, want 2", len(res.Trials))
	}
	if !res.Trials[0].Kept {
		t.Errorf("the improving trial was not kept: %s", res.Trials[0].Reason)
	}
	if res.Trials[1].Kept {
		t.Errorf("the regressing trial was kept: %s", res.Trials[1].Reason)
	}
	if res.Best.Primary != 0.70 {
		t.Errorf("Best.Primary = %v, want 0.70", res.Best.Primary)
	}
	if len(res.Kept) != 1 || res.Kept[0].After != "0.35" {
		t.Errorf("Kept = %v, want the 0.35 change only", res.Kept)
	}

	// The file must hold the kept value, not the reverted one.
	body := readFile(t, agentPath)
	if !strings.Contains(body, "temperature: 0.35") {
		t.Fatalf("the kept change is not on disk:\n%s", body)
	}
	if strings.Contains(body, "0.75") {
		t.Fatalf("the reverted change survived on disk:\n%s", body)
	}
}

// TestRatchetRevertsAChangeThatGamesThePrimaryMetric is THE anti-gaming
// guarantee: a change that improves the primary metric and pays for it in a
// guarded metric is reverted, not kept. Without this the loop's incentive is to
// find the knob that buys pass rate with tokens, wall clock, swallowed tool
// errors or a propped-up apply rate — which is precisely how a ratchet
// optimizes the metric it can see straight past the thing you wanted.
func TestRatchetRevertsAChangeThatGamesThePrimaryMetric(t *testing.T) {
	gamed := []struct {
		name  string
		score Score
		guard string
	}{
		{
			name:  "buys pass rate with tokens",
			score: Score{Primary: 0.95, TokensPerTask: 2500, SecondsPerTask: 10, ToolErrorRate: 0.05, EditApplyRate: 0.90},
			guard: "tokens per task",
		},
		{
			name:  "buys pass rate with wall clock",
			score: Score{Primary: 0.95, TokensPerTask: 1000, SecondsPerTask: 40, ToolErrorRate: 0.05, EditApplyRate: 0.90},
			guard: "wall seconds per task",
		},
		{
			name:  "buys pass rate with tool errors",
			score: Score{Primary: 0.95, TokensPerTask: 1000, SecondsPerTask: 10, ToolErrorRate: 0.40, EditApplyRate: 0.90},
			guard: "tool error rate",
		},
		{
			name:  "buys pass rate with a worse edit-format apply rate",
			score: Score{Primary: 0.95, TokensPerTask: 1000, SecondsPerTask: 10, ToolErrorRate: 0.05, EditApplyRate: 0.55},
			guard: "edit-format apply rate",
		},
	}

	for _, g := range gamed {
		t.Run(g.name, func(t *testing.T) {
			root := newTestProject(t)
			agentPath := filepath.Join(root, ".slmcode", "agents", "worker.yaml")
			before := readFile(t, agentPath)

			prop := &fixedProposer{changes: []Change{
				{KnobID: "agent:worker.max_tokens", After: "8192"},
			}}
			ev := &scriptEvaluator{scores: []Score{healthy(0.50), g.score}}

			r := newRatchet(t, root, ev, func(o *RatchetOptions) { o.Proposer = prop })
			res, err := r.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			trial := res.Trials[0]
			if trial.Score.Primary <= res.Baseline.Primary {
				t.Fatal("the fixture does not improve the primary metric — this test would pass vacuously")
			}
			if trial.Kept {
				t.Fatalf("a change that regressed %q was KEPT: %s", g.guard, trial.Reason)
			}
			if trial.Guard != g.guard {
				t.Errorf("Guard = %q, want %q (reason: %s)", trial.Guard, g.guard, trial.Reason)
			}
			if res.GuardVetoes() != 1 {
				t.Errorf("GuardVetoes = %d, want 1", res.GuardVetoes())
			}
			if res.Best.Primary != res.Baseline.Primary {
				t.Errorf("Best moved to %v despite the veto", res.Best.Primary)
			}
			if got := readFile(t, agentPath); got != before {
				t.Fatalf("the vetoed change was not reverted on disk:\n%s", got)
			}
		})
	}
}

// TestRatchetGuardsAgainstAccumulatedDrift: five steps each individually inside
// the tolerance still land far outside it. Checking only against the current
// champion would let every one of them through.
func TestRatchetGuardsAgainstAccumulatedDrift(t *testing.T) {
	root := newTestProject(t)
	// Each step: primary +0.02, tokens +4% (under the 5% per-step tolerance).
	scores := []Score{healthy(0.50)}
	primary, tokens := 0.50, 1000.0
	for i := 0; i < 6; i++ {
		primary += 0.02
		tokens *= 1.04
		scores = append(scores, Score{
			Primary: primary, TokensPerTask: tokens, SecondsPerTask: 10,
			ToolErrorRate: 0.05, EditApplyRate: 0.90,
		})
	}
	ev := &scriptEvaluator{scores: scores}
	r := newRatchet(t, root, ev, func(o *RatchetOptions) { o.Budget.MaxExperiments = 6 })
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 1.04^2 = 1.0816, already past 5% of the baseline: the second step must be
	// the one the baseline guard stops.
	if len(res.Kept) != 1 {
		t.Fatalf("kept %d changes, want 1 — accumulated drift got through: %v", len(res.Kept), res.Kept)
	}
	if res.Best.TokensPerTask > 1000*1.05+1e-9 {
		t.Errorf("tokens per task drifted to %v, past the baseline tolerance", res.Best.TokensPerTask)
	}
}

// TestRatchetRestoresTheSurfaceWhenEvaluationExplodes is the crash-safety
// contract: a panicking, erroring or canceled evaluation must leave the
// project byte-for-byte as it was. A killed run that leaves half-mutated agent
// YAMLs is worse than no ratchet at all.
func TestRatchetRestoresTheSurfaceWhenEvaluationExplodes(t *testing.T) {
	boom := errors.New("evaluator failed")

	cases := []struct {
		name  string
		build func(root string, cancel context.CancelFunc) Evaluator
		want  string
	}{
		{
			name: "panic",
			build: func(string, context.CancelFunc) Evaluator {
				return &scriptEvaluator{scores: []Score{healthy(0.5)}, panicAt: 2}
			},
			want: "panicked",
		},
		{
			name: "error",
			build: func(string, context.CancelFunc) Evaluator {
				return &scriptEvaluator{scores: []Score{healthy(0.5), {}}, errs: []error{nil, boom}}
			},
			want: "evaluator failed",
		},
		{
			name: "context cancel",
			build: func(_ string, cancel context.CancelFunc) Evaluator {
				return EvaluatorFunc(func(ctx context.Context) (Score, error) {
					if ctx.Err() != nil {
						return Score{}, ctx.Err()
					}
					cancel() // canceled DURING the trial, after the file was written
					return Score{}, ctx.Err()
				})
			},
			want: "context canceled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newTestProject(t)
			s := mustReflect(t, root)
			paths := s.Files()
			before, err := HashFiles(paths)
			if err != nil {
				t.Fatalf("HashFiles: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var ev Evaluator
			if tc.name == "context cancel" {
				// The baseline must succeed, so only the trial call cancels.
				calls := 0
				inner := tc.build(root, cancel)
				ev = EvaluatorFunc(func(c context.Context) (Score, error) {
					calls++
					if calls == 1 {
						return healthy(0.5), nil
					}
					return inner.Evaluate(c)
				})
			} else {
				ev = tc.build(root, cancel)
			}

			r, err := New(RatchetOptions{
				Surface:   s,
				Evaluator: ev,
				Proposer:  &fixedProposer{changes: []Change{{KnobID: "agent:worker.max_iter", After: "20"}}},
				Budget:    Budget{MaxExperiments: 3, MaxWallClock: time.Minute, MaxTokens: 1 << 30},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			res, runErr := r.Run(ctx)
			if runErr != nil {
				t.Fatalf("Run returned an error instead of a reportable result: %v", runErr)
			}

			after, err := HashFiles(paths)
			if err != nil {
				t.Fatalf("HashFiles: %v", err)
			}
			for _, p := range paths {
				if before[p] != after[p] {
					t.Errorf("%s was not restored byte-for-byte (%s → %s)", p, before[p], after[p])
				}
			}
			if len(res.Trials) != 1 {
				t.Fatalf("ran %d trials, want 1", len(res.Trials))
			}
			if res.Trials[0].Kept {
				t.Error("a trial whose evaluation exploded was kept")
			}
			if !strings.Contains(res.Trials[0].Error, tc.want) {
				t.Errorf("Trial.Error = %q, want it to mention %q", res.Trials[0].Error, tc.want)
			}
			// The run must SAY it stopped badly rather than reporting a clean finish.
			if res.StopReason != StopEvalFailed && res.StopReason != StopCanceled {
				t.Errorf("StopReason = %q, want an explicit failure", res.StopReason)
			}
			if res.StopDetail == "" {
				t.Error("StopDetail is empty — the failure was hidden")
			}
			// And the in-memory surface must agree with the disk.
			if k, _ := s.Knob("agent:worker.max_iter"); k.Value != "12" {
				t.Errorf("surface still holds max_iter = %q after the revert", k.Value)
			}
		})
	}
}

// TestRatchetBudgetExhaustionReturnsBestSoFarWithAStatedReason: a run that
// spends its budget must hand back what it found AND say that it ran out. A
// score table with no stop reason cannot distinguish "converged" from "ran out
// of money", and those are entirely different results.
func TestRatchetBudgetExhaustionReturnsBestSoFarWithAStatedReason(t *testing.T) {
	t.Run("experiments", func(t *testing.T) {
		root := newTestProject(t)
		ev := &scriptEvaluator{scores: []Score{
			healthy(0.50), healthy(0.60), healthy(0.65), healthy(0.55),
		}}
		r := newRatchet(t, root, ev, func(o *RatchetOptions) { o.Budget.MaxExperiments = 3 })
		res, err := r.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.Experiments != 3 {
			t.Fatalf("ran %d experiments, want exactly 3", res.Experiments)
		}
		if res.StopReason != StopExperiments {
			t.Fatalf("StopReason = %q, want %q", res.StopReason, StopExperiments)
		}
		if !strings.Contains(res.StopDetail, "NOT exhausted") {
			t.Errorf("StopDetail does not say the surface still has untried knobs: %q", res.StopDetail)
		}
		if res.Best.Primary != 0.65 {
			t.Errorf("Best.Primary = %v, want the best found (0.65)", res.Best.Primary)
		}
	})

	t.Run("tokens", func(t *testing.T) {
		root := newTestProject(t)
		expensive := healthy(0.60)
		expensive.Tokens = 5000
		ev := &scriptEvaluator{scores: []Score{healthy(0.50), expensive, expensive}}
		r := newRatchet(t, root, ev, func(o *RatchetOptions) { o.Budget.MaxTokens = 4000 })
		res, err := r.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.StopReason != StopTokens {
			t.Fatalf("StopReason = %q, want %q (used %d)", res.StopReason, StopTokens, res.TokensUsed)
		}
		if res.Best.Primary != 0.60 {
			t.Errorf("Best.Primary = %v — the best-so-far was lost", res.Best.Primary)
		}
	})

	t.Run("wall clock", func(t *testing.T) {
		root := newTestProject(t)
		clock := time.Unix(0, 0)
		ev := &scriptEvaluator{scores: []Score{healthy(0.50), healthy(0.60)}}
		r := newRatchet(t, root, ev, func(o *RatchetOptions) {
			o.Budget.MaxWallClock = 90 * time.Second
			o.Now = func() time.Time {
				clock = clock.Add(30 * time.Second)
				return clock
			}
		})
		res, err := r.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.StopReason != StopWallClock {
			t.Fatalf("StopReason = %q, want %q", res.StopReason, StopWallClock)
		}
	})

	t.Run("exhausted is not the same as out of budget", func(t *testing.T) {
		root := t.TempDir()
		agents := filepath.Join(root, ".slmcode", "agents")
		mkdirAll(t, agents)
		writeFile(t, filepath.Join(agents, "tiny.yaml"), "id: tiny\nmax_iter: 1\n")
		s, err := Reflect(Options{Root: root, NoConfig: true})
		if err != nil {
			t.Fatalf("Reflect: %v", err)
		}
		r, err := New(RatchetOptions{
			Surface:   s,
			Evaluator: &scriptEvaluator{scores: []Score{healthy(0.5)}},
			Proposer:  &fixedProposer{}, // nothing to propose at all
			Budget:    Budget{MaxExperiments: 50, MaxWallClock: time.Minute, MaxTokens: 1 << 30},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		res, err := r.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.StopReason != StopExhausted {
			t.Fatalf("StopReason = %q, want %q", res.StopReason, StopExhausted)
		}
		if strings.Contains(res.StopDetail, "budget") {
			t.Errorf("an exhausted surface was reported as a spent budget: %q", res.StopDetail)
		}
	})
}

func TestRatchetDryRunWritesNothing(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)
	paths := s.Files()
	before, err := HashFiles(paths)
	if err != nil {
		t.Fatalf("HashFiles: %v", err)
	}

	r, err := New(RatchetOptions{
		Surface:     s,
		Evaluator:   &scriptEvaluator{scores: []Score{healthy(0.5)}},
		Proposer:    NewDeterministicProposer(9),
		Budget:      Budget{MaxExperiments: 5, MaxWallClock: time.Minute, MaxTokens: 1 << 30},
		DryRun:      true,
		SnapshotDir: SnapshotDir(root),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	after, err := HashFiles(paths)
	if err != nil {
		t.Fatalf("HashFiles: %v", err)
	}
	for _, p := range paths {
		if before[p] != after[p] {
			t.Errorf("a dry run modified %s", p)
		}
	}
	if res.StopReason != StopDryRun {
		t.Errorf("StopReason = %q, want %q", res.StopReason, StopDryRun)
	}
	// A dry run must not call the evaluator at all: with no flags this command
	// IS a dry run, and it must not spin up a model to say what it would try.
	if ev, ok := r.evaluator.(*scriptEvaluator); ok && ev.calls != 0 {
		t.Errorf("a dry run called the evaluator %d time(s)", ev.calls)
	}
	if res.Baseline.Primary != Unknown {
		t.Errorf("a dry run reported a baseline of %v — it measured nothing", res.Baseline.Primary)
	}
	if len(res.Kept) != 0 {
		t.Errorf("a dry run kept %d change(s)", len(res.Kept))
	}
	if len(res.Trials) == 0 {
		t.Error("a dry run proposed nothing — it should still say what it would try")
	}
	for _, tr := range res.Trials {
		if !tr.DryRun {
			t.Errorf("trial %d is not marked as a dry run", tr.Seq)
		}
	}
	// A dry run must leave no state behind at all.
	if _, err := LoadSnapshot(SnapshotDir(root)); err == nil {
		t.Error("a dry run persisted a snapshot")
	}
}

// TestRatchetRunsReplayFromASeed: the whole loop, not just the proposer.
func TestRatchetRunsReplayFromASeed(t *testing.T) {
	run := func() []string {
		root := newTestProject(t)
		ev := &scriptEvaluator{scores: []Score{
			healthy(0.50), healthy(0.55), healthy(0.52), healthy(0.60), healthy(0.58),
		}}
		r := newRatchet(t, root, ev, func(o *RatchetOptions) {
			o.Seed = 2024
			o.Proposer = NewDeterministicProposer(2024)
			o.Budget.MaxExperiments = 4
		})
		res, err := r.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		var out []string
		for _, tr := range res.Trials {
			out = append(out, tr.KnobID+"="+tr.After+":"+boolStr(tr.Kept))
		}
		return out
	}
	first, second := run(), run()
	if !equalStrings(first, second) {
		t.Fatalf("the same seed produced two different runs:\n%v\n%v", first, second)
	}
	if len(first) != 4 {
		t.Fatalf("expected 4 trials, got %d", len(first))
	}
}

func TestRatchetRefusesToStartWithoutSurfaceOrEvaluator(t *testing.T) {
	if _, err := New(RatchetOptions{Evaluator: &scriptEvaluator{}}); err == nil {
		t.Error("New accepted a nil surface")
	}
	root := newTestProject(t)
	if _, err := New(RatchetOptions{Surface: mustReflect(t, root)}); err == nil {
		t.Error("New accepted a nil evaluator")
	}
}

func TestRatchetGuardsDefaultOn(t *testing.T) {
	root := newTestProject(t)
	r, err := New(RatchetOptions{
		Surface:   mustReflect(t, root),
		Evaluator: &scriptEvaluator{scores: []Score{healthy(0.5)}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(r.guards) != len(DefaultGuards()) {
		t.Fatalf("guards = %d, want the default set (%d) — guarding must be on by default",
			len(r.guards), len(DefaultGuards()))
	}
	// Turning them off takes an explicit empty slice, never a nil.
	off, err := New(RatchetOptions{
		Surface:   mustReflect(t, root),
		Evaluator: &scriptEvaluator{scores: []Score{healthy(0.5)}},
		Guards:    []Guard{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(off.guards) != 0 {
		t.Fatalf("an explicit empty guard set was overridden with %d guards", len(off.guards))
	}
}

func TestCheckGuardsSkipsUnknownMetrics(t *testing.T) {
	base := Score{Primary: 0.5, TokensPerTask: Unknown, SecondsPerTask: 10, ToolErrorRate: Unknown, EditApplyRate: 0.9}
	cur := Score{Primary: 0.9, TokensPerTask: 99999, SecondsPerTask: 10, ToolErrorRate: 0.99, EditApplyRate: 0.9}
	if breach, ok := CheckGuards(base, cur, DefaultGuards(), "champion"); !ok {
		t.Fatalf("an unmeasured metric was treated as a regression: %s", breach)
	}
	// And the inverse: measured on both sides, it must trip.
	base.TokensPerTask, base.ToolErrorRate = 1000, 0.05
	if _, ok := CheckGuards(base, cur, DefaultGuards(), "champion"); ok {
		t.Fatal("a measured 100x token regression did not trip a guard")
	}
}

func TestRestoreLastUndoesEverythingAnAppliedRunKept(t *testing.T) {
	root := newTestProject(t)
	agentPath := filepath.Join(root, ".slmcode", "agents", "worker.yaml")
	original := readFile(t, agentPath)

	ev := &scriptEvaluator{scores: []Score{healthy(0.50), healthy(0.80)}}
	r := newRatchet(t, root, ev, func(o *RatchetOptions) {
		o.Proposer = &fixedProposer{changes: []Change{{KnobID: "agent:worker.temperature", After: "0.55"}}}
		o.SnapshotDir = SnapshotDir(root)
	})
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Kept) != 1 {
		t.Fatalf("nothing was kept; this test would prove nothing")
	}
	if readFile(t, agentPath) == original {
		t.Fatal("the kept change never reached the file")
	}

	restored, err := RestoreLast(root)
	if err != nil {
		t.Fatalf("RestoreLast: %v", err)
	}
	if len(restored) != 2 {
		t.Errorf("restored %v, want both surface files", restored)
	}
	if got := readFile(t, agentPath); got != original {
		t.Fatalf("restore did not put the file back:\n%s", got)
	}
}

func boolStr(b bool) string {
	if b {
		return "kept"
	}
	return "reverted"
}
