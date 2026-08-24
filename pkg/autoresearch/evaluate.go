package autoresearch

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// Unknown is what every metric reports when its denominator was zero.
//
// The convention is pkg/eval/metrics': "no data" is a distinct answer from
// "zero percent", because averaging a fabricated 0 is how a metric starts
// lying. Guards skip unknown values entirely rather than scoring them as a
// regression or an improvement.
const Unknown = -1.0

// Score is one evaluation's outcome: the metric the ratchet optimizes plus the
// metrics that are allowed to veto it.
type Score struct {
	// Primary is the metric being optimized — task pass rate by default.
	// Higher is better.
	Primary float64 `json:"primary"`

	// The guarded metrics. These exist because a ratchet improves the metric it
	// can see: without them, "raise the pass rate" is solved perfectly well by
	// spending ten times the tokens or by disabling whatever was producing tool
	// errors.
	TokensPerTask  float64 `json:"tokens_per_task"`
	SecondsPerTask float64 `json:"seconds_per_task"`
	ToolErrorRate  float64 `json:"tool_error_rate"`
	EditApplyRate  float64 `json:"edit_apply_rate"`

	// Tokens is what THIS evaluation spent, for the token budget.
	Tokens int `json:"tokens"`
	// Label is free-form provenance ("baseline", "trial 3").
	Label string `json:"label,omitempty"`
}

// UnknownScore is every metric at Unknown: nothing was measured.
//
// A dry run uses it rather than a zero value, because a zero Score renders as
// "0.0% pass rate, 0 tokens per task" — a set of confident-looking numbers for
// an evaluation that never happened.
func UnknownScore() Score {
	return Score{
		Primary:        Unknown,
		TokensPerTask:  Unknown,
		SecondsPerTask: Unknown,
		ToolErrorRate:  Unknown,
		EditApplyRate:  Unknown,
	}
}

// Render is a one-line summary for the CLI and BEST.md.
func (s Score) Render() string {
	return fmt.Sprintf("primary %s · %s tok/task · %s s/task · tool-err %s · edit-apply %s",
		pct(s.Primary), num(s.TokensPerTask), num(s.SecondsPerTask),
		pct(s.ToolErrorRate), pct(s.EditApplyRate))
}

func pct(v float64) string {
	if v < 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f%%", v*100)
}

func num(v float64) string {
	if v < 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f", v)
}

// Evaluator scores the harness as it currently stands on disk.
//
// It must be a FIXED evaluator: the whole method depends on the yardstick not
// moving while the thing being measured does. Implementations should therefore
// hold their case list and their configuration, not re-derive them per call.
type Evaluator interface {
	Evaluate(ctx context.Context) (Score, error)
}

// EvaluatorFunc adapts a function to Evaluator.
type EvaluatorFunc func(ctx context.Context) (Score, error)

// Evaluate implements Evaluator.
func (f EvaluatorFunc) Evaluate(ctx context.Context) (Score, error) { return f(ctx) }

// Guard is one metric that may veto a change even when the primary improved.
type Guard struct {
	// Name appears in the journal and in the "reverted because" line.
	Name string `json:"name"`
	// HigherIsBetter says which direction is a regression.
	HigherIsBetter bool `json:"higher_is_better"`
	// Tolerance is how much regression is forgiven. Absolute by default;
	// Relative makes it a fraction of the baseline.
	Tolerance float64 `json:"tolerance"`
	// Relative interprets Tolerance as a fraction of the baseline value.
	// Cost metrics need this: "5% more tokens" means something on any project,
	// "400 more tokens" means something on exactly one.
	Relative bool `json:"relative"`
	// Value extracts the metric. Unknown (< 0) on either side skips the guard.
	Value func(Score) float64 `json:"-"`
}

// DefaultGuards is the on-by-default guard set.
//
// The four metrics here are the four ways a harness change can buy a better
// pass rate with something you did not agree to spend: more tokens, more wall
// clock, more tool errors swallowed somewhere, or a worse edit-format apply
// rate propped up by retries. Tolerances are deliberately tight for the two
// rates (2 percentage points — noise, not a trend) and looser for the two cost
// metrics, where a real improvement often does legitimately cost a little.
func DefaultGuards() []Guard {
	return []Guard{
		{
			Name:           "tokens per task",
			HigherIsBetter: false,
			Tolerance:      0.05,
			Relative:       true,
			Value:          func(s Score) float64 { return s.TokensPerTask },
		},
		{
			Name:           "wall seconds per task",
			HigherIsBetter: false,
			Tolerance:      0.10,
			Relative:       true,
			Value:          func(s Score) float64 { return s.SecondsPerTask },
		},
		{
			Name:           "tool error rate",
			HigherIsBetter: false,
			Tolerance:      0.02,
			Value:          func(s Score) float64 { return s.ToolErrorRate },
		},
		{
			Name:           "edit-format apply rate",
			HigherIsBetter: true,
			Tolerance:      0.02,
			Value:          func(s Score) float64 { return s.EditApplyRate },
		},
	}
}

// GuardBreach is a guard that tripped.
type GuardBreach struct {
	Name     string  `json:"name"`
	Against  string  `json:"against"` // "champion" or "baseline"
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`
	Allowed  float64 `json:"allowed"`
}

// String renders the breach for a journal reason line.
func (b GuardBreach) String() string {
	return fmt.Sprintf("%s regressed vs %s: %.4f → %.4f (allowed %.4f)",
		b.Name, b.Against, b.Baseline, b.Current, b.Allowed)
}

// CheckGuards reports the first guard that regressed beyond its tolerance,
// scanning in slice order so the answer is deterministic.
//
// A guard whose metric is Unknown on either side is skipped: a change cannot be
// blamed for a number nobody measured, and it must not be credited for one
// either. A Relative guard with a non-positive baseline is skipped for the same
// reason — 5% of nothing is nothing, which would fail every change.
func CheckGuards(baseline, current Score, guards []Guard, against string) (GuardBreach, bool) {
	for _, g := range guards {
		if g.Value == nil {
			continue
		}
		b, c := g.Value(baseline), g.Value(current)
		if b < 0 || c < 0 {
			continue
		}
		allowed := g.Tolerance
		if g.Relative {
			if b <= 0 {
				continue
			}
			allowed = g.Tolerance * math.Abs(b)
		}
		regress := c - b
		if g.HigherIsBetter {
			regress = b - c
		}
		if regress > allowed+1e-12 {
			return GuardBreach{Name: g.Name, Against: against, Baseline: b, Current: c, Allowed: allowed}, false
		}
	}
	return GuardBreach{}, true
}

// GuardNames lists the active guards, for `--surface` and the docs.
func GuardNames(guards []Guard) string {
	names := make([]string, 0, len(guards))
	for _, g := range guards {
		names = append(names, g.Name)
	}
	return strings.Join(names, ", ")
}
