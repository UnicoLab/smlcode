package metrics

import (
	"strings"
	"testing"
)

func runs(n int, apply, pass float64) []Metrics {
	var out []Metrics
	for i := 0; i < n; i++ {
		out = append(out, Metrics{
			RunID: "r" + itoa(i),
			Tasks: 10, TasksPassed: int(pass * 10),
			EditsAttempted: 10, EditsApplied: int(apply * 10),
			ToolCalls: 100, ToolErrors: 10, RedundantCalls: 5,
			LLMCalls: 50, TokensIn: 10000, TokensOut: 1000, WallMS: 60000,
			Gates:              []Gate{{Name: "test", Passed: pass > 0.5}},
			Failures:           4,
			RepairHits:         2,
			ResolvedFromMemory: 2, ResolvedFromLLM: 2,
		})
	}
	return out
}

func TestAggregatePoolsRatherThanAverages(t *testing.T) {
	in := []Metrics{
		{Tasks: 1, TasksPassed: 1, EditsAttempted: 1, EditsApplied: 1},
		{Tasks: 99, TasksPassed: 0, EditsAttempted: 99, EditsApplied: 0},
	}
	s := Aggregate(in)
	if s.Runs != 2 || s.Tasks != 100 || s.TasksPassed != 1 {
		t.Fatalf("aggregate = %+v", s)
	}
	// A naive per-run average would report 50%; pooling reports 1%.
	c := Compare(nil, in)
	for _, d := range c.Deltas {
		if d.Name != "task pass rate" {
			continue
		}
		if d.Current > 0.02 {
			t.Errorf("pass rate = %.3f; a small run was allowed to dominate a large one", d.Current)
		}
	}
}

func TestCompareDetectsImprovement(t *testing.T) {
	baseline := runs(5, 0.6, 0.4)
	current := runs(5, 0.95, 0.8)
	c := Compare(baseline, current)
	if !c.Improved() {
		t.Fatalf("a clearly better set of runs was not reported as improved:\n%s", c.Render())
	}
	byName := map[string]Delta{}
	for _, d := range c.Deltas {
		byName[d.Name] = d
	}
	apply := byName["edit-format apply rate"]
	if !apply.Known || !apply.Better() {
		t.Errorf("edit-format apply rate not reported as better: %+v", apply)
	}
	if apply.Change < 0.3 {
		t.Errorf("apply-rate change = %.2f, want ~0.35", apply.Change)
	}
	pass := byName["task pass rate"]
	if !pass.Better() {
		t.Errorf("task pass rate not better: %+v", pass)
	}
}

func TestCompareDetectsRegression(t *testing.T) {
	c := Compare(runs(5, 0.95, 0.9), runs(5, 0.5, 0.3))
	if c.Improved() {
		t.Fatalf("a regression was reported as an improvement:\n%s", c.Render())
	}
	out := c.Render()
	if !strings.Contains(out, "no net improvement") {
		t.Errorf("verdict missing:\n%s", out)
	}
	if !strings.Contains(out, "⚠️") {
		t.Errorf("regressions not marked:\n%s", out)
	}
}

func TestCompareDirectionality(t *testing.T) {
	// Fewer LLM calls per task is better; more is worse.
	cheap := []Metrics{{Tasks: 10, LLMCalls: 10}}
	pricey := []Metrics{{Tasks: 10, LLMCalls: 100}}
	c := Compare(pricey, cheap)
	for _, d := range c.Deltas {
		if d.Name != "LLM calls per task" {
			continue
		}
		if !d.Better() {
			t.Errorf("cutting LLM calls was not reported as better: %+v", d)
		}
		if d.HigherIsBetter {
			t.Error("LLM calls per task should be lower-is-better")
		}
	}
}

func TestCompareMissingData(t *testing.T) {
	c := Compare(nil, nil)
	for _, d := range c.Deltas {
		if d.Known {
			t.Errorf("metric %q claimed data from nothing", d.Name)
		}
		if d.Better() || d.Worse() {
			t.Errorf("metric %q claimed a direction with no data", d.Name)
		}
	}
	out := c.Render()
	if !strings.Contains(out, "Not enough runs") {
		t.Errorf("empty comparison should say so:\n%s", out)
	}
	if strings.Contains(out, "NaN") || strings.Contains(out, "Inf") {
		t.Errorf("render leaked a non-finite number:\n%s", out)
	}
}

func TestRenderIsReadable(t *testing.T) {
	c := Compare(runs(3, 0.6, 0.5), runs(3, 0.9, 0.9))
	out := c.Render()
	for _, want := range []string{
		"3 baseline run(s) → 3 current run(s)",
		"| Metric | Baseline | Current | Change |",
		"edit-format apply rate", "task pass rate",
		"failures fixed from memory", "repair-rule hit rate",
		"LLM calls per task", "pp",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	if s := Aggregate(runs(2, 0.5, 0.5)).Render(); !strings.Contains(s, "edit-format apply rate") {
		t.Errorf("summary render missing metrics:\n%s", s)
	}
}
