package eval

import (
	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
)

// Per-run harness metrics live in pkg/eval/metrics, which depends only on the
// standard library so the orchestrator can import it (this package imports the
// orchestrator, so it cannot be the home of anything the orchestrator needs).
// These aliases exist so `eval.Metrics` remains a discoverable entry point.
type (
	// Metrics is one run's record: pass rate, edit-format apply rate, tool
	// error rate, cost and failure accounting.
	Metrics = metrics.Metrics
	// MetricsSummary is the pooled aggregate of a set of runs.
	MetricsSummary = metrics.Summary
	// MetricsComparison is a baseline-vs-current delta.
	MetricsComparison = metrics.Comparison
	// Trajectory is a recorded task, replayable offline.
	Trajectory = metrics.Trajectory
)

// MetricsPath returns a project's metrics log path.
func MetricsPath(projectDir string) string { return metrics.Path(projectDir) }

// AppendMetrics appends one run record to a project's metrics log.
func AppendMetrics(projectDir string, m Metrics) error { return metrics.AppendTo(projectDir, m) }

// LoadMetrics reads a project's metrics log, skipping corrupt records.
func LoadMetrics(projectDir string) ([]Metrics, error) { return metrics.LoadFrom(projectDir) }

// CompareMetrics produces a readable delta between two sets of runs.
func CompareMetrics(baseline, current []Metrics) MetricsComparison {
	return metrics.Compare(baseline, current)
}
