// Package metrics records, persists and compares per-run harness metrics.
//
// It exists so "the harness improves over time" is a measurable claim rather
// than an aspiration. Every run appends one JSON object to
// .slmcode/metrics/runs.jsonl; Compare turns two sets of runs into a readable
// delta; and the replay harness in this package re-scores stored trajectories
// offline so a change can be A/B'd with no live model.
//
// Edit-format apply rate is a first-class metric alongside task pass rate,
// because for a 7B–32B model edit-format compliance IS the bottleneck: a plan
// that is right and an edit that will not apply produce exactly zero working
// code. Aider's leaderboard reports "% of responses using the correct edit
// format" next to task success for the same reason.
//
// There are TWO such rates and they measure different things:
//
//   - EditApplyRate      — did the edit land in the end, counting the ones that
//     only worked after a repaired argument or a retry.
//   - FirstAttemptApplyRate — did the edit land exactly as the model emitted it.
//
// The second is the one Aider's leaderboard tracks and the one that matters for
// a small model: a format that needs two repairs before it applies is not a
// format that model can use, however good the eventual number looks. Reporting
// only the first conflates "the harness recovered" with "the model complied",
// which is precisely the confusion that lets a harness change look like a model
// improvement.
//
// The package deliberately depends on nothing but the standard library and
// pkg/internal/atomicfile, so the orchestrator can import it without dragging
// in the evaluation harness (which itself imports the orchestrator).
package metrics

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// DefaultPath is where a project's run metrics live.
const DefaultPath = ".slmcode/metrics/runs.jsonl"

// Bounds. A metrics log that grows without limit is a bug, not a feature.
const (
	MaxRuns    = 2000
	MaxLineLen = 64 * 1024
	MaxGates   = 12
)

// Gate is one quality-gate outcome.
type Gate struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// Metrics is one run's record.
type Metrics struct {
	RunID    string    `json:"run_id"`
	At       time.Time `json:"at"`
	Model    string    `json:"model,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Language string    `json:"language,omitempty"`
	Label    string    `json:"label,omitempty"` // free-form: "baseline", "with-memory"…

	Tasks       int `json:"tasks"`
	TasksPassed int `json:"tasks_passed"`

	// EditFormat is the format in use; EditsAttempted/EditsApplied give the
	// apply rate, the headline small-model metric.
	EditFormat     string `json:"edit_format,omitempty"`
	EditsAttempted int    `json:"edits_attempted"`
	EditsApplied   int    `json:"edits_applied"`
	// EditsFirstAttempt counts the edits that applied exactly as the model
	// emitted them — no repaired arguments, no second try. It is always
	// <= EditsApplied; the gap between them is what the harness recovered.
	EditsFirstAttempt int `json:"edits_first_attempt"`

	ToolCalls      int `json:"tool_calls"`
	ToolErrors     int `json:"tool_errors"`
	RedundantCalls int `json:"redundant_calls"`

	LLMCalls  int   `json:"llm_calls"`
	TokensIn  int   `json:"tokens_in"`
	TokensOut int   `json:"tokens_out"`
	WallMS    int64 `json:"wall_ms"`

	Gates []Gate `json:"gates,omitempty"`

	// Failure accounting. RepairHits counts failures that matched a stored
	// repair rule; the Resolved* fields partition how each failure ended.
	Failures           int `json:"failures"`
	RepairHits         int `json:"repair_hits"`
	ResolvedFromMemory int `json:"resolved_from_memory"`
	ResolvedFromLLM    int `json:"resolved_from_llm"`
	Unresolved         int `json:"unresolved"`
}

// Normalize fills defaults and bounds the record.
func (m *Metrics) Normalize(now time.Time) {
	if m.At.IsZero() {
		m.At = now
	}
	m.At = m.At.UTC().Truncate(time.Second)
	m.RunID = strings.TrimSpace(m.RunID)
	m.Model = strings.TrimSpace(m.Model)
	m.Provider = strings.TrimSpace(m.Provider)
	m.Language = strings.ToLower(strings.TrimSpace(m.Language))
	m.EditFormat = strings.TrimSpace(m.EditFormat)
	m.Label = strings.TrimSpace(m.Label)
	if len(m.Gates) > MaxGates {
		m.Gates = m.Gates[:MaxGates]
	}
	for i := range m.Gates {
		m.Gates[i].Name = clip(m.Gates[i].Name, 80)
	}
	for _, p := range []*int{
		&m.Tasks, &m.TasksPassed, &m.EditsAttempted, &m.EditsApplied, &m.EditsFirstAttempt,
		&m.ToolCalls, &m.ToolErrors, &m.RedundantCalls, &m.LLMCalls,
		&m.TokensIn, &m.TokensOut, &m.Failures, &m.RepairHits,
		&m.ResolvedFromMemory, &m.ResolvedFromLLM, &m.Unresolved,
	} {
		if *p < 0 {
			*p = 0
		}
	}
	if m.WallMS < 0 {
		m.WallMS = 0
	}
	if m.TasksPassed > m.Tasks {
		m.Tasks = m.TasksPassed
	}
	if m.EditsApplied > m.EditsAttempted {
		m.EditsAttempted = m.EditsApplied
	}
	// A first-attempt apply IS an apply, so it can never exceed either count.
	if m.EditsFirstAttempt > m.EditsApplied {
		m.EditsFirstAttempt = m.EditsApplied
	}
}

// Rates. Each returns -1 when the denominator is zero, so "no data" is
// distinguishable from "zero percent" — averaging a fake 0 is how a metric
// starts lying.
func (m Metrics) TaskPassRate() float64  { return ratio(m.TasksPassed, m.Tasks) }
func (m Metrics) EditApplyRate() float64 { return ratio(m.EditsApplied, m.EditsAttempted) }

// FirstAttemptApplyRate is "% of responses using the correct edit format": the
// share of edit attempts that applied as emitted, with no repair and no retry.
// It is deliberately NOT the same number as EditApplyRate — see the package
// doc for why keeping them apart is the point.
func (m Metrics) FirstAttemptApplyRate() float64 {
	return ratio(m.EditsFirstAttempt, m.EditsAttempted)
}

// EditRepairRate is the share of applied edits that only landed because the
// harness repaired or retried them. High is a working harness AND a model that
// cannot use the format it was given.
func (m Metrics) EditRepairRate() float64 {
	return ratio(m.EditsApplied-m.EditsFirstAttempt, m.EditsAttempted)
}
func (m Metrics) ToolErrorRate() float64     { return ratio(m.ToolErrors, m.ToolCalls) }
func (m Metrics) RedundantCallRate() float64 { return ratio(m.RedundantCalls, m.ToolCalls) }
func (m Metrics) RepairHitRate() float64     { return ratio(m.RepairHits, m.Failures) }

// MemoryResolutionRate is the share of resolved failures that were fixed from
// memory instead of a fresh LLM round-trip. This is the number that says
// whether "fail once" is actually working.
func (m Metrics) MemoryResolutionRate() float64 {
	return ratio(m.ResolvedFromMemory, m.ResolvedFromMemory+m.ResolvedFromLLM)
}

// GatePassRate is the share of gates that passed.
func (m Metrics) GatePassRate() float64 {
	if len(m.Gates) == 0 {
		return -1
	}
	passed := 0
	for _, g := range m.Gates {
		if g.Passed {
			passed++
		}
	}
	return ratio(passed, len(m.Gates))
}

// LLMCallsPerTask is the cost of a task in model round-trips.
func (m Metrics) LLMCallsPerTask() float64 {
	if m.Tasks <= 0 {
		return -1
	}
	return float64(m.LLMCalls) / float64(m.Tasks)
}

// TokensPerTask is the token cost of a task.
func (m Metrics) TokensPerTask() float64 {
	if m.Tasks <= 0 {
		return -1
	}
	return float64(m.TokensIn+m.TokensOut) / float64(m.Tasks)
}

// WallSecondsPerTask is the wall-clock cost of a task.
func (m Metrics) WallSecondsPerTask() float64 {
	if m.Tasks <= 0 {
		return -1
	}
	return float64(m.WallMS) / 1000 / float64(m.Tasks)
}

func ratio(n, d int) float64 {
	if d <= 0 {
		return -1
	}
	return float64(n) / float64(d)
}

// Path resolves the metrics log for a project root.
func Path(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, filepath.FromSlash(DefaultPath))
}

// Append writes one record to the JSONL log at path.
func Append(path string, m Metrics) error {
	if path == "" {
		return nil
	}
	m.Normalize(time.Now())
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // caller's own metrics path
	if err != nil {
		return err
	}
	_, wErr := f.Write(append(data, '\n'))
	return errors.Join(wErr, f.Close())
}

// AppendTo is Append against a project root.
func AppendTo(projectDir string, m Metrics) error { return Append(Path(projectDir), m) }

// Load reads every record from a JSONL log, oldest first. Corrupt lines are
// skipped, never fatal — a half-written record must not cost you the history
// either side of it. A missing file yields an empty slice and no error.
func Load(path string) ([]Metrics, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path) //nolint:gosec // caller's own metrics path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Metrics
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), MaxLineLen)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m Metrics
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	// A scanner error (an over-long line) still returns what was parsed.
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// LoadFrom is Load against a project root.
func LoadFrom(projectDir string) ([]Metrics, error) { return Load(Path(projectDir)) }

// Prune rewrites the log keeping at most maxRuns of the most recent records.
func Prune(path string, maxRuns int) (int, error) {
	if path == "" {
		return 0, nil
	}
	if maxRuns <= 0 {
		maxRuns = MaxRuns
	}
	all, err := Load(path)
	if err != nil {
		return 0, err
	}
	if len(all) <= maxRuns {
		return 0, nil
	}
	kept := all[len(all)-maxRuns:]
	var buf []byte
	for _, m := range kept {
		data, err := json.Marshal(m)
		if err != nil {
			continue
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	if err := atomicfile.Write(path, buf, 0o600); err != nil {
		return 0, err
	}
	return len(all) - len(kept), nil
}

// Filter selects records.
type Filter struct {
	Label    string
	Model    string
	Language string
	Since    time.Time
	Until    time.Time
	Last     int // keep only the last N matches
}

// Select applies a filter.
func Select(in []Metrics, f Filter) []Metrics {
	var out []Metrics
	for _, m := range in {
		if f.Label != "" && !strings.EqualFold(m.Label, f.Label) {
			continue
		}
		if f.Model != "" && !strings.EqualFold(m.Model, f.Model) {
			continue
		}
		if f.Language != "" && !strings.EqualFold(m.Language, f.Language) {
			continue
		}
		if !f.Since.IsZero() && m.At.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && m.At.After(f.Until) {
			continue
		}
		out = append(out, m)
	}
	if f.Last > 0 && len(out) > f.Last {
		out = out[len(out)-f.Last:]
	}
	return out
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}
