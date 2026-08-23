package eval

import (
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

func TestOfflineCasesLoad(t *testing.T) {
	cases, err := OfflineCases()
	if err != nil {
		t.Fatalf("OfflineCases: %v", err)
	}
	if len(cases) < 2 {
		t.Fatalf("only %d fixtures embedded", len(cases))
	}
	for _, c := range cases {
		if c.ID == "" {
			t.Fatal("fixture with no id")
		}
		if len(c.Trajectory.Steps) == 0 {
			t.Fatalf("%s has no steps", c.ID)
		}
		if c.Trajectory.Language == "" {
			t.Fatalf("%s has no language — repair lookup is language-scoped", c.ID)
		}
	}
}

// TestOfflineEvalProvesTheRepairLadderHelps is the whole point of item 10: an
// improvement has to be provable, and proving it must not need a live model.
//
// Identical recordings, one variable (the repair store). The metrics that move
// are exactly the ones the repair ladder is supposed to move.
func TestOfflineEvalProvesTheRepairLadderHelps(t *testing.T) {
	rep, err := RunOffline(OfflineOptions{ModelFamily: "qwen2.5-coder"})
	if err != nil {
		t.Fatalf("RunOffline: %v", err)
	}
	base := metrics.Aggregate(rep.Baseline)
	cur := metrics.Aggregate(rep.Current)

	if base.Failures == 0 {
		t.Fatal("the fixtures record no failures — there is nothing to repair")
	}
	if base.Failures != cur.Failures {
		t.Fatalf("both arms must see the same failures (%d vs %d) — the A/B has more than one variable",
			base.Failures, cur.Failures)
	}
	if cur.RepairHits == 0 {
		t.Fatal("the repair store matched nothing; the ladder is not being exercised")
	}
	if base.RepairHits != 0 {
		t.Fatal("the baseline arm must have no repair store")
	}
	if cur.ResolvedFromMemory == 0 {
		t.Fatal("no failure was fixed deterministically — the line-number rung did not fire")
	}
	if cur.LLMCalls >= base.LLMCalls {
		t.Fatalf("repairs must SAVE round-trips: baseline %d, current %d", base.LLMCalls, cur.LLMCalls)
	}
	if cur.Unresolved != base.Unresolved {
		t.Fatalf("the control fixture must stay unresolved in both arms (%d vs %d)",
			base.Unresolved, cur.Unresolved)
	}
	if !rep.Improved() {
		t.Fatalf("Compare must report an improvement:\n%s", rep.Render())
	}
	if !strings.Contains(rep.Render(), "repair-ladder-go") {
		t.Fatalf("the rendered report must name the fixtures:\n%s", rep.Render())
	}
}

// TestOfflineEvalCoversTheEditFormatFallback pins the second behavior the
// fixtures exist for: a failed diff hunk must be recognized and must not be
// scored as a landed edit.
func TestOfflineEvalCoversTheEditFormatFallback(t *testing.T) {
	cases, err := OfflineCases()
	if err != nil {
		t.Fatal(err)
	}
	var fallback OfflineCase
	for _, c := range cases {
		if c.ID == "edit-format-fallback-py" {
			fallback = c
		}
	}
	if fallback.ID == "" {
		t.Fatal("the edit-format fallback fixture is missing")
	}
	if fallback.Trajectory.EditFormat != "diff" {
		t.Fatalf("the fallback fixture must start in the diff format, got %q", fallback.Trajectory.EditFormat)
	}

	rules, err := evolve.OpenRules(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := metrics.Replay(fallback.Trajectory, metrics.ReplayOptions{
		Repairer: rules, ModelFamily: "qwen2.5-coder", At: time.Unix(0, 0).UTC(),
	})
	// The second hunk was never made to work — the model abandoned the diff
	// format — so the apply rate must be below 1. (A failure whose recorded
	// retry DID work still counts as applied: Replay scores the outcome, and
	// the cost of getting there is what the repair store changes.)
	if m.EditsAttempted <= m.EditsApplied {
		t.Fatalf("a fixture with an abandoned hunk must record more attempts (%d) than applies (%d)",
			m.EditsAttempted, m.EditsApplied)
	}
	if m.EditApplyRate() >= 1 {
		t.Fatalf("edit-apply rate = %.2f; an abandoned diff hunk is being scored as applied", m.EditApplyRate())
	}
	if m.Unresolved == 0 {
		t.Fatal("the abandoned hunk must be recorded as unresolved")
	}
	if m.RepairHits == 0 {
		t.Fatal("the patch-failed rung must match — that rule is what switches the edit format")
	}
	if m.RedundantCalls == 0 {
		t.Fatal("the fixture repeats the same failing hunk; that must show as a redundant call")
	}
}

// TestOfflineEvalIsDeterministic: an A/B that moves between runs cannot support
// a claim about a change.
func TestOfflineEvalIsDeterministic(t *testing.T) {
	first, err := RunOffline(OfflineOptions{ModelFamily: "qwen2.5-coder"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunOffline(OfflineOptions{ModelFamily: "qwen2.5-coder"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Render() != second.Render() {
		t.Fatalf("offline replay is not deterministic:\n--- first ---\n%s\n--- second ---\n%s",
			first.Render(), second.Render())
	}
}

// TestEvalReportEmitsComparableMetrics covers the live half: every case carries
// a metrics record, and two reports compare.
func TestEvalReportEmitsComparableMetrics(t *testing.T) {
	baseline := Report{Results: []Result{
		{ID: "a", OK: false, Metrics: metrics.Metrics{
			Tasks: 2, TasksPassed: 1, EditsAttempted: 4, EditsApplied: 2,
			ToolCalls: 10, ToolErrors: 4, LLMCalls: 12,
		}},
	}}
	current := Report{Results: []Result{
		{ID: "a", OK: true, Metrics: metrics.Metrics{
			Tasks: 2, TasksPassed: 2, EditsAttempted: 4, EditsApplied: 4,
			ToolCalls: 10, ToolErrors: 1, LLMCalls: 8,
		}},
	}}
	if got := len(current.Metrics()); got != 1 {
		t.Fatalf("Metrics() returned %d records", got)
	}
	if s := current.Summary(); s.Tasks != 2 || s.TasksPassed != 2 {
		t.Fatalf("Summary = %+v", s)
	}
	cmp := current.CompareTo(baseline)
	if !cmp.Improved() {
		t.Fatalf("a run with a higher apply rate, fewer tool errors and fewer LLM calls must compare as improved:\n%s",
			cmp.Render())
	}
	for _, want := range []string{"edit-format apply rate", "tool error rate", "LLM calls per task"} {
		if !strings.Contains(cmp.Render(), want) {
			t.Fatalf("comparison must report %q:\n%s", want, cmp.Render())
		}
	}
}

// TestMetricsCollectorFromEventStream pins the derivation rules, including the
// ones that are lower bounds.
func TestMetricsCollectorFromEventStream(t *testing.T) {
	c := newMetricsCollector()
	events := []orchestrator.Event{
		{Kind: stream.KindAgentStart, Agent: "worker"},
		{Kind: stream.KindFileChange, Agent: "worker", Message: "edit a.go", Scope: "a.go"},
		{Kind: stream.KindIntervention, Agent: "harness", Scope: "loop",
			Message: "repeated the same ws_edit call", Output: "repeated_tool_call"},
		{Kind: stream.KindIntervention, Agent: "harness", Scope: "malformed_args",
			Message: "arguments for ws_patch were malformed"},
		{Kind: stream.KindAgentEnd, Agent: "reviewer", Level: stream.LevelProblem, Message: "approved=false"},
		{Kind: stream.KindAgentEnd, Agent: "worker", Level: stream.LevelSuccess, Message: "worker finished"},
		{Kind: stream.KindPhase, Message: "noise that must not count"},
	}
	for _, e := range events {
		c.observe(e)
	}
	res := Result{ID: "case", TasksTotal: 1, TasksDone: 1, FilesOK: true, OK: true,
		Duration: 1500 * time.Millisecond}
	m := c.snapshot(res, nil, "qwen2.5-coder:7b", "ollama", time.Unix(0, 0).UTC())

	if m.EditsApplied != 1 {
		t.Fatalf("EditsApplied = %d, want 1 (one file_change)", m.EditsApplied)
	}
	if m.EditsAttempted < m.EditsApplied {
		t.Fatalf("attempts (%d) must never be below applies (%d)", m.EditsAttempted, m.EditsApplied)
	}
	if m.RedundantCalls != 1 {
		t.Fatalf("RedundantCalls = %d, want 1 (the loop intervention)", m.RedundantCalls)
	}
	if m.ToolErrors < 2 {
		t.Fatalf("ToolErrors = %d, want at least 2", m.ToolErrors)
	}
	if m.LLMCalls != 1 {
		t.Fatalf("LLMCalls = %d, want 1 (one agent_start)", m.LLMCalls)
	}
	if m.WallMS != 1500 {
		t.Fatalf("WallMS = %d, want 1500", m.WallMS)
	}
	if m.Tasks != 1 || m.TasksPassed != 1 {
		t.Fatalf("tasks = %d/%d", m.TasksPassed, m.Tasks)
	}
	var sawFailingGate bool
	for _, g := range m.Gates {
		if !g.Passed {
			sawFailingGate = true
		}
	}
	if !sawFailingGate {
		t.Fatal("a problem-level agent_end must be recorded as a failed gate")
	}
}
