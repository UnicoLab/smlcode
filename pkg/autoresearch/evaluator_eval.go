package autoresearch

import (
	"context"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/eval"
	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
)

// EvalEvaluator is the default Evaluator: it runs the fixed eval suite and
// pools the per-case metrics into one Score.
//
// It is a thin wrapper on purpose. pkg/eval already decides what a case is and
// pkg/eval/metrics already decides how records pool; re-deriving either here
// would give the ratchet a second, subtly different yardstick, and two
// yardsticks is the same as none.
type EvalEvaluator struct {
	cases []eval.Case
	cfg   *config.Config

	mu    sync.Mutex
	first []metrics.Metrics
	last  []metrics.Metrics
}

// NewEvalEvaluator builds the default evaluator. Passing nil cases uses
// eval.DefaultCases(), which needs no network beyond the configured model.
func NewEvalEvaluator(cases []eval.Case, cfg *config.Config) *EvalEvaluator {
	if len(cases) == 0 {
		cases = eval.DefaultCases()
	}
	return &EvalEvaluator{cases: append([]eval.Case(nil), cases...), cfg: cfg}
}

// Cases returns the fixed case list this evaluator scores against.
func (e *EvalEvaluator) Cases() []eval.Case {
	return append([]eval.Case(nil), e.cases...)
}

// Evaluate runs every case and returns the pooled score.
//
// A failing case is data, not an error: a change that breaks two cases SHOULD
// score badly and be reverted, which cannot happen if it aborts the run
// instead. Only a canceled context is an error.
func (e *EvalEvaluator) Evaluate(ctx context.Context) (Score, error) {
	rep := eval.RunAll(ctx, e.cases, e.cfg)
	recs := rep.Metrics()

	e.mu.Lock()
	if e.first == nil {
		e.first = recs
	}
	e.last = recs
	e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Score{}, err
	}
	return ScoreFromSummary(rep.Summary()), nil
}

// Comparison renders the first evaluation against the most recent one using
// pkg/eval/metrics, so a finished run can print the same delta table the rest
// of the harness prints.
func (e *EvalEvaluator) Comparison() metrics.Comparison {
	e.mu.Lock()
	first, last := e.first, e.last
	e.mu.Unlock()
	return metrics.Compare(first, last)
}

// ScoreFromSummary projects a pooled metrics summary onto a Score.
//
// Pooled, not averaged: metrics.Aggregate sums numerators and denominators, so
// a two-task case and a ten-task case contribute in proportion to their size.
// Every rate keeps the package's Unknown convention.
func ScoreFromSummary(s metrics.Summary) Score {
	return Score{
		Primary:        ratio(s.TasksPassed, s.Tasks),
		TokensPerTask:  per(s.TokensIn+s.TokensOut, s.Tasks),
		SecondsPerTask: seconds(s.WallMS, s.Tasks),
		ToolErrorRate:  ratio(s.ToolErrors, s.ToolCalls),
		EditApplyRate:  ratio(s.EditsApplied, s.EditsAttempted),
		Tokens:         s.TokensIn + s.TokensOut,
	}
}

// ScoreFromMetrics pools raw records and projects them.
func ScoreFromMetrics(in []metrics.Metrics) Score { return ScoreFromSummary(metrics.Aggregate(in)) }

func ratio(n, d int) float64 {
	if d <= 0 {
		return Unknown
	}
	return float64(n) / float64(d)
}

func per(n, d int) float64 {
	if d <= 0 {
		return Unknown
	}
	return float64(n) / float64(d)
}

func seconds(ms int64, tasks int) float64 {
	if tasks <= 0 {
		return Unknown
	}
	return float64(ms) / 1000 / float64(tasks)
}
