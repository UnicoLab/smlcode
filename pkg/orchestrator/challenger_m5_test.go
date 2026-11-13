package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// TestChallengerM5_OrchestratorConcurrentRunRejection stress-tests concurrent Run() calls
// ensuring that while one run is executing, subsequent concurrent runs receive
// "a run is already in progress" and never corrupt the internal orchestrator state.
func TestChallengerM5_OrchestratorConcurrentRunRejection(t *testing.T) {
	started := make(chan struct{})
	blocker := make(chan struct{})

	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			select {
			case <-started:
			default:
				close(started)
			}
			<-blocker // Hold the first run until released
			return `{"summary":"mock result"}`
		},
	}

	o := setupTestOrchestrator(t, exec)
	o.cfg.Calibrate = "off"
	o.cfg.ClarifyMode = plan.ClarifyOff
	o.cfg.PlanApprove = plan.PlanApproveModeAuto
	o.cfg.AutoApprove = true

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	// Start run 1 in background
	run1ErrCh := make(chan error, 1)
	go func() {
		_, err := o.Run(ctx1, "primary task")
		run1ErrCh <- err
	}()

	// Wait for run 1 to start
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for primary run to start")
	}

	// Now launch 10 concurrent Run() attempts while run 1 is active
	var wg sync.WaitGroup
	concurrentErrors := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := o.Run(context.Background(), fmt.Sprintf("concurrent task %d", idx))
			concurrentErrors[idx] = err
		}(i)
	}
	wg.Wait()

	// Release primary run and cancel context to complete quickly
	cancel1()
	close(blocker)
	<-run1ErrCh

	for i, err := range concurrentErrors {
		if err == nil {
			t.Errorf("concurrent run %d succeeded, want error 'a run is already in progress'", i)
		} else if !strings.Contains(err.Error(), "a run is already in progress") {
			t.Errorf("concurrent run %d returned unexpected error: %v", i, err)
		}
	}
}

// TestChallengerM5_PreCanceledContextInRun verifies that calling Run with an already
// canceled context aborts cleanly without deadlocks or uncleaned state.
func TestChallengerM5_PreCanceledContextInRun(t *testing.T) {
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return `{"summary":"should not be called"}`
		},
	}
	o := setupTestOrchestrator(t, exec)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	res, err := o.Run(ctx, "implement fast cache")
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled in error, got: %v", err)
	}
	if res != nil && res.Success {
		t.Errorf("expected res.Success = false on canceled run, got true")
	}

	// Verify running state was cleared
	o.mu.Lock()
	running := o.running
	o.mu.Unlock()
	if running {
		t.Errorf("orchestrator.running remains true after canceled run")
	}
}

// TestChallengerM5_DocPRDAndContractDocumentCeilings verifies that massive PRD and
// CONTRACT inputs are bounded strictly at 24 KB and 16 KB ceilings respectively.
func TestChallengerM5_DocPRDAndContractDocumentCeilings(t *testing.T) {
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return ""
		},
	}
	o := setupTestOrchestrator(t, exec)

	// 1. Massive PRD with 100 features
	var massivePRD strings.Builder
	massivePRD.WriteString("# Massive PRD\n\n")
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&massivePRD, "### Feature %d: High Performance Microservice Component %d\n", i, i)
		massivePRD.WriteString("This is a detailed specification with extensive acceptance criteria and boundary requirements.\n")
		massivePRD.WriteString("- Criterion 1: Must handle 10,000 requests per second with latency under 5ms.\n")
		massivePRD.WriteString("- Criterion 2: Zero memory allocations on the hot path.\n\n")
	}
	rawPRD := massivePRD.String()
	if len(rawPRD) < 30000 {
		t.Fatalf("test precondition failed: rawPRD length is %d, want > 30000", len(rawPRD))
	}

	err := o.store.WriteBounded(contextstore.DocPRD, rawPRD)
	if err != nil {
		t.Fatalf("WriteBounded DocPRD failed: %v", err)
	}

	storedPRD, err := o.store.Read(contextstore.DocPRD)
	if err != nil {
		t.Fatalf("Read DocPRD failed: %v", err)
	}
	if len(storedPRD) > 24576 {
		t.Errorf("DocPRD length = %d, exceeds 24 KB (24576 bytes) ceiling", len(storedPRD))
	}

	// 2. Massive CONTRACT with 150 interfaces
	var massiveContract strings.Builder
	massiveContract.WriteString("# Interface Contracts\n\n")
	for i := 1; i <= 150; i++ {
		fmt.Fprintf(&massiveContract, "### Contract %d: IService%d\n", i, i)
		fmt.Fprintf(&massiveContract, "```go\ntype IService%d interface {\n\tExecute%d(ctx context.Context, req Request%d) (*Response%d, error)\n}\n```\n\n", i, i, i, i)
	}
	rawContract := massiveContract.String()
	if len(rawContract) < 20000 {
		t.Fatalf("test precondition failed: rawContract length is %d, want > 20000", len(rawContract))
	}

	err = o.store.WriteBounded(contextstore.DocContract, rawContract)
	if err != nil {
		t.Fatalf("WriteBounded DocContract failed: %v", err)
	}

	storedContract, err := o.store.Read(contextstore.DocContract)
	if err != nil {
		t.Fatalf("Read DocContract failed: %v", err)
	}
	if len(storedContract) > 16384 {
		t.Errorf("DocContract length = %d, exceeds 16 KB (16384 bytes) ceiling", len(storedContract))
	}
}

// TestChallengerM5_CorruptedScopeInterviewRecovery verifies that malformed clarify outputs
// (truncated JSON, markdown wrappers with junk, unicode noise) are handled defensively.
func TestChallengerM5_CorruptedScopeInterviewRecovery(t *testing.T) {
	corruptedInputs := []string{
		"```json\n{\"needs_user\": false, \"questions\": [UNCLOSED",
		"Non-JSON conversational response: I recommend we implement a cache using redis.",
		"{\"needs_user\": true, \"questions\": null, \"assumptions\": [123, 456]}",
		"",
		"   \n\t  \r ",
		"{\"prd\": {\"features\": \"not-a-list\"}}",
	}

	for i, badInput := range corruptedInputs {
		t.Run(fmt.Sprintf("CorruptedCase_%d", i), func(t *testing.T) {
			exec := &mockSubAgentExec{
				fn: func(agentID, input string) string {
					return badInput
				},
			}
			o := setupTestOrchestrator(t, exec)
			o.cfg.ClarifyMode = plan.ClarifyAuto

			interview := o.runScopeInterview(context.Background(), "create calculator", "")
			// Should return a valid ScopeInterview without panicking
			_ = interview.ToClarifyResult()
		})
	}
}

// TestChallengerM5_AdversarialReviewDocPersistedInCompleteRun verifies that DocReview
// (REVIEW.md) is always created and bounded, and shared state is updated.
func TestChallengerM5_AdversarialReviewDocPersistedInCompleteRun(t *testing.T) {
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return `{"summary":"all done"}`
		},
	}
	o := setupTestOrchestrator(t, exec)

	board := &plan.Board{
		QueryID: "test-review-run",
		Query:   "implement feature",
		Tasks: []plan.Task{
			{
				ID:     "T1",
				Title:  "build feature",
				Column: plan.ColDone,
				Status: plan.StatusDone,
			},
		},
	}

	testOut := `{"passed":true,"summary":"all 15 unit tests pass cleanly","failures":[]}`
	start := time.Now()

	res, err := o.completeRun(context.Background(), "test-review-run", board.Query, "", board, testOut, false, false, "go test ./...", start)
	if err != nil {
		t.Fatalf("completeRun returned unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("completeRun returned nil result")
	}

	reviewDoc, err := o.store.Read(contextstore.DocReview)
	if err != nil || reviewDoc == "" {
		t.Fatalf("expected DocReview to be written, err=%v", err)
	}

	if !strings.Contains(reviewDoc, "Verification & Adversarial Review Report") {
		t.Errorf("REVIEW.md missing title header, got: %s", reviewDoc)
	}
	if !strings.Contains(reviewDoc, "test-review-run") {
		t.Errorf("REVIEW.md missing run ID")
	}
	if !strings.Contains(reviewDoc, "go test ./...") {
		t.Errorf("REVIEW.md missing QA command")
	}

	sharedReview, ok := o.shared.GetGlobal("review")
	if !ok || sharedReview == "" {
		t.Errorf("shared global 'review' not set")
	}
}
