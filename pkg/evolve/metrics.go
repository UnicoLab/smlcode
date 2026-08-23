package evolve

import (
	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
)

// MetricsFor derives the per-run metrics record from a run report and the
// reflection computed from it. Keeping the derivation here (rather than in
// pkg/eval/metrics) is what lets the metrics package stay dependency-free and
// importable from the orchestrator.
func MetricsFor(r RunReport, ref Reflection) metrics.Metrics {
	m := metrics.Metrics{
		RunID:    r.RunID,
		At:       r.EndedAt,
		Model:    r.Model,
		Provider: r.Provider,
		Language: r.Language,

		Tasks:       r.PlannedTasks,
		TasksPassed: r.CompletedTasks,

		EditFormat:        r.EditFormat,
		EditsAttempted:    r.EditsAttempted,
		EditsApplied:      r.EditsApplied,
		EditsFirstAttempt: r.EditsFirstAttempt,

		ToolCalls:      r.ToolCalls,
		ToolErrors:     r.ToolErrors,
		RedundantCalls: r.RedundantCalls,

		LLMCalls:  r.LLMCalls,
		TokensIn:  r.TokensIn,
		TokensOut: r.TokensOut,
		WallMS:    r.WallMS(),

		Failures:           len(r.Failures),
		ResolvedFromMemory: ref.ResolvedFromMemory,
		ResolvedFromLLM:    ref.ResolvedFromLLM,
		Unresolved:         ref.Unresolved,
	}
	for _, g := range r.Gates {
		m.Gates = append(m.Gates, metrics.Gate{Name: g.Name, Passed: g.Passed})
	}
	// A repair-rule hit is a failure that arrived at a stored rule, whether or
	// not the rule ended up being the thing that fixed it.
	for _, f := range r.Failures {
		if f.RuleID != "" {
			m.RepairHits++
		}
	}
	m.Normalize(r.EndedAt)
	return m
}

// RecordMetrics appends the run's metrics to <projectDir>/.slmcode/metrics/runs.jsonl
// and prunes the log. Best-effort: a metrics failure never fails a run.
func RecordMetrics(projectDir string, r RunReport, ref Reflection) error {
	if projectDir == "" {
		return nil
	}
	path := metrics.Path(projectDir)
	if err := metrics.Append(path, MetricsFor(r, ref)); err != nil {
		return err
	}
	_, err := metrics.Prune(path, metrics.MaxRuns)
	return err
}
