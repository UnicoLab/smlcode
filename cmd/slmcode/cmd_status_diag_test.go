package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/session"
)

func TestFormatRunDiagnosticsCLIShowsActionsAndUsage(t *testing.T) {
	turn := session.Turn{ID: "run-1", Query: "fix local model run"}
	summary := session.AnalyzeEvents([]session.EventRecord{
		{Time: "2026-08-19T10:00:00Z", Phase: "execute", Kind: "task_start", Agent: "worker", TaskID: "T1", Message: "retry wave 1", Model: "qwen-local", Tokens: 700},
		{Time: "2026-08-19T10:00:03Z", Phase: "execute", Kind: "task_fail", Agent: "worker", TaskID: "T1", Message: "provider failed: connection refused; model not found", Model: "qwen-local", CostUSD: 0.002},
	})

	out := formatRunDiagnosticsCLI(turn, summary)
	for _, want := range []string{
		"Latest Run Diagnostics",
		"run-1",
		"tasks=1",
		"failures=1",
		"tokens=700",
		"qwen-local",
		"Check the model endpoint",
		"slmcode doctor",
		"Verify the configured model",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatLatestRunDiagnosticsReadsNewestEventLog(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	oldTurn, err := session.BeginTurn(slm, "run-old", "old query")
	if err != nil {
		t.Fatal(err)
	}
	newTurn, err := session.BeginTurn(slm, "run-new", "new query")
	if err != nil {
		t.Fatal(err)
	}
	oldTurn.UpdatedAt = "2026-08-19T09:00:00Z"
	newTurn.UpdatedAt = "2026-08-19T10:00:00Z"
	if err := session.SaveTurnBoard(slm, oldTurn, oldTurn.Board); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveTurnBoard(slm, newTurn, newTurn.Board); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendEvent(slm, "run-old", session.EventRecord{Phase: "done", Kind: "run_done", Message: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendEvent(slm, "run-new", session.EventRecord{Phase: "test", Kind: "task_fail", TaskID: "T1", Message: "qa_gate failed", Model: "local"}); err != nil {
		t.Fatal(err)
	}

	out := formatLatestRunDiagnostics(slm)
	if !strings.Contains(out, "run-new") || strings.Contains(out, "run-old") {
		t.Fatalf("did not select newest run diagnostics:\n%s", out)
	}
	if !strings.Contains(out, "Run the project QA gate") {
		t.Fatalf("missing QA action:\n%s", out)
	}
}
