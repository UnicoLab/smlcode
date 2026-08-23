package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// Each correction round must be judged on the CORRECTOR's output, not on the
// worker's original answer. The store reload that picks up human board edits
// used to replace the whole task with the LiveStore copy — written once, at the
// end of the wave — so every retry reset Output and the reviewer re-judged the
// same text MaxRetries+1 times while the corrector's work was discarded.
func TestCorrectionRoundsSeeThePreviousCorrection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var reviewPrompts []string
	corrections := 0
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		switch {
		case strings.Contains(req.AgentID, "review"):
			reviewPrompts = append(reviewPrompts, req.Input)
			return `{"approved":false,"score":30,"summary":"nope","issues":["fix it"]}`
		case req.AgentID == plan.RoleCorrector:
			corrections++
			return fmt.Sprintf(`{"status":"done","summary":"CORRECTOR-PASS-%d"}`, corrections)
		}
		return `{"status":"done","summary":"WORKER-OUTPUT"}`
	}}
	r := defaultRunner(t, root, exec)
	r.WorkerCritique = false
	r.MaxTaskCalls = 20 // isolate the ladder from the call budget
	r.Store = plan.NewLiveStore(filepath.Join(root, ".slmcode"))
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Update a.go", Description: "modify a.go", Acceptance: "compiles",
		Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"},
	}}}
	if err := r.Store.Replace(*board); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = r.RunBoard(ctx, board)

	if corrections < 2 || len(reviewPrompts) < 3 {
		t.Fatalf("expected a real ladder, got %d corrections / %d reviews", corrections, len(reviewPrompts))
	}
	for i := 1; i < len(reviewPrompts); i++ {
		want := fmt.Sprintf("CORRECTOR-PASS-%d", i)
		if !strings.Contains(reviewPrompts[i], want) {
			t.Errorf("review #%d does not carry %s — the correction was discarded before it was judged",
				i+1, want)
		}
	}
	got, _ := board.Get("T1")
	if got.Retries == 0 {
		t.Error("Task.Retries was reset to 0 by the store reload")
	}
}

// The per-task call budget must produce a useful terminal state, not a silently
// dropped task. This also pins the arithmetic: on the shipped defaults a task
// that never satisfies the reviewer spends worker + critique + 2×(review +
// correct) and escalates after TWO correction rounds, well short of
// MaxRetries=4 — the budget, not MaxRetries, is what ends the ladder.
func TestCallBudgetExhaustionEscalatesWithAUsefulState(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		calls = append(calls, req.AgentID)
		if strings.Contains(req.AgentID, "review") {
			return `{"approved":false,"score":30,"summary":"needs work","issues":["fix it"]}`
		}
		return `{"status":"done","summary":"did it"}` // never any write evidence
	}}
	r := defaultRunner(t, root, exec)
	r.WorkerCritique = true
	r.Store = plan.NewLiveStore(filepath.Join(root, ".slmcode"))
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Update a.go", Description: "modify a.go", Acceptance: "compiles",
		Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"},
	}}}
	if err := r.Store.Replace(*board); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = r.RunBoard(ctx, board)

	if len(calls) != DefaultMaxTaskCalls {
		t.Errorf("task made %d LLM calls, budget is %d: %v", len(calls), DefaultMaxTaskCalls, calls)
	}
	got, _ := board.Get("T1")
	if got.Column != plan.ColToScope {
		t.Fatalf("budget exhaustion left the task in %q, want the human backlog", got.Column)
	}
	if !strings.Contains(got.Error, "budget") {
		t.Errorf("escalation reason does not name the budget: %q", got.Error)
	}
	if !strings.Contains(got.Notes, "ESCALATED") {
		t.Errorf("escalation notes are missing: %q", got.Notes)
	}
}
