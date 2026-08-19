package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestAppendAndReadEvents(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	if err := AppendEvent(slm, "run-1", EventRecord{Phase: "init", Kind: "phase", Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(slm, "run-1", EventRecord{Phase: "execute", Agent: "worker", Message: "tick"}); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadEvents(slm, "run-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d", len(recs))
	}
	if recs[0].Message != "hello" || recs[1].Agent != "worker" {
		t.Fatalf("%+v", recs)
	}
}

func TestAppendAndReadEventData(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	payload := map[string]any{
		"summary": "dynamic",
		"phases":  []any{map[string]any{"id": "execute", "enabled": true}},
	}
	if err := AppendEvent(slm, "run-1", EventRecord{
		Phase: "compose", Kind: "composition", Message: "dynamic", Output: "# Dynamic pipeline", Data: payload,
	}); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadEvents(slm, "run-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Output == "" || recs[0].Data == nil {
		t.Fatalf("%+v", recs)
	}
	raw, err := json.Marshal(recs[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("invalid data: %s", raw)
	}
}

func TestAnalyzeEventsSurfacesSLMDiagnostics(t *testing.T) {
	events := []EventRecord{
		{Time: "2026-08-19T10:00:00Z", Phase: "plan", Kind: "phase", Agent: "planner", Message: "plan created", Model: "qwen-local"},
		{Time: "2026-08-19T10:00:02Z", Phase: "plan", Kind: "ask_answered", Message: "user requested replan with smaller scope", Model: "qwen-local"},
		{Time: "2026-08-19T10:00:05Z", Phase: "execute", Kind: "task_start", Agent: "go-worker", TaskID: "T1", Message: "retry wave 1", Model: "qwen-local"},
		{Time: "2026-08-19T10:00:08Z", Phase: "execute", Kind: "task_start", Agent: "go-worker", TaskID: "T1", Message: "retry wave 2", Model: "qwen-local"},
		{Time: "2026-08-19T10:00:10Z", Phase: "execute", Kind: "task_start", Agent: "go-worker", TaskID: "T2", Message: "retry wave 3", Model: "fast-local"},
		{Time: "2026-08-19T10:00:13Z", Phase: "test", Kind: "task_fail", Agent: "tester", TaskID: "T2", Message: "qa_gate failed round 2/2", Model: "fast-local", Tokens: 1200, CostUSD: 0.003},
	}

	got := AnalyzeEvents(events)
	if got.TotalEvents != len(events) || got.Tasks != 2 {
		t.Fatalf("bad totals: %+v", got)
	}
	if got.Replans != 1 || got.Retries != 3 || got.Failures == 0 {
		t.Fatalf("missing diagnostics: %+v", got)
	}
	if got.DurationMS != 13_000 {
		t.Fatalf("duration=%d", got.DurationMS)
	}
	if len(got.Agents) == 0 || got.Agents[0].Name != "go-worker" {
		t.Fatalf("agents not ranked: %+v", got.Agents)
	}
	if len(got.Models) != 2 {
		t.Fatalf("models: %+v", got.Models)
	}
	if got.Tokens != 1200 || got.CostUSD != 0.003 {
		t.Fatalf("usage not summarized: %+v", got)
	}
	var highRetry, replan, noTerminal bool
	for _, in := range got.Insights {
		if in.Title == "High retry pressure" {
			highRetry = true
		}
		if in.Title == "Plan was revised" {
			replan = true
		}
		if in.Title == "No successful terminal event" {
			noTerminal = true
		}
	}
	if !highRetry || !replan || !noTerminal {
		t.Fatalf("insights did not explain run: %+v", got.Insights)
	}
	if !hasRunAction(got, "Shrink the next attempt") || !hasRunAction(got, "Run the project QA gate") {
		t.Fatalf("missing action recommendations: %+v", got.Actions)
	}
}

func TestAnalyzeEventsRecognizesTerminalSuccess(t *testing.T) {
	got := AnalyzeEvents([]EventRecord{
		{Time: "2026-08-19T10:00:00Z", Phase: "init", Kind: "run_start", Message: "started"},
		{Time: "2026-08-19T10:00:03Z", Phase: "done", Kind: "run_done", Message: "done"},
	})
	for _, in := range got.Insights {
		if in.Title == "No successful terminal event" {
			t.Fatalf("unexpected terminal warning: %+v", got)
		}
	}
}

func TestAnalyzeEventsRecommendsLocalModelFixes(t *testing.T) {
	got := AnalyzeEvents([]EventRecord{
		{
			Time:    "2026-08-19T10:00:00Z",
			Phase:   "execute",
			Kind:    "run_error",
			Message: "provider failed: connection refused while loading model not found; context window exceeded",
			Model:   "missing-local",
		},
	})
	for _, want := range []string{
		"Check the model endpoint",
		"Verify the configured model",
		"Shrink the next attempt",
	} {
		if !hasRunAction(got, want) {
			t.Fatalf("missing %q action: %+v", want, got.Actions)
		}
	}
}

func hasRunAction(summary EventSummary, title string) bool {
	for _, action := range summary.Actions {
		if action.Title == title {
			return true
		}
	}
	return false
}
