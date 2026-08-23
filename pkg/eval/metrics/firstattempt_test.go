package metrics

import (
	"path/filepath"
	"testing"
)

// fixturePath resolves a shipped trajectory fixture. The fixtures live one
// package up (pkg/eval embeds them for the offline eval); reading them from
// here keeps this test on the SAME recording the eval harness replays, which is
// the only way the two numbers can be compared.
func fixturePath(name string) string {
	return filepath.Join("..", "fixtures", name)
}

// This is the fixture the package doc's distinction is about: a 7B emits a
// unified diff twice, both hunks fail to apply, and only the third attempt —
// a search/replace edit — lands. Every edit eventually accounted for as
// "applied" would say the model's edit format worked. It did not.
func TestFirstAttemptRateDivergesFromApplyRateOnTheFallbackFixture(t *testing.T) {
	tr, err := LoadTrajectory(fixturePath("edit-format-fallback.json"))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if tr.ID != "edit-format-fallback-py" {
		t.Fatalf("wrong fixture: %q", tr.ID)
	}

	m := Replay(tr, ReplayOptions{})

	// Three edit attempts: ws_patch, ws_patch, ws_edit.
	if m.EditsAttempted != 3 {
		t.Fatalf("edits attempted = %d, want 3", m.EditsAttempted)
	}
	// Eventual applies: the first ws_patch is recovered by its recorded
	// fixed_args, and the ws_edit applied outright. The second ws_patch never
	// recovered.
	if m.EditsApplied != 2 {
		t.Errorf("edits applied = %d, want 2", m.EditsApplied)
	}
	// First-attempt applies: ONLY the ws_edit. This is the number that says
	// "this model cannot emit a unified diff for this file".
	if m.EditsFirstAttempt != 1 {
		t.Errorf("first-attempt applies = %d, want 1", m.EditsFirstAttempt)
	}

	apply, first := m.EditApplyRate(), m.FirstAttemptApplyRate()
	if apply <= first {
		t.Fatalf("the two metrics did not diverge: apply=%.3f first-attempt=%.3f — "+
			"a first-attempt metric that tracks the eventual one measures nothing new",
			apply, first)
	}
	if got, want := apply, 2.0/3.0; !closeTo(got, want) {
		t.Errorf("apply rate = %.4f, want %.4f", got, want)
	}
	if got, want := first, 1.0/3.0; !closeTo(got, want) {
		t.Errorf("first-attempt rate = %.4f, want %.4f", got, want)
	}
	// The gap IS the harness's recovery work, and it is reported as such.
	if got, want := m.EditRepairRate(), 1.0/3.0; !closeTo(got, want) {
		t.Errorf("edit repair rate = %.4f, want %.4f", got, want)
	}
}

// The first-attempt metric must be a property of the RECORDING, not of the
// repair store: adding repairs saves round-trips, it cannot make a model emit a
// better diff. If a repair could move this number the A/B would be measuring
// the harness and calling it the model.
func TestFirstAttemptRateIsIndependentOfTheRepairStore(t *testing.T) {
	tr, err := LoadTrajectory(fixturePath("edit-format-fallback.json"))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	baseline := Replay(tr, ReplayOptions{Label: "baseline"})
	repaired := Replay(tr, ReplayOptions{
		Repairer: stubRepairerFunc(func(_, _, _, _, args string) (string, string, bool) {
			// A repairer that "fixes" everything by echoing the recorded args.
			return "try this", args, true
		}),
		Label: "with-repairs",
	})
	if baseline.EditsFirstAttempt != repaired.EditsFirstAttempt {
		t.Errorf("a repair store changed the first-attempt count: %d → %d",
			baseline.EditsFirstAttempt, repaired.EditsFirstAttempt)
	}
	if baseline.EditsAttempted != repaired.EditsAttempted {
		t.Errorf("attempt count moved: %d → %d", baseline.EditsAttempted, repaired.EditsAttempted)
	}
}

// The trajectory that never needed a repair must report the two rates equal —
// otherwise the new metric would be penalizing a compliant model.
func TestFirstAttemptRateEqualsApplyRateWhenNothingNeededRepair(t *testing.T) {
	tr := Trajectory{
		ID: "clean", TaskPassed: true,
		Steps: []Step{
			{Kind: StepAssistant},
			{Kind: StepTool, Tool: "ws_edit", Args: `{"a":1}`, OK: true, EditAttempt: true},
			{Kind: StepTool, Tool: "ws_edit", Args: `{"a":2}`, OK: true, EditAttempt: true},
		},
	}
	m := Replay(tr, ReplayOptions{})
	if m.EditsAttempted != 2 || m.EditsApplied != 2 || m.EditsFirstAttempt != 2 {
		t.Fatalf("clean trajectory scored %d/%d/%d", m.EditsFirstAttempt, m.EditsApplied, m.EditsAttempted)
	}
	if m.EditApplyRate() != m.FirstAttemptApplyRate() {
		t.Error("a fully compliant run must score the same on both rates")
	}
	if m.EditRepairRate() != 0 {
		t.Errorf("repair rate = %v, want 0", m.EditRepairRate())
	}
}

// Aggregate pools it, Compare ranks it, and Render prints it.
func TestFirstAttemptMetricIsThreadedThroughAggregateCompareAndRender(t *testing.T) {
	tr, err := LoadTrajectory(fixturePath("edit-format-fallback.json"))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	runs := ReplayAll([]Trajectory{tr, tr, tr}, ReplayOptions{})
	s := Aggregate(runs)
	if s.EditsFirstAttempt != 3 || s.EditsApplied != 6 || s.EditsAttempted != 9 {
		t.Fatalf("aggregate = first=%d applied=%d attempted=%d",
			s.EditsFirstAttempt, s.EditsApplied, s.EditsAttempted)
	}
	if !containsSub(s.Render(), "first-attempt edit rate") {
		t.Errorf("Summary.Render omits the first-attempt rate:\n%s", s.Render())
	}

	// A "better" arm that only lifts the eventual apply rate must NOT be
	// credited with better format compliance.
	better := make([]Metrics, len(runs))
	copy(better, runs)
	for i := range better {
		better[i].EditsApplied = 3 // the harness recovered everything
	}
	c := Compare(runs, better)
	byName := map[string]Delta{}
	for _, d := range c.Deltas {
		byName[d.Name] = d
	}
	apply, ok := byName["edit-format apply rate"]
	if !ok {
		t.Fatal("apply-rate delta missing")
	}
	first, ok := byName["first-attempt edit-format rate"]
	if !ok {
		t.Fatal("first-attempt delta missing — Compare does not report it")
	}
	if !apply.Better() {
		t.Error("the apply rate should have improved")
	}
	if first.Better() || first.Change != 0 {
		t.Errorf("recovering more edits was credited as better format compliance: %+v", first)
	}
	if !containsSub(c.Render(), "first-attempt edit-format rate") {
		t.Errorf("Comparison.Render omits the first-attempt rate:\n%s", c.Render())
	}
}

// Normalize must not let the new counter tell a story the others contradict.
func TestNormalizeBoundsFirstAttempt(t *testing.T) {
	m := Metrics{EditsAttempted: 2, EditsApplied: 1, EditsFirstAttempt: 5}
	m.Normalize(m.At)
	if m.EditsFirstAttempt != 1 {
		t.Errorf("first-attempt = %d, want clamped to applied (1)", m.EditsFirstAttempt)
	}
	neg := Metrics{EditsFirstAttempt: -3}
	neg.Normalize(neg.At)
	if neg.EditsFirstAttempt != 0 {
		t.Errorf("negative first-attempt = %d", neg.EditsFirstAttempt)
	}
	// No edits at all: "no data", not zero percent.
	empty := Metrics{}
	if empty.FirstAttemptApplyRate() != -1 {
		t.Errorf("empty first-attempt rate = %v, want -1 (no data)", empty.FirstAttemptApplyRate())
	}
}

func closeTo(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

func containsSub(hay, needle string) bool {
	return len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
