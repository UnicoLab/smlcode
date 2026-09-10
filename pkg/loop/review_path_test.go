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

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// ── the reviewer is not asked when its answer could not matter ─────────────

// TestFastRejectSkipsTheReviewerOnAHardGateFailure: a FAILED deterministic
// smoke is a rejection whatever the reviewer says — applyHardGates overturns
// an approval in that state — so the reviewer request is pure cost. The verdict
// must be the gate's own words.
func TestFastRejectSkipsTheReviewerOnAHardGateFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{answer: func(ggagent.SubAgentRequest, int) string {
		return `{"approved":true,"score":95,"summary":"looks fine"}`
	}}
	r := defaultRunner(t, root, exec)
	task := plan.Task{
		ID: "T1", Title: "Update a", Role: plan.RoleWorker, Column: plan.ColInReview,
		Description: "update the greeting in a.go", Acceptance: "greeting updated",
		Files: []string{"a.go"},
		Output: `{"status":"done","summary":"updated","files_changed":["a.go"]}` +
			failingSmokeSection("go test ./...", "--- FAIL: TestGreeting"),
	}
	g := r.gatherGateSignals(context.Background(), &task, r.snapshotTargets(task))
	if !g.smokeFail || !g.blocking() {
		t.Fatalf("fixture does not trip the smoke gate: %+v", g)
	}
	review, raw, err := r.decideReview(context.Background(), task, g)
	if err != nil {
		t.Fatalf("decideReview: %v", err)
	}
	if n := exec.total(); n != 0 {
		t.Fatalf("%d LLM request(s) were made for a verdict the gate had already decided", n)
	}
	if review.Approved || raw != "" {
		t.Fatalf("hard gate failure was not a rejection: approved=%v raw=%q", review.Approved, raw)
	}
	summary, issue := g.rejectReason()
	if review.Summary != summary {
		t.Fatalf("summary = %q, want the gate's own %q", review.Summary, summary)
	}
	if len(review.Issues) != 1 || review.Issues[0] != issue {
		t.Fatalf("issues = %v, want [%q]", review.Issues, issue)
	}
}

// TestFastRejectUsesTheWorkersOwnBlockedSummary: a worker that finalized with
// status blocked and left nothing on disk has already said what a rejection
// would say. Its summary is the verdict; no reviewer is consulted.
func TestFastRejectUsesTheWorkersOwnBlockedSummary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{answer: func(ggagent.SubAgentRequest, int) string {
		return `{"approved":true,"score":95,"summary":"looks fine"}`
	}}
	r := defaultRunner(t, root, exec)
	task := plan.Task{
		ID: "T1", Title: "Update a", Role: plan.RoleWorker, Column: plan.ColInReview,
		Description: "update the greeting in a.go", Acceptance: "greeting updated",
		Files:  []string{"a.go"},
		Output: `{"status":"blocked","summary":"the greeting constant is defined in a file outside my scope","files_changed":[]}`,
	}
	g := r.gatherGateSignals(context.Background(), &task, r.snapshotTargets(task))
	review, _, err := r.decideReview(context.Background(), task, g)
	if err != nil {
		t.Fatalf("decideReview: %v", err)
	}
	if n := exec.total(); n != 0 {
		t.Fatalf("%d LLM request(s) were made to reject a worker that rejected itself", n)
	}
	if review.Approved {
		t.Fatal("a blocked worker with no evidence was approved")
	}
	if !strings.Contains(review.Summary, "outside my scope") ||
		len(review.Issues) != 1 || !strings.Contains(review.Issues[0], "outside my scope") {
		t.Fatalf("verdict does not carry the worker's own words: summary=%q issues=%v",
			review.Summary, review.Issues)
	}
	// A tester's failing command is its finding, not a defect: never fast-rejected.
	tester := task
	tester.Role = plan.RoleTester
	if _, ok := g.fastReject(tester); ok {
		t.Fatal("a tester was fast-rejected")
	}
}

// ── a reviewer transport error is not a verdict on the work ────────────────

// flakyReviewerExec fails the first N reviewer calls with a transport error and
// answers every later one; workers answer prose with no status JSON and no
// write, so nothing but the reviewer can approve the task.
type flakyReviewerExec struct {
	mu             sync.Mutex
	reviewerErrors int
	reviewerCalls  int
	workerCalls    int
}

func (e *flakyReviewerExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		if strings.Contains(req.AgentID, "review") {
			e.reviewerCalls++
			if e.reviewerCalls <= e.reviewerErrors {
				out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
					Error: errors.New("chat failed: Post \"http://127.0.0.1:1/v1/chat/completions\": " +
						"dial tcp 127.0.0.1:1: connect: connection refused")}
				continue
			}
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
				Output: `{"approved":true,"score":90,"summary":"findings are sound"}`}
			continue
		}
		e.workerCalls++
		out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
			Output: "Findings: the parser already handles UTF-8 input correctly; no change is needed."}
	}
	return out, nil
}

func (e *flakyReviewerExec) counts() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reviewerCalls, e.workerCalls
}

// investigationTask is a task the evidence gate does not require a write for,
// so the reviewer's verdict is the only thing standing between it and done.
func investigationTask(id string, deps ...string) plan.Task {
	return plan.Task{
		ID: id, Title: "Investigate the parser", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "Report whether the parser handles UTF-8 input.", Acceptance: "findings reported",
		Files: []string{"parser.go"}, DependsOn: deps,
	}
}

func TestReviewerTransportErrorDoesNotBlockTheTask(t *testing.T) {
	t.Run("one error: the reviewer is asked once more", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "parser.go"), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		exec := &flakyReviewerExec{reviewerErrors: 1}
		r := defaultRunner(t, root, exec)
		r.MaxRetries = 0
		board := &plan.Board{Tasks: []plan.Task{investigationTask("T1")}}
		if err := r.RunBoard(context.Background(), board); err != nil {
			t.Fatalf("RunBoard: %v", err)
		}
		got, _ := board.Get("T1")
		if got.Column == plan.ColBlocked {
			t.Fatalf("a reviewer transport error blocked the task: %+v", got)
		}
		if got.Column != plan.ColDone {
			t.Fatalf("T1 column=%s error=%q, want done after the re-ask", got.Column, got.Error)
		}
		if reviews, _ := exec.counts(); reviews != 2 {
			t.Fatalf("reviewer requests=%d, want exactly 2 (the failed one and one re-ask)", reviews)
		}
	})

	t.Run("reviewer down for good: human backlog, dependents untouched", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "parser.go"), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		exec := &flakyReviewerExec{reviewerErrors: 1 << 20}
		r := defaultRunner(t, root, exec)
		r.MaxRetries = 0
		board := &plan.Board{Tasks: []plan.Task{investigationTask("T1"), investigationTask("T2", "T1")}}
		_ = r.RunBoard(context.Background(), board)
		t1, _ := board.Get("T1")
		if t1.Column == plan.ColBlocked {
			t.Fatalf("a reviewer outage blocked the task: %+v", t1)
		}
		if t1.Column != plan.ColToScope || !strings.Contains(t1.Error, "reviewer unavailable") {
			t.Fatalf("T1 column=%s error=%q, want to_scope with a reviewer-unavailable reason",
				t1.Column, t1.Error)
		}
		t2, _ := board.Get("T2")
		if t2.Column != plan.ColReadyToDev {
			t.Fatalf("dependent T2 column=%s error=%q, want ready_to_dev — nothing judged T1's work",
				t2.Column, t2.Error)
		}
		if reviews, _ := exec.counts(); reviews != 2 {
			t.Fatalf("reviewer requests=%d, want exactly 2 (one re-ask, then escalate)", reviews)
		}
	})
}

// ── a timed-out worker resumes; a transient error retries ───────────────────

// timeoutThenResumeExec times the first worker call out mid-ReAct (with a
// transcript, as the real executor returns one) and finishes the task on the
// second — but only from the restored transcript, never from a cold start.
type timeoutThenResumeExec struct {
	mu          sync.Mutex
	root        string
	workerCalls int
	sawResume   bool
	coldRestart bool
}

func (e *timeoutThenResumeExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		if strings.Contains(req.AgentID, "review") {
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
				Output: `{"approved":true,"score":90,"summary":"ok"}`}
			continue
		}
		e.workerCalls++
		if e.workerCalls == 1 {
			msgs := []llm.Message{
				{Role: "user", Content: req.Input},
				{Role: "assistant", Content: "editing a.go", ToolCalls: []llm.ToolCall{{
					ID: "call_1", Type: "function",
					Function: llm.FunctionCall{Name: "ws_edit", Arguments: `{"path":"a.go"}`},
				}}},
			}
			out[i] = ggagent.SubAgentResult{
				AgentID: req.AgentID, TaskID: req.TaskID,
				Messages: msgs, Iteration: 3, PendingToolCalls: msgs[1].ToolCalls,
				Error: context.DeadlineExceeded,
			}
			continue
		}
		if req.Resume && len(req.Messages) >= 2 {
			e.sawResume = true
		} else {
			e.coldRestart = true
		}
		_ = os.WriteFile(filepath.Join(e.root, "a.go"), []byte("package main\n\nconst Greeting = \"hi\"\n"), 0o644)
		out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
			Output: `{"status":"done","summary":"resumed and finished","files_changed":["a.go"]}`}
	}
	return out, nil
}

func TestTimedOutWorkerIsResumedNotParked(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	turn, err := session.BeginTurn(slm, "run-timeout-1", "update the greeting")
	if err != nil {
		t.Fatal(err)
	}
	exec := &timeoutThenResumeExec{root: root}
	r := defaultRunner(t, root, exec)
	r.SlmDir = slm
	r.TurnID = turn.ID
	r.MaxRetries = 0
	r.Timeout = time.Second
	var logs []string
	r.Log = func(format string, args ...interface{}) { logs = append(logs, fmt.Sprintf(format, args...)) }
	timeoutInterventions := 0
	r.OnEvent = func(kind, _, _, _, scope, _ string) {
		if kind == "intervention" && scope == "timeout" {
			timeoutInterventions++
		}
	}
	board := &plan.Board{QueryID: turn.ID, Query: turn.Query, Tasks: []plan.Task{{
		ID: "T1", Title: "update the greeting", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "update the greeting in a.go", Acceptance: "greeting updated",
		Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v\n%s", err, strings.Join(logs, "\n"))
	}
	got, _ := board.Get("T1")
	if got.Column != plan.ColDone {
		t.Fatalf("T1 column=%s error=%q — a first-attempt timeout must be retried, not parked\n%s",
			got.Column, got.Error, strings.Join(logs, "\n"))
	}
	if exec.workerCalls != 2 {
		t.Fatalf("worker calls=%d, want 2 (the timeout and the resumed retry)", exec.workerCalls)
	}
	if !exec.sawResume || exec.coldRestart {
		t.Fatalf("the retry did not resume from the checkpoint: sawResume=%v cold=%v",
			exec.sawResume, exec.coldRestart)
	}
	if timeoutInterventions != 0 {
		t.Fatalf("%d timeout intervention(s) raised for a retry the harness handled itself", timeoutInterventions)
	}
	if !strings.Contains(got.Notes, "RETRY-AFTER-TIMEOUT") {
		t.Fatalf("the retry left no note on the task: %q", got.Notes)
	}
}

// TestTimedOutWorkerParksOnlyAtTheAttemptCeiling: with a ceiling of two, a
// worker that times out every time is dispatched exactly twice and then parked.
func TestTimedOutWorkerParksOnlyAtTheAttemptCeiling(t *testing.T) {
	root := t.TempDir()
	exec := &timeoutExec{}
	r := defaultRunner(t, root, exec)
	r.MaxRetries = 0
	r.MaxTaskAttempts = 2
	r.Timeout = time.Second
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "one", Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}
	if got := exec.callCount(); got != 2 {
		t.Fatalf("executor calls=%d, want 2 (ceiling)", got)
	}
	got, _ := board.Get("T1")
	if got.Column != plan.ColToScope || !strings.Contains(got.Error, "timed out") {
		t.Fatalf("T1 at the ceiling: column=%s error=%q, want to_scope with a timeout reason", got.Column, got.Error)
	}
}

// transientThenOKExec fails the first worker call with a connection-level
// error, then succeeds.
type transientThenOKExec struct {
	mu          sync.Mutex
	root        string
	first       error
	workerCalls int
}

func (e *transientThenOKExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		if strings.Contains(req.AgentID, "review") {
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
				Output: `{"approved":true,"score":90,"summary":"ok"}`}
			continue
		}
		e.workerCalls++
		if e.workerCalls == 1 {
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Error: e.first}
			continue
		}
		_ = os.WriteFile(filepath.Join(e.root, "a.go"), []byte("package main\n\nconst Greeting = \"hi\"\n"), 0o644)
		out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
			Output: `{"status":"done","summary":"second try","files_changed":["a.go"]}`}
	}
	return out, nil
}

func TestTransientWorkerErrorIsRetriedOnceBeforeBlocking(t *testing.T) {
	newBoard := func() *plan.Board {
		return &plan.Board{Tasks: []plan.Task{{
			ID: "T1", Title: "update the greeting", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update the greeting in a.go", Acceptance: "greeting updated",
			Files: []string{"a.go"},
		}}}
	}
	t.Run("connection refused is retried and the retry lands", func(t *testing.T) {
		root := t.TempDir()
		_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644)
		exec := &transientThenOKExec{root: root, first: errors.New(
			"chat failed: Post \"http://127.0.0.1:1/v1/chat/completions\": dial tcp 127.0.0.1:1: connect: connection refused")}
		if !backends.Classify(exec.first).Retryable() {
			t.Fatal("fixture error is not classified as retryable")
		}
		r := defaultRunner(t, root, exec)
		r.MaxRetries = 0
		board := newBoard()
		if err := r.RunBoard(context.Background(), board); err != nil {
			t.Fatalf("RunBoard: %v", err)
		}
		got, _ := board.Get("T1")
		if got.Column != plan.ColDone {
			t.Fatalf("T1 column=%s error=%q, want done after one transient retry", got.Column, got.Error)
		}
		if exec.workerCalls != 2 {
			t.Fatalf("worker calls=%d, want 2", exec.workerCalls)
		}
	})
	t.Run("an unclassifiable error still blocks without a retry", func(t *testing.T) {
		root := t.TempDir()
		_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644)
		exec := &transientThenOKExec{root: root, first: errors.New("the model exploded")}
		if backends.Classify(exec.first).Retryable() {
			t.Fatal("fixture error is unexpectedly retryable")
		}
		r := defaultRunner(t, root, exec)
		r.MaxRetries = 0
		board := newBoard()
		_ = r.RunBoard(context.Background(), board)
		got, _ := board.Get("T1")
		if got.Column != plan.ColBlocked {
			t.Fatalf("T1 column=%s, want blocked", got.Column)
		}
		if exec.workerCalls != 1 {
			t.Fatalf("worker calls=%d, want 1 (no retry for a permanent failure)", exec.workerCalls)
		}
	})
}

func TestTransientRetryDelayHonorsRetryAfterWithinACap(t *testing.T) {
	cases := []struct {
		name string
		c    backends.Classification
		want time.Duration
	}{
		{"plain transient waits nothing", backends.Classification{Class: backends.ClassTransient}, 0},
		{"429 without a hint pauses briefly", backends.Classification{Class: backends.ClassRateLimited}, time.Second},
		{"Retry-After is honored", backends.Classification{Class: backends.ClassRateLimited, RetryAfter: 4 * time.Second}, 4 * time.Second},
		{"a long hint is capped", backends.Classification{Class: backends.ClassTransient, RetryAfter: 5 * time.Minute}, maxTransientRetryDelay},
	}
	for _, tc := range cases {
		if got := transientRetryDelay(tc.c); got != tc.want {
			t.Errorf("%s: delay=%s want %s", tc.name, got, tc.want)
		}
	}
}

// ── every dispatch is clamped to the runway ─────────────────────────────────

// TestEveryDispatchTimeoutFitsTheRunway drives worker, reviewer and corrector
// under a 30-second run deadline with a 12-minute task timeout, and checks the
// timeout handed to the executor for EVERY request against what the runway
// allows once the finish reserve is held back.
func TestEveryDispatchTimeoutFitsTheRunway(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		if strings.Contains(req.AgentID, "review") {
			return `{"approved":false,"score":30,"summary":"no test was added"}`
		}
		return `{"status":"done","summary":"claims only","files_changed":["a.go"]}`
	}}
	r := defaultRunner(t, root, exec)
	r.Timeout = 12 * time.Minute
	r.MaxRetries = 1
	const runway = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), runway)
	defer cancel()
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "update the greeting", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "update the greeting in a.go", Acceptance: "greeting updated",
		Files: []string{"a.go"},
	}}}
	_ = r.RunBoard(ctx, board)

	usable := runway - runway/loopFinishReserveDivisor
	if exec.countFor(plan.RoleReviewer) == 0 || exec.countFor(plan.RoleCorrector) == 0 {
		t.Fatalf("the fixture did not exercise review and correction: %d reviewer, %d corrector requests",
			exec.countFor(plan.RoleReviewer), exec.countFor(plan.RoleCorrector))
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	for _, req := range exec.reqs {
		if req.Timeout <= 0 || req.Timeout > usable {
			t.Errorf("%s request timeout %s exceeds the %s the runway allows (task_timeout=%s)",
				req.AgentID, req.Timeout, usable, r.Timeout)
		}
	}
}
