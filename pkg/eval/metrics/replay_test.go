package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stubRepairer is a minimal Repairer: it knows exactly one repair — strip
// ws_read's line-number gutter out of old_str — which is enough to prove the
// A/B machinery measures what it claims to measure. In production the same
// interface is satisfied by *evolve.Rules.
type stubRepairer struct{ calls int }

var gutter = regexp.MustCompile(`(?m)^[ \t]*\d+[ \t]*\|[ \t]?`)

func (s *stubRepairer) SuggestRepair(tool, message, _, _, args string) (string, string, bool) {
	s.calls++
	if tool != "ws_edit" || !strings.Contains(strings.ToLower(message), "line-number prefix") {
		return "", "", false
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return "", "", false
	}
	old, _ := parsed["old_str"].(string)
	fixed := gutter.ReplaceAllString(old, "")
	if fixed == old {
		return "strip the gutter", "", true
	}
	parsed["old_str"] = fixed
	out, err := json.Marshal(parsed)
	if err != nil {
		return "", "", false
	}
	return "strip the gutter", string(out), true
}

func gutterTrajectory(id string) Trajectory {
	badArgs := `{"path":"a.go","old_str":"   42| if err != nil {","new_str":"if err == nil {"}`
	goodArgs := `{"path":"a.go","old_str":"if err != nil {","new_str":"if err == nil {"}`
	return Trajectory{
		ID: id, Query: "guard the error branch", Language: "go", Model: "qwen2.5-coder:14b",
		EditFormat: "search_replace", TaskPassed: true,
		Steps: []Step{
			{Kind: StepAssistant},
			{Kind: StepTool, Tool: "ws_read", Args: `{"path":"a.go"}`, OK: true},
			{
				Kind: StepTool, Tool: "ws_edit", Args: badArgs, EditAttempt: true,
				Error:     "Edit refused — old_str still contains ws_read's line-number prefix (like `   42|`).",
				FixedArgs: goodArgs,
			},
		},
	}
}

// The offline A/B: identical recorded inputs, one variable (the repair store).
func TestReplayABShowsTheRepairStoreSavesRoundTrips(t *testing.T) {
	var ts []Trajectory
	for i := 0; i < 5; i++ {
		ts = append(ts, gutterTrajectory("traj_"+itoa(i)))
	}

	baseline := ReplayAll(ts, ReplayOptions{Label: "baseline"})
	repaired := ReplayAll(ts, ReplayOptions{Repairer: &stubRepairer{}, Label: "with-repairs"})

	bs, cs := Aggregate(baseline), Aggregate(repaired)
	if bs.LLMCalls <= cs.LLMCalls {
		t.Fatalf("repairs did not save any round-trips: baseline %d vs repaired %d", bs.LLMCalls, cs.LLMCalls)
	}
	if cs.ResolvedFromMemory != 5 {
		t.Errorf("resolved from memory = %d, want 5", cs.ResolvedFromMemory)
	}
	if bs.ResolvedFromMemory != 0 {
		t.Errorf("the baseline claimed %d memory resolutions", bs.ResolvedFromMemory)
	}
	// Both arms must land the same edits — the repair saves cost, it does not
	// change correctness. If it did, the A/B would be measuring two things.
	if bs.EditsApplied != cs.EditsApplied || bs.TasksPassed != cs.TasksPassed {
		t.Errorf("arms disagree on correctness: baseline %d/%d, repaired %d/%d",
			bs.EditsApplied, bs.TasksPassed, cs.EditsApplied, cs.TasksPassed)
	}

	c := Compare(baseline, repaired)
	if !c.Improved() {
		t.Fatalf("the A/B did not register an improvement:\n%s", c.Render())
	}
	byName := map[string]Delta{}
	for _, d := range c.Deltas {
		byName[d.Name] = d
	}
	if d := byName["LLM calls per task"]; !d.Better() {
		t.Errorf("LLM calls per task not improved: %+v", d)
	}
	if d := byName["failures fixed from memory"]; !d.Better() {
		t.Errorf("memory resolution rate not improved: %+v", d)
	}

	// And the convenience wrapper agrees.
	if !ABTest(ts, &stubRepairer{}, "qwen2.5-coder").Improved() {
		t.Error("ABTest disagreed with the manual comparison")
	}
}

func TestReplayCountsTheBasics(t *testing.T) {
	tr := Trajectory{
		ID: "t", TaskPassed: true,
		Steps: []Step{
			{Kind: StepAssistant},
			{Kind: StepTool, Tool: "ws_read", Args: `{"path":"a.go"}`, OK: true},
			{Kind: StepTool, Tool: "ws_read", Args: `{"path":"a.go"}`, OK: true}, // redundant
			{Kind: StepTool, Tool: "ws_edit", Args: `{"a":1}`, OK: true, EditAttempt: true},
			{Kind: StepTool, Tool: "ws_edit", Args: `{"a":2}`, OK: false, Error: "boom", EditAttempt: true},
		},
	}
	m := Replay(tr, ReplayOptions{})
	if m.ToolCalls != 4 {
		t.Errorf("tool calls = %d, want 4", m.ToolCalls)
	}
	if m.RedundantCalls != 1 {
		t.Errorf("redundant = %d, want 1", m.RedundantCalls)
	}
	if m.ToolErrors != 1 || m.Failures != 1 {
		t.Errorf("errors = %d failures = %d", m.ToolErrors, m.Failures)
	}
	if m.EditsAttempted != 2 || m.EditsApplied != 1 {
		t.Errorf("edits = %d/%d, want 1/2", m.EditsApplied, m.EditsAttempted)
	}
	if m.Unresolved != 1 {
		t.Errorf("unresolved = %d, want 1 (no FixedArgs recorded)", m.Unresolved)
	}
	if m.TasksPassed != 0 {
		t.Error("a task with an unresolved failure must not be scored as passed")
	}
}

func TestReplayIsDeterministic(t *testing.T) {
	ts := []Trajectory{gutterTrajectory("a"), gutterTrajectory("b")}
	first := ReplayAll(ts, ReplayOptions{Repairer: &stubRepairer{}})
	second := ReplayAll(ts, ReplayOptions{Repairer: &stubRepairer{}})
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("replay is not deterministic")
	}
}

func TestReplayWithAnUnhelpfulRepairer(t *testing.T) {
	// A repairer that matches but produces nothing useful must not be credited
	// with a memory resolution.
	useless := stubRepairerFunc(func(string, string, string, string, string) (string, string, bool) {
		return "have you tried turning it off and on again", "", true
	})
	m := Replay(gutterTrajectory("t"), ReplayOptions{Repairer: useless})
	if m.RepairHits != 1 {
		t.Errorf("repair hits = %d, want 1", m.RepairHits)
	}
	if m.ResolvedFromMemory != 0 {
		t.Errorf("a repair that changed nothing was credited as a memory fix")
	}
	if m.ResolvedFromLLM != 1 {
		t.Errorf("resolved from LLM = %d, want 1", m.ResolvedFromLLM)
	}
}

type stubRepairerFunc func(tool, message, language, modelFamily, args string) (string, string, bool)

func (f stubRepairerFunc) SuggestRepair(tool, message, language, modelFamily, args string) (string, string, bool) {
	return f(tool, message, language, modelFamily, args)
}

func TestTrajectoryFixtureRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := SaveTrajectory(filepath.Join(dir, "t"+itoa(i)+".json"), gutterTrajectory("t"+itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTrajectories(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("loaded %d trajectories, want 3 (non-JSON and corrupt files skipped)", len(got))
	}
	if got[0].ID != "t0" || len(got[0].Steps) != 3 {
		t.Errorf("round trip lost data: %+v", got[0])
	}
	if _, err := LoadTrajectory(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("loading a missing fixture should error")
	}
	if _, err := LoadTrajectories(filepath.Join(dir, "nope")); err == nil {
		t.Error("loading a missing directory should error")
	}
}

func TestSameJSONIgnoresKeyOrder(t *testing.T) {
	if !sameJSON(`{"a":1,"b":2}`, `{"b":2,"a":1}`) {
		t.Error("key order should not matter")
	}
	if sameJSON(`{"a":1}`, `{"a":2}`) {
		t.Error("different values must not match")
	}
	if !sameJSON("plain", "plain") {
		t.Error("non-JSON should fall back to string equality")
	}
	if sameJSON("plain", "other") {
		t.Error("different non-JSON must not match")
	}
}
