package evolve

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/eval/metrics"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// fakeModel is a stand-in small model with two habits the harness must learn
// to work around:
//
//  1. it copies ws_read's line-number gutter into old_str, so every first edit
//     attempt fails;
//  2. it produces a fresh, correct answer only after an expensive round-trip.
//
// Each llmRoundTrip is one call we are trying to eliminate.
type fakeModel struct{ roundTrips int }

func (m *fakeModel) firstEditArgs() string {
	return `{"path":"pkg/a/b.go","old_str":"   42| if err != nil {","new_str":"if err == nil {"}`
}

// applyEdit returns whether the workspace would accept these args, and the
// error message it would produce otherwise.
func applyEdit(args string) (bool, string) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return false, "failed to parse tool arguments: invalid JSON"
	}
	old, _ := parsed["old_str"].(string)
	if strings.Contains(old, "|") {
		return false, "Edit refused — old_str still contains ws_read's line-number prefix (like `   42|`)."
	}
	return true, ""
}

// runOnce simulates a single task: try the edit, and on failure ask the engine
// for a repair before falling back to another model round-trip.
func runOnce(t *testing.T, eng *Engine, m *fakeModel, runID string) RunReport {
	t.Helper()
	start := time.Now()
	report := RunReport{
		RunID: runID, StartedAt: start,
		Query: "guard the nil error branch", Language: "go", Model: "qwen2.5-coder:14b",
		PlannedTasks: 1, EditFormat: "search_replace",
	}

	args := m.firstEditArgs()
	m.roundTrips++ // producing the first attempt always costs one call
	report.LLMCalls++
	report.EditsAttempted++

	ok, errMsg := applyEdit(args)
	for attempt := 0; !ok && attempt < 3; attempt++ {
		sig := Signal{Tool: "ws_edit", Language: "go", Model: "qwen2.5-coder:14b", Message: errMsg}
		adv := eng.OnFailure(sig, args)

		ev := FailureEvent{Signal: sig, Attempts: attempt + 1}
		switch {
		case adv.Found && adv.Apply && adv.NewArgs != "":
			// The whole point: repaired deterministically, no model call.
			args = adv.NewArgs
			ev.RuleID = adv.RuleID
			ev.ResolvedBy = "rule:" + adv.RuleID
			ev.Resolution = adv.Repair.String()
		default:
			// Fall back to an expensive round-trip that fixes it by hand.
			m.roundTrips++
			report.LLMCalls++
			args = strings.ReplaceAll(args, "   42| ", "")
			ev.ResolvedBy = "llm"
			ev.Resolution = "model rewrote old_str without the gutter"
			ev.Repair = &Repair{
				Kind: RepairTransformArgs, Transform: TransformStripLineNumbers, Retry: true,
				Guidance: "strip ws_read's line-number gutter from old_str before editing",
			}
		}
		report.Failures = append(report.Failures, ev)
		report.EditsAttempted++
		ok, errMsg = applyEdit(args)
		eng.Resolved(adv.Fingerprint, ev.RuleID, ev.Resolution)
	}
	if ok {
		report.EditsApplied = 1
		report.CompletedTasks = 1
		report.FilesChanged = []string{"pkg/a/b.go"}
		report.Gates = []GateResult{{Name: "go build", Passed: true}}
	}
	report.EndedAt = time.Now()
	report.Decisions = []DecisionRecord{{
		Key: Key{Decision: DecEditFormat, ModelFamily: "qwen2.5-coder", Language: "go"},
		Arm: "search_replace",
		Outcome: Outcome{
			Applied: ok, GateRan: ok, GatePassed: ok, Retries: len(report.Failures),
		},
	}}
	return report
}

// TestHarnessFailsOnceThenRepairsItself is the headline claim of pkg/evolve.
// With the shipped rule set, the very first occurrence of a KNOWN failure mode
// is repaired without a model call; and a failure mode that is NOT shipped is
// learned after one occurrence and repaired for free from then on.
func TestHarnessFailsOnceThenRepairsItself(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	eng, err := OpenWith(proj, user, EngineOptions{Deterministic: true, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	eng.Memory().SetRunContext(memory.RunContext{
		Query: "guard the nil error branch", Language: "go", Model: "qwen2.5-coder:14b",
	})

	m := &fakeModel{}
	var perRun []int
	for i := 0; i < 5; i++ {
		before := m.roundTrips
		rep := runOnce(t, eng, m, "run_"+itoa(i))
		perRun = append(perRun, m.roundTrips-before)

		if rep.EditsApplied != 1 {
			t.Fatalf("run %d never landed the edit", i)
		}
		if _, err := eng.Finish(context.Background(), rep, nil); err != nil {
			t.Fatalf("Finish: %v", err)
		}
	}
	for i, calls := range perRun {
		if calls != 1 {
			t.Errorf("run %d cost %d model calls; the seeded rule should have made it 1 (%v)", i, calls, perRun)
		}
	}
}

// The same scenario with the shipped rules removed: the harness must LEARN the
// repair from its first failure and be free from then on.
func TestHarnessLearnsAnUnseenFailureAfterOneOccurrence(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	eng, err := OpenWith(proj, user, EngineOptions{
		Deterministic: true, Seed: 1, NoSeedRules: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng.Close() }()
	eng.Memory().SetRunContext(memory.RunContext{Language: "go", Model: "qwen2.5-coder:14b"})

	m := &fakeModel{}
	var perRun []int
	for i := 0; i < 6; i++ {
		before := m.roundTrips
		rep := runOnce(t, eng, m, "run_"+itoa(i))
		perRun = append(perRun, m.roundTrips-before)
		if _, err := eng.Finish(context.Background(), rep, nil); err != nil {
			t.Fatalf("Finish: %v", err)
		}
	}
	if perRun[0] != 2 {
		t.Fatalf("the first, never-seen failure should have cost an extra round-trip: %v", perRun)
	}
	if perRun[len(perRun)-1] != 1 {
		t.Fatalf("the harness never learned to repair itself: %v", perRun)
	}
	for i := 1; i < len(perRun); i++ {
		if perRun[i] > perRun[i-1] {
			t.Fatalf("cost regressed at run %d: %v", i, perRun)
		}
	}

	// And the learned rule is on disk, readable and applicable.
	rules, _ := OpenRulesWith(proj, user, RulesOptions{NoSeed: true})
	adv, ok := rules.Lookup(Signal{
		Tool: "ws_edit", Language: "go", Model: "qwen2.5-coder:14b",
		Message: "Edit refused — old_str still contains ws_read's line-number prefix",
	})
	if !ok {
		t.Fatal("the learned rule did not survive a reload")
	}
	if !adv.Apply {
		t.Errorf("the learned rule never earned enough confidence to apply: %.2f", adv.Confidence)
	}
	if adv.Rule.Repair.Transform != TransformStripLineNumbers {
		t.Errorf("wrong repair learned: %+v", adv.Rule.Repair)
	}
}

// Everything the subsystem writes must be plain, inspectable files, and
// deleting them must leave the harness working.
func TestOnDiskLayoutIsInspectableAndDeletable(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	eng, err := OpenWith(proj, user, EngineOptions{Deterministic: true})
	if err != nil {
		t.Fatal(err)
	}
	eng.Memory().SetRunContext(memory.RunContext{Language: "go", Model: "qwen2.5-coder:14b"})
	m := &fakeModel{}
	rep := runOnce(t, eng, m, "run_0")
	if _, err := eng.Finish(context.Background(), rep, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}

	wantProject := []string{
		".slmcode/memory/episodes.jsonl",
		".slmcode/memory/episodes.index.json",
		".slmcode/memory/facts.json",
		".slmcode/memory/SEMANTIC.md",
		".slmcode/memory/REFLECTION.md",
		".slmcode/evolve/rules.json",
	}
	for _, rel := range wantProject {
		p := filepath.Join(proj, filepath.FromSlash(rel))
		data, err := os.ReadFile(p) //nolint:gosec
		if err != nil {
			t.Errorf("%s missing: %v", rel, err)
			continue
		}
		if strings.HasSuffix(rel, ".json") {
			var v any
			if err := json.Unmarshal(data, &v); err != nil {
				t.Errorf("%s is not readable JSON: %v", rel, err)
			}
		}
		if strings.HasSuffix(rel, ".jsonl") {
			for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var v any
				if err := json.Unmarshal([]byte(line), &v); err != nil {
					t.Errorf("%s line %d is not readable JSON: %v", rel, i, err)
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(user, ".slmcode", "evolve", "policy.json")); err != nil {
		t.Errorf("user policy missing: %v", err)
	}

	// rm -rf both state directories, then keep working.
	if err := os.RemoveAll(filepath.Join(proj, ".slmcode")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(user, ".slmcode")); err != nil {
		t.Fatal(err)
	}
	eng2, err := OpenWith(proj, user, EngineOptions{Deterministic: true})
	if err != nil {
		t.Fatalf("reopening after rm -rf failed: %v", err)
	}
	defer func() { _ = eng2.Close() }()
	if eng2.Rules().Count() != len(SeedRules()) {
		t.Errorf("after a wipe the store has %d rules, want the %d seeds", eng2.Rules().Count(), len(SeedRules()))
	}
	rep2 := runOnce(t, eng2, m, "run_after_wipe")
	if rep2.EditsApplied != 1 {
		t.Error("the harness stopped working after its state was deleted")
	}
}

func TestEngineForgetResetsEverything(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	eng, _ := OpenWith(proj, user, EngineOptions{Deterministic: true})
	eng.Memory().SetRunContext(memory.RunContext{Language: "go", Model: "m"})
	m := &fakeModel{}
	rep := runOnce(t, eng, m, "r0")
	if _, err := eng.Finish(context.Background(), rep, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.Forget(memory.ScopeAll); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if eng.Memory().Episodes().Count() != 0 {
		t.Error("episodes survived Forget")
	}
	if eng.Rules().Count() != len(SeedRules()) {
		t.Error("learned rules survived Forget")
	}
	if len(eng.Bandit().Snapshot()) != 0 {
		t.Error("policy survived Forget")
	}
	if eng.Regressions().Count() != 0 {
		t.Error("regression checks survived Forget")
	}
}

func TestEngineIsNilSafe(t *testing.T) {
	var eng *Engine
	if adv := eng.OnFailure(Signal{Message: "x"}, ""); adv.Found {
		t.Error("nil engine found advice")
	}
	eng.Resolved(Fingerprint{}, "", "")
	// A nil engine must still hand back a usable default rather than nothing.
	if c := eng.Choose(DecEditFormat, "a", "b"); c.Arm != "a" {
		t.Errorf("nil engine chose %q, want the first offered arm", c.Arm)
	}
	if c := eng.Choose(DecEditFormat); c.Arm != "" {
		t.Errorf("nil engine with no arms chose %q", c.Arm)
	}
	if eng.Why(DecEditFormat) == "" {
		t.Error("nil engine Why should still say something")
	}
	if _, err := eng.Finish(context.Background(), RunReport{}, nil); err != nil {
		t.Errorf("nil engine Finish: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Errorf("nil engine Close: %v", err)
	}
	if err := eng.Forget(memory.ScopeAll); err != nil {
		t.Errorf("nil engine Forget: %v", err)
	}
	if eng.Memory() != nil || eng.Rules() != nil || eng.Bandit() != nil || eng.Regressions() != nil {
		t.Error("nil engine returned non-nil stores")
	}
}

// Memory must never make things worse: the injected block stays inside its
// budget no matter how much history accumulates.
func TestInjectedContextStaysBounded(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	eng, _ := OpenWith(proj, user, EngineOptions{Deterministic: true})
	defer func() { _ = eng.Close() }()
	eng.Memory().SetRunContext(memory.RunContext{
		Query: "guard the nil error branch", Language: "go", Model: "qwen2.5-coder:14b", Role: "worker",
	})
	m := &fakeModel{}
	const budget = 600
	for i := 0; i < 40; i++ {
		rep := runOnce(t, eng, m, "run_"+itoa(i))
		if _, err := eng.Finish(context.Background(), rep, nil); err != nil {
			t.Fatal(err)
		}
		block := eng.Memory().RenderForPrompt("worker", budget)
		if n := len(block) / 4; n > budget+50 {
			t.Fatalf("run %d: injected block is roughly %d tokens, budget %d", i, n, budget)
		}
	}
	if got := eng.Rules().Count(); got > MaxRules {
		t.Errorf("rule store grew to %d", got)
	}
	if got := eng.Memory().Episodes().Count(); got > memory.DefaultMaxEpisodes {
		t.Errorf("episode store grew to %d", got)
	}
}

// The hot path must be cheap: a failure lookup happens on every tool error.
func TestLookupIsCheap(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector's instrumentation makes wall-clock budgets meaningless")
	}
	r, _ := OpenRules(t.TempDir(), t.TempDir())
	sig := Signal{Tool: "ws_edit", Language: "go", Model: "qwen2.5-coder:14b",
		Message: "old_str not found in pkg/loop/runner.go.\nClosest text already in the file."}
	start := time.Now()
	const n = 2000
	for i := 0; i < n; i++ {
		r.Lookup(sig)
	}
	per := time.Since(start) / n
	if per > 500*time.Microsecond {
		t.Errorf("Lookup took %v per call; the hot-path budget is a few hundred microseconds", per)
	}
}

// The repair store must satisfy the offline replay harness's Repairer
// interface, and replaying real fixtures through it must show a measurable
// improvement — this is the link between "the harness learns" and "we can
// prove it".
func TestRulesDriveTheOfflineABHarness(t *testing.T) {
	rules, err := OpenRules(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var repairer metrics.Repairer = rules // compile-time contract check

	bad := `{"path":"a.go","old_str":"   42| if err != nil {","new_str":"if err == nil {"}`
	good := `{"path":"a.go","old_str":"if err != nil {","new_str":"if err == nil {"}`
	var fixtures []metrics.Trajectory
	for i := 0; i < 4; i++ {
		fixtures = append(fixtures, metrics.Trajectory{
			ID: "fx" + itoa(i), Language: "go", Model: "qwen2.5-coder:14b",
			EditFormat: "search_replace", TaskPassed: true,
			Steps: []metrics.Step{
				{Kind: metrics.StepAssistant},
				{Kind: metrics.StepTool, Tool: "ws_read", Args: `{"path":"a.go"}`, OK: true},
				{
					Kind: metrics.StepTool, Tool: "ws_edit", Args: bad, EditAttempt: true,
					Error:     "Edit refused — old_str still contains ws_read's line-number prefix (like `   42|`).",
					FixedArgs: good,
				},
			},
		})
	}

	c := metrics.ABTest(fixtures, repairer, "qwen2.5-coder")
	if !c.Improved() {
		t.Fatalf("the shipped rules produced no measurable improvement:\n%s", c.Render())
	}
	if c.Current.ResolvedFromMemory != len(fixtures) {
		t.Errorf("resolved from memory = %d, want %d", c.Current.ResolvedFromMemory, len(fixtures))
	}
	if c.Current.LLMCalls >= c.Baseline.LLMCalls {
		t.Errorf("no round-trips saved: %d → %d", c.Baseline.LLMCalls, c.Current.LLMCalls)
	}
}

// MetricsFor must faithfully project a run report onto the metrics record.
func TestMetricsForProjection(t *testing.T) {
	r := sampleReport()
	ref := Reflect(r)
	m := MetricsFor(r, ref)
	if m.Tasks != 2 || m.TasksPassed != 2 {
		t.Errorf("tasks = %d/%d", m.TasksPassed, m.Tasks)
	}
	if m.EditsAttempted != 8 || m.EditsApplied != 7 {
		t.Errorf("edits = %d/%d", m.EditsApplied, m.EditsAttempted)
	}
	if got := m.EditApplyRate(); got < 0.87 || got > 0.88 {
		t.Errorf("apply rate = %.3f", got)
	}
	if m.Failures != 3 || m.ResolvedFromMemory != 1 || m.ResolvedFromLLM != 1 || m.Unresolved != 1 {
		t.Errorf("failure accounting = %+v", m)
	}
	if m.RepairHits != 1 {
		t.Errorf("repair hits = %d, want 1", m.RepairHits)
	}
	if len(m.Gates) != 2 || m.GatePassRate() != 1 {
		t.Errorf("gates = %+v", m.Gates)
	}
	if m.WallMS != 90000 {
		t.Errorf("wall = %d", m.WallMS)
	}
}

func TestRecordMetricsWritesTheLog(t *testing.T) {
	proj := t.TempDir()
	r := sampleReport()
	ref := Reflect(r)
	if err := RecordMetrics(proj, r, ref); err != nil {
		t.Fatal(err)
	}
	got, err := metrics.LoadFrom(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RunID != "run_1" {
		t.Fatalf("metrics log = %+v", got)
	}
	if err := RecordMetrics("", r, ref); err != nil {
		t.Errorf("RecordMetrics with no project dir should be a no-op: %v", err)
	}
}
