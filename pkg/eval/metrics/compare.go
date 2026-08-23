package metrics

import (
	"fmt"
	"math"
	"strings"
)

// Summary is the aggregate of a set of runs. Rates are pooled (sum of
// numerators over sum of denominators), not averaged per run: averaging rates
// over runs of different sizes silently overweights the small ones.
type Summary struct {
	Runs int `json:"runs"`

	Tasks       int `json:"tasks"`
	TasksPassed int `json:"tasks_passed"`

	EditsAttempted int `json:"edits_attempted"`
	EditsApplied   int `json:"edits_applied"`
	// EditsFirstAttempt pools the edits that applied as emitted. Pooling it
	// separately (rather than deriving it from a per-run rate) keeps the
	// aggregate honest across runs of very different sizes.
	EditsFirstAttempt int `json:"edits_first_attempt"`

	ToolCalls      int `json:"tool_calls"`
	ToolErrors     int `json:"tool_errors"`
	RedundantCalls int `json:"redundant_calls"`

	GatesRun    int `json:"gates_run"`
	GatesPassed int `json:"gates_passed"`

	LLMCalls  int   `json:"llm_calls"`
	TokensIn  int   `json:"tokens_in"`
	TokensOut int   `json:"tokens_out"`
	WallMS    int64 `json:"wall_ms"`

	Failures           int `json:"failures"`
	RepairHits         int `json:"repair_hits"`
	ResolvedFromMemory int `json:"resolved_from_memory"`
	ResolvedFromLLM    int `json:"resolved_from_llm"`
	Unresolved         int `json:"unresolved"`
}

// Aggregate pools a set of runs.
func Aggregate(in []Metrics) Summary {
	var s Summary
	for _, m := range in {
		s.Runs++
		s.Tasks += m.Tasks
		s.TasksPassed += m.TasksPassed
		s.EditsAttempted += m.EditsAttempted
		s.EditsApplied += m.EditsApplied
		s.EditsFirstAttempt += m.EditsFirstAttempt
		s.ToolCalls += m.ToolCalls
		s.ToolErrors += m.ToolErrors
		s.RedundantCalls += m.RedundantCalls
		s.LLMCalls += m.LLMCalls
		s.TokensIn += m.TokensIn
		s.TokensOut += m.TokensOut
		s.WallMS += m.WallMS
		s.Failures += m.Failures
		s.RepairHits += m.RepairHits
		s.ResolvedFromMemory += m.ResolvedFromMemory
		s.ResolvedFromLLM += m.ResolvedFromLLM
		s.Unresolved += m.Unresolved
		for _, g := range m.Gates {
			s.GatesRun++
			if g.Passed {
				s.GatesPassed++
			}
		}
	}
	return s
}

// Metric identifies one comparable quantity.
type Metric struct {
	Name string
	// HigherIsBetter says which direction counts as an improvement.
	HigherIsBetter bool
	// Unit is "%", "" (count) or "s".
	Unit string
	// Value extracts the number from a summary; -1 means "no data".
	Value func(Summary) float64
}

// Metrics compared by Compare, in report order: correctness first, then the
// self-improvement signals, then cost.
func comparedMetrics() []Metric {
	return []Metric{
		{"task pass rate", true, "%", func(s Summary) float64 { return ratio(s.TasksPassed, s.Tasks) }},
		{"edit-format apply rate", true, "%", func(s Summary) float64 { return ratio(s.EditsApplied, s.EditsAttempted) }},
		// The Aider-leaderboard number: applied AS EMITTED. Ranked next to the
		// eventual apply rate on purpose — a change that moves the first only
		// proves the harness got better at recovering, not that the format fits
		// the model.
		{"first-attempt edit-format rate", true, "%", func(s Summary) float64 {
			return ratio(s.EditsFirstAttempt, s.EditsAttempted)
		}},
		{"gate pass rate", true, "%", func(s Summary) float64 { return ratio(s.GatesPassed, s.GatesRun) }},
		{"repair-rule hit rate", true, "%", func(s Summary) float64 { return ratio(s.RepairHits, s.Failures) }},
		{"failures fixed from memory", true, "%", func(s Summary) float64 {
			return ratio(s.ResolvedFromMemory, s.ResolvedFromMemory+s.ResolvedFromLLM)
		}},
		{"unresolved failures per run", false, "", func(s Summary) float64 { return per(s.Unresolved, s.Runs) }},
		{"tool error rate", false, "%", func(s Summary) float64 { return ratio(s.ToolErrors, s.ToolCalls) }},
		{"redundant-call rate", false, "%", func(s Summary) float64 { return ratio(s.RedundantCalls, s.ToolCalls) }},
		{"LLM calls per task", false, "", func(s Summary) float64 { return per(s.LLMCalls, s.Tasks) }},
		{"tokens in per task", false, "", func(s Summary) float64 { return per(s.TokensIn, s.Tasks) }},
		{"tokens out per task", false, "", func(s Summary) float64 { return per(s.TokensOut, s.Tasks) }},
		{"wall seconds per task", false, "s", func(s Summary) float64 {
			if s.Tasks <= 0 {
				return -1
			}
			return float64(s.WallMS) / 1000 / float64(s.Tasks)
		}},
	}
}

func per(n, d int) float64 {
	if d <= 0 {
		return -1
	}
	return float64(n) / float64(d)
}

// Delta is one metric's movement between two sets of runs.
type Delta struct {
	Name           string  `json:"name"`
	Unit           string  `json:"unit,omitempty"`
	Baseline       float64 `json:"baseline"`
	Current        float64 `json:"current"`
	Change         float64 `json:"change"`
	HigherIsBetter bool    `json:"higher_is_better"`
	// Known is false when either side had no data for this metric.
	Known bool `json:"known"`
}

// Better reports whether the metric moved in the good direction.
func (d Delta) Better() bool {
	if !d.Known || d.Change == 0 {
		return false
	}
	if d.HigherIsBetter {
		return d.Change > 0
	}
	return d.Change < 0
}

// Worse reports whether the metric moved in the bad direction.
func (d Delta) Worse() bool {
	if !d.Known || d.Change == 0 {
		return false
	}
	return !d.Better()
}

// Comparison is the full baseline-vs-current delta.
type Comparison struct {
	Baseline Summary `json:"baseline"`
	Current  Summary `json:"current"`
	Deltas   []Delta `json:"deltas"`
}

// Improved reports whether more compared metrics got better than got worse.
func (c Comparison) Improved() bool {
	better, worse := 0, 0
	for _, d := range c.Deltas {
		switch {
		case d.Better():
			better++
		case d.Worse():
			worse++
		}
	}
	return better > worse
}

// Compare produces a readable delta between two sets of runs.
func Compare(baseline, current []Metrics) Comparison {
	c := Comparison{Baseline: Aggregate(baseline), Current: Aggregate(current)}
	for _, m := range comparedMetrics() {
		b, cur := m.Value(c.Baseline), m.Value(c.Current)
		d := Delta{
			Name: m.Name, Unit: m.Unit,
			Baseline: b, Current: cur,
			HigherIsBetter: m.HigherIsBetter,
			Known:          b >= 0 && cur >= 0,
		}
		if d.Known {
			d.Change = cur - b
		}
		c.Deltas = append(c.Deltas, d)
	}
	return c
}

// Render prints the comparison as a Markdown table.
func (c Comparison) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Metrics: %d baseline run(s) → %d current run(s)\n\n", c.Baseline.Runs, c.Current.Runs)
	b.WriteString("| Metric | Baseline | Current | Change |\n")
	b.WriteString("|---|---:|---:|---:|\n")
	for _, d := range c.Deltas {
		if !d.Known {
			fmt.Fprintf(&b, "| %s | – | – | no data |\n", d.Name)
			continue
		}
		marker := ""
		switch {
		case d.Better():
			marker = " ✅"
		case d.Worse():
			marker = " ⚠️"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s%s |\n",
			d.Name, format(d.Baseline, d.Unit), format(d.Current, d.Unit), formatChange(d.Change, d.Unit), marker)
	}
	b.WriteString("\n")
	if c.Baseline.Runs == 0 || c.Current.Runs == 0 {
		b.WriteString("_Not enough runs on one side to compare._\n")
		return b.String()
	}
	if c.Improved() {
		b.WriteString("**Verdict: improved.**\n")
	} else {
		b.WriteString("**Verdict: no net improvement.**\n")
	}
	return b.String()
}

func format(v float64, unit string) string {
	if v < 0 {
		return "–"
	}
	switch unit {
	case "%":
		return fmt.Sprintf("%.1f%%", v*100)
	case "s":
		return fmt.Sprintf("%.1fs", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

func formatChange(v float64, unit string) string {
	if math.Abs(v) < 1e-9 {
		return "0"
	}
	sign := "+"
	if v < 0 {
		sign = "−"
		v = -v
	}
	switch unit {
	case "%":
		return fmt.Sprintf("%s%.1f pp", sign, v*100)
	case "s":
		return fmt.Sprintf("%s%.1fs", sign, v)
	default:
		return fmt.Sprintf("%s%.2f", sign, v)
	}
}

// Render prints one summary as a compact Markdown block.
func (s Summary) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d run(s), %d task(s)\n", s.Runs, s.Tasks)
	rows := []struct {
		name string
		val  float64
		unit string
	}{
		{"task pass rate", ratio(s.TasksPassed, s.Tasks), "%"},
		{"edit-format apply rate", ratio(s.EditsApplied, s.EditsAttempted), "%"},
		{"first-attempt edit rate", ratio(s.EditsFirstAttempt, s.EditsAttempted), "%"},
		{"gate pass rate", ratio(s.GatesPassed, s.GatesRun), "%"},
		{"tool error rate", ratio(s.ToolErrors, s.ToolCalls), "%"},
		{"redundant-call rate", ratio(s.RedundantCalls, s.ToolCalls), "%"},
		{"repair-rule hit rate", ratio(s.RepairHits, s.Failures), "%"},
		{"fixed from memory", ratio(s.ResolvedFromMemory, s.ResolvedFromMemory+s.ResolvedFromLLM), "%"},
		{"LLM calls per task", per(s.LLMCalls, s.Tasks), ""},
		{"tokens per task", per(s.TokensIn+s.TokensOut, s.Tasks), ""},
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "- %-24s %s\n", r.name, format(r.val, r.unit))
	}
	return b.String()
}
