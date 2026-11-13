package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// TestChallengerM5_AdversarialVetoOverridesAndHaltsRetryLadder verifies that when an
// adversarial reviewer issues a "veto", any approved: true claim is overridden to false,
// the task is immediately escalated, and the corrector ladder is halted without wasting attempts.
func TestChallengerM5_AdversarialVetoOverridesAndHaltsRetryLadder(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "service.go")
	if err := os.WriteFile(targetPath, []byte("package service\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var correctorInvocations int32
	var reviewerInvocations int32

	exec := &challengerMockExecutor{
		reviewFn: func(req ggagent.SubAgentRequest, callNum int) string {
			atomic.AddInt32(&reviewerInvocations, 1)
			// Adversarial reviewer issues a veto despite superficially "clean" looking syntax
			return `{
				"approved": true,
				"score": 90,
				"verdict": "veto",
				"summary": "veto: architectural security vulnerability in auth protocol",
				"issues": ["unauthenticated token reuse possible"]
			}`
		},
		correctorFn: func(req ggagent.SubAgentRequest, callNum int) string {
			atomic.AddInt32(&correctorInvocations, 1)
			return `{"status":"done","summary":"attempted fix"}`
		},
	}

	r := &Runner{
		Root:       root,
		Executor:   exec,
		ClaimsGate: true,
		MaxRetries: 3,
		Log:        func(string, ...interface{}) {},
	}

	task := plan.Task{
		ID:          "TASK_SEC_VETO",
		Role:        plan.RoleWorker,
		Title:       "Implement auth service",
		Description: "implement auth service",
		Criteria:    []plan.Criterion{{ID: "AC1", Text: "secure authentication without replay", Priority: "must"}},
		Files:       []string{"service.go"},
		Output:      `{"status":"done","files_changed":["service.go"],"summary":"service implemented"}`,
		Column:      plan.ColInReview,
	}

	// Update the file so evidenceOK passes
	if err := os.WriteFile(targetPath, []byte("package service\n\nfunc Run() { println(\"updated\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	baseline := map[string]string{"service.go": "1:initial"}

	final, esc, err := r.reviewAndCorrectTask(context.Background(), task, baseline)
	if err != nil {
		t.Fatalf("reviewAndCorrectTask returned unexpected error: %v", err)
	}

	// 1. Task must not be approved
	if final.Column == plan.ColDone {
		t.Errorf("task was marked done despite adversarial veto!")
	}
	if esc == nil {
		t.Fatalf("expected task escalation after veto, got nil escalation")
	}
	if final.Column != plan.ColToScope {
		t.Errorf("expected final column to be %s (to_scope), got %s", plan.ColToScope, final.Column)
	}

	// 2. Reviewer was called exactly once
	if revCalls := atomic.LoadInt32(&reviewerInvocations); revCalls != 1 {
		t.Errorf("expected reviewer to be called 1 time before veto halt, got %d", revCalls)
	}

	// 3. Corrector ladder MUST NOT have been invoked (veto is fatal/unrecoverable)
	if corrCalls := atomic.LoadInt32(&correctorInvocations); corrCalls != 0 {
		t.Errorf("CRITICAL: corrector was invoked %d times after an adversarial veto!", corrCalls)
	}
}

// TestChallengerM5_AdversarialRejectAllowsCorrectorUntilRegression tests that a "reject"
// verdict permits correction, but if the correction worsens the review score (regression),
// the ladder immediately halts and escalates.
func TestChallengerM5_AdversarialRejectAllowsCorrectorUntilRegression(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "calc.go")
	if err := os.WriteFile(targetPath, []byte("package calc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var reviewerCallCount int32
	var correctorCallCount int32

	exec := &challengerMockExecutor{
		reviewFn: func(req ggagent.SubAgentRequest, callNum int) string {
			c := atomic.AddInt32(&reviewerCallCount, 1)
			if c == 1 {
				// Attempt 1: rejection with score 60
				return `{
					"approved": false,
					"score": 60,
					"verdict": "reject",
					"summary": "reject: division by zero not handled",
					"issues": ["missing zero check"]
				}`
			}
			// Attempt 2: corrector introduced worse bug -> score drops to 30!
			return `{
				"approved": false,
				"score": 30,
				"verdict": "reject",
				"summary": "reject: syntax error and broken logic",
				"issues": ["broken syntax"]
			}`
		},
		correctorFn: func(req ggagent.SubAgentRequest, callNum int) string {
			atomic.AddInt32(&correctorCallCount, 1)
			return `{"status":"done","files_changed":["calc.go"],"summary":"attempted fix"}`
		},
	}

	r := &Runner{
		Root:       root,
		Executor:   exec,
		ClaimsGate: true,
		MaxRetries: 5, // high retry budget
		Log:        func(string, ...interface{}) {},
	}

	task := plan.Task{
		ID:          "TASK_REGRESS",
		Role:        plan.RoleWorker,
		Title:       "Implement divide",
		Description: "implement divide function",
		Criteria:    []plan.Criterion{{ID: "AC1", Text: "calculate division without zero panic", Priority: "must"}},
		Files:       []string{"calc.go"},
		Output:      `{"status":"done","files_changed":["calc.go"]}`,
		Column:      plan.ColInReview,
	}

	// Baseline snapshot
	if err := os.WriteFile(targetPath, []byte("package calc\nfunc Div() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline := map[string]string{"calc.go": "1:initial"}

	final, esc, err := r.reviewAndCorrectTask(context.Background(), task, baseline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if esc == nil {
		t.Fatal("expected task escalation due to score regression")
	}
	if final.Column != plan.ColToScope {
		t.Errorf("expected column %s, got %s", plan.ColToScope, final.Column)
	}

	// Should have executed exactly 2 reviews (60, then 30)
	if revCalls := atomic.LoadInt32(&reviewerCallCount); revCalls != 2 {
		t.Errorf("expected exactly 2 reviews before regression halt, got %d", revCalls)
	}

	// Should have executed exactly 1 correction before regression halt
	if corrCalls := atomic.LoadInt32(&correctorCallCount); corrCalls != 1 {
		t.Errorf("expected exactly 1 correction before regression halt, got %d", corrCalls)
	}
}

// TestChallengerM5_ParallelReviewConcurrencyStress stress-tests reviewWave with
// ReviewParallel enabled across 12 tasks on disjoint files.
func TestChallengerM5_ParallelReviewConcurrencyStress(t *testing.T) {
	root := t.TempDir()

	const taskCount = 12
	var tasks []plan.Task
	snapshots := make(map[string]map[string]string)

	for i := 1; i <= taskCount; i++ {
		fname := fmt.Sprintf("mod%02d.go", i)
		fpath := filepath.Join(root, fname)
		if err := os.WriteFile(fpath, []byte("package mod\nfunc F() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tid := fmt.Sprintf("TASK_%02d", i)
		tasks = append(tasks, plan.Task{
			ID:          tid,
			Role:        plan.RoleWorker,
			Title:       "Update " + fname,
			Description: "update " + fname,
			Files:       []string{fname},
			Column:      plan.ColInReview,
			Output:      fmt.Sprintf(`{"status":"done","files_changed":[%q]}`, fname),
		})
		snapshots[tid] = map[string]string{fname: "1:base"}

		// Touch file so evidence exists
		if err := os.WriteFile(fpath, []byte(fmt.Sprintf("package mod\nfunc F%02d() {}\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	board := &plan.Board{Tasks: tasks}

	exec := &challengerMockExecutor{
		reviewFn: func(req ggagent.SubAgentRequest, callNum int) string {
			return `{"approved":true,"score":92,"verdict":"approve","summary":"parallel review pass"}`
		},
	}

	r := &Runner{
		Root:           root,
		Executor:       exec,
		ClaimsGate:     true,
		ReviewParallel: true,
		MaxParallel:    4,
		Log:            func(string, ...interface{}) {},
	}

	// Run parallel review wave
	r.reviewWave(context.Background(), board, tasks, snapshots)

	// All tasks must have transitioned to Done
	for _, task := range board.Tasks {
		if task.Column != plan.ColDone {
			t.Errorf("task %s was not marked done, in column %s", task.ID, task.Column)
		}
	}
}

// TestChallengerM5_TeamworkFastPathDeniedForNonRenameTasks verifies that under
// Teamwork mode (ReviewerRole = "teamwork-review"), the disk write fast-path is
// denied for non-rename tasks so that the adversarial reviewer is always consulted.
func TestChallengerM5_TeamworkFastPathDeniedForNonRenameTasks(t *testing.T) {
	root := t.TempDir()
	fpath := filepath.Join(root, "service.go")
	if err := os.WriteFile(fpath, []byte("package service\n\nfunc V1() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var revCalls int32
	exec := &challengerMockExecutor{
		reviewFn: func(req ggagent.SubAgentRequest, callNum int) string {
			atomic.AddInt32(&revCalls, 1)
			return `{"approved":true,"score":95,"verdict":"approve","summary":"adversarial review passed"}`
		},
	}

	r := &Runner{
		Root:         root,
		Executor:     exec,
		ReviewerRole: "teamwork-review",
		ClaimsGate:   true,
		Log:          func(string, ...interface{}) {},
	}

	task := plan.Task{
		ID:          "TASK_NO_CRITERIA",
		Role:        plan.RoleWorker,
		Title:       "Update service",
		Description: "update service",
		Files:       []string{"service.go"},
		Output:      `{"status":"done","files_changed":["service.go"],"summary":"updated"}`,
		Column:      plan.ColInReview,
	}

	// Update file on disk so disk write evidence exists
	if err := os.WriteFile(fpath, []byte("package service\n\nfunc V2() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	baseline := map[string]string{"service.go": "1:v1"}

	final, esc, err := r.reviewAndCorrectTask(context.Background(), task, baseline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if esc != nil {
		t.Fatalf("unexpected escalation: %v", esc)
	}
	if final.Column != plan.ColDone {
		t.Errorf("expected task to be done, got %s", final.Column)
	}

	// Adversarial reviewer MUST have been invoked, not bypassed by fast-path!
	if calls := atomic.LoadInt32(&revCalls); calls != 1 {
		t.Errorf("expected adversarial reviewer to be called 1 time, got %d (fast path should be denied)", calls)
	}
}
