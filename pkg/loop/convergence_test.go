package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// alwaysRetryBoard builds a one-task board whose reviewer never approves, with
// an escalate handler that answers "retry" every single time — the exact shape
// the UX reviewer reproduced: the gate says retry, the task re-enters an
// identical failing ladder, and nothing changes.
func alwaysRetryBoard(t *testing.T) (*Runner, *plan.Board, *scriptedExec, *int) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		if strings.Contains(req.AgentID, "review") {
			return `{"approved":false,"score":10,"summary":"still wrong","issues":["fix it"]}`
		}
		return `{"status":"done","summary":"did it"}`
	}}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 1
	r.WorkerCritique = false
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Update a.go", Description: "modify a.go", Acceptance: "compiles",
		Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"},
	}}}
	gateRetries := 0
	r.OnEscalate = func(_ context.Context, b *plan.Board, tk plan.Task, _ string) {
		gateRetries++
		plan.ApplyEscalateAction(b, tk.ID, plan.EscalateActionRetry, "human said retry")
	}
	return r, board, exec, &gateRetries
}

// A task that can never satisfy its gate must terminate in a bounded, SMALL
// number of model calls.
//
// Before the convergence fix this ran until RunBoard's 200-round safety guard
// tripped: ~9,100 model calls on a one-task board, because answering "retry"
// at the escalate gate moved the task back to ready_to_dev, reset the per-task
// call budget, and re-ran a byte-identical ladder forever.
func TestPermanentlyFailingTaskTerminatesInBoundedCalls(t *testing.T) {
	r, board, exec, gateRetries := alwaysRetryBoard(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := r.RunBoard(ctx, board)

	calls := exec.total()
	t.Logf("terminated after %d model calls, %d gate retries: %v", calls, *gateRetries, err)

	// Ceiling: (1 + MaxGateRetries) ladders, each capped by MaxTaskCalls.
	ceiling := (1 + plan.DefaultMaxGateRetries) * DefaultMaxTaskCalls
	if calls > ceiling {
		t.Fatalf("burned %d model calls on a one-task board (ceiling %d) — the retry ladder does not converge",
			calls, ceiling)
	}
	// The gate may FIRE once more than the cap — that last firing is the one
	// that refuses the retry — but it must never GRANT more than the cap.
	if *gateRetries > plan.DefaultMaxGateRetries+1 {
		t.Fatalf("escalate gate fired %d times, cap is %d", *gateRetries, plan.DefaultMaxGateRetries)
	}
	got, _ := board.Get("T1")
	if got.GateRetries > plan.DefaultMaxGateRetries {
		t.Fatalf("escalate gate granted %d retries, cap is %d", got.GateRetries, plan.DefaultMaxGateRetries)
	}
	if got.Column != plan.ColToScope {
		t.Fatalf("task ended in %q, want the human backlog once the retry cap is spent", got.Column)
	}
	if !strings.Contains(got.Notes, "retry cap") {
		t.Errorf("task notes never name the retry cap: %q", got.Notes)
	}
	if err != nil && !errors.Is(err, ErrGaveUp) {
		t.Fatalf("unexpected error class: %v", err)
	}
}

// Each gate retry must CHANGE something. Re-running the identical prompt
// against the identical stale output is an infinite loop with extra steps.
func TestGateRetryIsAStateChange(t *testing.T) {
	r, board, _, _ := alwaysRetryBoard(t)
	var afterRetry plan.Task
	seen := false
	r.OnEscalate = func(_ context.Context, b *plan.Board, tk plan.Task, _ string) {
		plan.ApplyEscalateAction(b, tk.ID, plan.EscalateActionRetry, "human said retry")
		if !seen {
			afterRetry, _ = b.Get(tk.ID)
			seen = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = r.RunBoard(ctx, board)

	if !seen {
		t.Fatal("the escalate gate never fired")
	}
	if strings.TrimSpace(afterRetry.Output) != "" {
		t.Errorf("retry kept the stale Output — the corrector re-judges old text: %q", afterRetry.Output)
	}
	if afterRetry.GateRetries != 1 {
		t.Errorf("GateRetries = %d after one gate retry, want 1", afterRetry.GateRetries)
	}
	if len(afterRetry.AttemptLog) == 0 {
		t.Error("retry carried no 'attempt N failed because X' ledger forward")
	}
	if !strings.Contains(strings.Join(afterRetry.AttemptLog, "\n"), "still wrong") {
		t.Errorf("the ledger does not name why the attempt failed: %v", afterRetry.AttemptLog)
	}
}

// A task that succeeds after a retry must still succeed.
func TestTaskThatSucceedsAfterRetryStillSucceeds(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	reopened := false
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		if strings.Contains(req.AgentID, "review") {
			mu.Lock()
			ok := reopened
			mu.Unlock()
			// Reject the whole first ladder; approve once the gate has
			// reopened the task.
			if !ok {
				return `{"approved":false,"score":10,"summary":"still wrong","issues":["fix it"]}`
			}
			return `{"approved":true,"score":90,"summary":"good now","issues":[]}`
		}
		mu.Lock()
		ok := reopened
		mu.Unlock()
		if ok {
			// The retry is the attempt that actually lands an edit — which is
			// the whole point of reopening the task.
			_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n\nfunc F() {}\n"), 0o600)
			return `{"status":"done","summary":"fixed it","files_changed":["a.go"]}`
		}
		return `{"status":"done","summary":"did it"}`
	}}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 1
	r.MaxRetries = 1
	r.WorkerCritique = false
	r.OnEscalate = func(_ context.Context, b *plan.Board, tk plan.Task, _ string) {
		plan.ApplyEscalateAction(b, tk.ID, plan.EscalateActionRetry, "try once more")
		mu.Lock()
		reopened = true
		mu.Unlock()
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Update a.go", Description: "modify a.go", Acceptance: "compiles",
		Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"},
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.RunBoard(ctx, board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	got, _ := board.Get("T1")
	if got.Column != plan.ColDone {
		t.Fatalf("task that passes review after a gate retry ended in %q, want done (%s)",
			got.Column, got.Error)
	}
}

// The board-level no-progress detector is the bound for handlers that never go
// through plan.ApplyEscalateAction — an embedding UI is free to move a task
// back to ready_to_dev by any route it likes, and the gate's own cap cannot see
// that. Three identical rounds is a stall; two hundred is not a guard.
func TestNoProgressRoundsStopTheBoard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		if strings.Contains(req.AgentID, "review") {
			return `{"approved":false,"score":10,"summary":"still wrong","issues":["fix it"]}`
		}
		return `{"status":"done","summary":"did it"}`
	}}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 1
	r.WorkerCritique = false
	// Take the per-task attempt ceiling out of the picture so the stall
	// detector is the thing under test.
	r.MaxTaskAttempts = 500
	r.OnEscalate = func(_ context.Context, b *plan.Board, tk plan.Task, _ string) {
		// A raw reopen: no ledger, no scope change, no counter — exactly the
		// shape that used to spin to the 200-round guard.
		cur, _ := b.Get(tk.ID)
		cur.Error = ""
		cur.MoveTo(plan.ColReadyToDev)
		b.UpdateTask(cur)
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Update a.go", Description: "modify a.go", Acceptance: "compiles",
		Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"},
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := r.RunBoard(ctx, board)
	if !errors.Is(err, ErrNoProgress) {
		t.Fatalf("err = %v, want ErrNoProgress", err)
	}
	var gerr *GaveUpError
	if !errors.As(err, &gerr) {
		t.Fatalf("err is not a *GaveUpError: %v", err)
	}
	if gerr.Rounds > 8 {
		t.Errorf("stall took %d rounds to detect — the detector is not the bound", gerr.Rounds)
	}
	if len(gerr.Stalled) == 0 || !strings.Contains(gerr.Stalled[0], "T1") {
		t.Errorf("the error does not name what stalled: %v", gerr.Stalled)
	}
	if !strings.Contains(gerr.Remedy, "re-scope") {
		t.Errorf("the error does not say what to do about it: %q", gerr.Remedy)
	}
	if calls := exec.total(); calls > 4*DefaultMaxTaskCalls {
		t.Errorf("stalled board spent %d model calls", calls)
	}
}

// The per-task attempt ceiling parks a task in the human backlog instead of
// aborting the whole board, and says so.
func TestAttemptCeilingParksTheTaskAndLetsSiblingsFinish(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package p\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		if strings.Contains(req.AgentID, "review") {
			if req.TaskID == "T2" {
				return `{"approved":true,"score":90,"summary":"fine","issues":[]}`
			}
			return `{"approved":false,"score":10,"summary":"still wrong","issues":["fix it"]}`
		}
		if req.TaskID == "T2" {
			_ = os.WriteFile(filepath.Join(root, "b.go"), []byte("package p\n\nfunc G() {}\n"), 0o600)
			return `{"status":"done","summary":"did it","files_changed":["b.go"]}`
		}
		return `{"status":"done","summary":"did it"}`
	}}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 1
	r.WorkerCritique = false
	r.MaxTaskAttempts = 2
	r.OnEscalate = func(_ context.Context, b *plan.Board, tk plan.Task, _ string) {
		cur, _ := b.Get(tk.ID)
		cur.Error = ""
		cur.MoveTo(plan.ColReadyToDev)
		b.UpdateTask(cur)
	}
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "Update a.go", Description: "modify a.go", Acceptance: "compiles",
			Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"}},
		{ID: "T2", Title: "Update b.go", Description: "modify b.go", Acceptance: "compiles",
			Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"b.go"}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = r.RunBoard(ctx, board)

	t1, _ := board.Get("T1")
	if t1.Column != plan.ColToScope {
		t.Errorf("T1 ended in %q, want the human backlog", t1.Column)
	}
	if !strings.Contains(t1.Error, "ceiling") {
		t.Errorf("T1 error does not name the attempt ceiling: %q", t1.Error)
	}
	t2, _ := board.Get("T2")
	if t2.Column != plan.ColDone {
		t.Errorf("T2 ended in %q — parking T1 must not stop its siblings", t2.Column)
	}
}

// The attempt ceiling bounds CONSECUTIVE failed attempts, not lifetime
// dispatches. A task that reached done and was reopened later (tester, QA gate,
// a human) has made progress and must start from a full ceiling — otherwise a
// long, healthy run parks tasks that are working.
func TestAttemptCeilingResetsWhenATaskCompletes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edits := 0
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		if strings.Contains(req.AgentID, "review") {
			return `{"approved":true,"score":90,"summary":"fine","issues":[]}`
		}
		// Each pass must land a DIFFERENT edit, or the evidence gate is right
		// to reject it and the test would be measuring that instead.
		edits++
		_ = os.WriteFile(filepath.Join(root, "a.go"),
			[]byte(fmt.Sprintf("package p\n\nfunc F%d() {}\n", edits)), 0o600)
		return `{"status":"done","summary":"did it","files_changed":["a.go"]}`
	}}
	r := defaultRunner(t, root, exec)
	r.MaxParallel = 1
	r.WorkerCritique = false
	r.MaxTaskAttempts = 2
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Update a.go", Description: "modify a.go", Acceptance: "compiles",
		Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"},
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Three successful passes, each above the 2-attempt ceiling if attempts
	// were counted for the lifetime of the board.
	for i := 0; i < 3; i++ {
		if err := r.RunBoard(ctx, board); err != nil {
			t.Fatalf("pass %d: %v", i+1, err)
		}
		got, _ := board.Get("T1")
		if got.Column != plan.ColDone {
			t.Fatalf("pass %d ended in %q, want done (%s)", i+1, got.Column, got.Error)
		}
		if r.waveAttempts.get("T1") != 0 {
			t.Fatalf("pass %d left %d attempts charged against a completed task",
				i+1, r.waveAttempts.get("T1"))
		}
		// Reopen, the way the tester/QA gate does.
		got.MoveTo(plan.ColReadyToDev)
		got.Error = ""
		board.UpdateTask(got)
	}
}
