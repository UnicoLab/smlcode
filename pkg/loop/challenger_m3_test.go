package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

type challengerMockExecutor struct {
	reviewCalls    int32
	correctorCalls int32
	workerCalls    int32
	reviewFn       func(req ggagent.SubAgentRequest, callNum int) string
	correctorFn    func(req ggagent.SubAgentRequest, callNum int) string
}

func (m *challengerMockExecutor) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		var res string
		switch {
		case strings.Contains(req.AgentID, "reviewer") || req.AgentID == plan.RoleReviewer:
			c := atomic.AddInt32(&m.reviewCalls, 1)
			if m.reviewFn != nil {
				res = m.reviewFn(req, int(c))
			} else {
				res = `{"approved":true,"score":90,"verdict":"approve"}`
			}
		case strings.Contains(req.AgentID, "corrector") || req.AgentID == plan.RoleCorrector:
			c := atomic.AddInt32(&m.correctorCalls, 1)
			if m.correctorFn != nil {
				res = m.correctorFn(req, int(c))
			} else {
				res = `{"status":"done","summary":"attempted correction"}`
			}
		default:
			atomic.AddInt32(&m.workerCalls, 1)
			res = `{"status":"done","summary":"worker output"}`
		}
		out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Output: res}
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario A: Sycophantic reviewer approving (score: 99, approved: true) a task
// that has broken smoke / syntax / criteria / evidence.
// Does applyHardGates veto it unconditionally?
// ─────────────────────────────────────────────────────────────────────────────

func TestChallenger_ScenarioA_SycophanticReviewerVetoedUnconditionally(t *testing.T) {
	root := t.TempDir()
	r := &Runner{
		Root:       root,
		ClaimsGate: true,
		Log:        func(string, ...interface{}) {},
	}

	task := plan.Task{
		ID:     "TASK_SYCOPHANT",
		Role:   plan.RoleWorker,
		Files:  []string{"server.go"},
		Output: "broken output with errors",
	}

	sycophantReview := plan.ReviewResult{
		Approved: true,
		Score:    99,
		Verdict:  "approve",
		Summary:  "Flawless perfection, code looks extraordinary!",
		Issues:   []string{},
	}

	tests := []struct {
		name          string
		gate          gateState
		wantSummary   string
		wantMaxScore  int
		wantScoreZero bool
	}{
		{
			name:         "Smoke failure overrides score 99 to rejection",
			gate:         gateState{smokeFail: true},
			wantSummary:  "deterministic smoke failed",
			wantMaxScore: 20,
		},
		{
			name:         "Static syntax/placeholder failure overrides score 99",
			gate:         gateState{staticFail: true},
			wantSummary:  "static quality gate",
			wantMaxScore: 20,
		},
		{
			name:         "Criteria failure overrides score 99",
			gate:         gateState{criteriaFail: true},
			wantSummary:  "acceptance criterion failed",
			wantMaxScore: 20,
		},
		{
			name:         "ws_shell execution failure overrides score 99",
			gate:         gateState{shellFail: true},
			wantSummary:  "ws_shell failure evidence",
			wantMaxScore: 20,
		},
		{
			name:         "Missing smoke pass for coding task overrides score 99",
			gate:         gateState{smokeMissing: true},
			wantSummary:  "missing deterministic smoke pass",
			wantMaxScore: 20,
		},
		{
			name:         "Claims failure (hallucinated paths) overrides score 99",
			gate:         gateState{claimsFail: true},
			wantSummary:  "hallucinated files_changed paths",
			wantMaxScore: 20,
		},
		{
			name:          "Evidence missing overrides score 99 and sets score to 0",
			gate:          gateState{evidenceMissing: true, evidenceWhy: "no write on disk"},
			wantSummary:   "evidence gate: no write on disk",
			wantMaxScore:  0,
			wantScoreZero: true,
		},
		{
			name: "Compound failures: smoke + criteria + evidenceMissing all fail",
			gate: gateState{
				smokeFail:       true,
				criteriaFail:    true,
				evidenceMissing: true,
				evidenceWhy:     "target file untouched",
			},
			wantSummary:   "evidence gate",
			wantMaxScore:  0,
			wantScoreZero: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := r.applyHardGates(&task, sycophantReview, tc.gate, nil)

			if res.Approved {
				t.Fatalf("FAILED: sycophantic approval was NOT vetoed! Approved=true, Score=%d", res.Score)
			}
			if res.Score > tc.wantMaxScore {
				t.Errorf("expected score <= %d, got %d", tc.wantMaxScore, res.Score)
			}
			if tc.wantScoreZero && res.Score != 0 {
				t.Errorf("expected score == 0 for missing evidence, got %d", res.Score)
			}
			if !strings.Contains(res.Summary, tc.wantSummary) {
				t.Errorf("expected summary to contain %q, got %q", tc.wantSummary, res.Summary)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario B: Worker output with empty or non-existent edits on focus files.
// Does decideReview fast-reject WITHOUT invoking the reviewer LLM?
// ─────────────────────────────────────────────────────────────────────────────

func TestChallenger_ScenarioB_EmptyOrNonExistentEditsFastReject(t *testing.T) {
	root := t.TempDir()
	mock := &challengerMockExecutor{}
	r := &Runner{
		Root:         root,
		ClaimsGate:   true,
		Executor:     mock,
		ReviewerRole: plan.RoleReviewer,
		Log:          func(string, ...interface{}) {},
	}

	// Case 1: Target file exists on disk, but worker made 0 changes (identical to baseline).
	// Task is an edit task ("Update auth logic").
	targetFile := filepath.Join(root, "auth.go")
	if err := os.WriteFile(targetFile, []byte("package auth\nfunc Login() bool { return false }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	taskUntouched := plan.Task{
		ID:          "TASK_UNTOUCHED",
		Role:        plan.RoleWorker,
		Title:       "Update auth login",
		Description: "implement true return",
		Files:       []string{"auth.go"},
		Output:      `{"status":"done","summary":"updated login"}`,
	}
	baseline := r.snapshotTargets(taskUntouched)

	g := r.gatherGateSignals(context.Background(), &taskUntouched, baseline)
	if !g.evidenceMissing {
		t.Fatalf("expected evidenceMissing=true when file is untouched since snapshot")
	}

	mock.reviewCalls = 0
	res, _, err := r.decideReview(context.Background(), taskUntouched, g)
	if err != nil {
		t.Fatalf("unexpected error from decideReview: %v", err)
	}
	if res.Approved {
		t.Errorf("expected decideReview to fast-reject, got Approved=true")
	}
	if res.Score != 0 {
		t.Errorf("expected score 0 for missing evidence, got %d", res.Score)
	}
	if !strings.Contains(res.Summary, "evidence gate") {
		t.Errorf("expected evidence gate mention in summary, got %q", res.Summary)
	}
	if atomic.LoadInt32(&mock.reviewCalls) != 0 {
		t.Fatalf("CRITICAL: decideReview invoked reviewer LLM (%d times) despite missing evidence!", mock.reviewCalls)
	}

	// Case 2: Worker claims non-existent file ("ghost.go").
	taskGhost := plan.Task{
		ID:          "TASK_GHOST",
		Role:        plan.RoleWorker,
		Title:       "Create ghost file",
		Description: "create ghost file",
		Files:       []string{"ghost.go"},
		Output:      `{"status":"done","summary":"created ghost"}`,
	}
	gGhost := r.gatherGateSignals(context.Background(), &taskGhost, map[string]string{})
	if !gGhost.evidenceMissing {
		t.Fatalf("expected evidenceMissing=true for non-existent target")
	}

	mock.reviewCalls = 0
	resGhost, _, err := r.decideReview(context.Background(), taskGhost, gGhost)
	if err != nil {
		t.Fatalf("decideReview failed: %v", err)
	}
	if resGhost.Approved {
		t.Errorf("expected ghost task to be fast-rejected, got Approved=true")
	}
	if atomic.LoadInt32(&mock.reviewCalls) != 0 {
		t.Fatalf("CRITICAL: reviewer LLM called for ghost file!")
	}

	// Case 3: Empty worker output.
	taskEmpty := plan.Task{
		ID:          "TASK_EMPTY",
		Role:        plan.RoleWorker,
		Title:       "Edit auth",
		Description: "edit auth.go",
		Files:       []string{"auth.go"},
		Output:      "",
	}
	gEmpty := r.gatherGateSignals(context.Background(), &taskEmpty, baseline)
	mock.reviewCalls = 0
	resEmpty, _, err := r.decideReview(context.Background(), taskEmpty, gEmpty)
	if err != nil {
		t.Fatalf("decideReview failed on empty output: %v", err)
	}
	if resEmpty.Approved {
		t.Errorf("expected empty output to be rejected")
	}
	if atomic.LoadInt32(&mock.reviewCalls) != 0 {
		t.Fatalf("CRITICAL: reviewer LLM called for empty worker output!")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario C: Worker output that claims out-of-scope files.
// Does decideReview fast-reject with scope violation WITHOUT calling LLM?
// ─────────────────────────────────────────────────────────────────────────────

func TestChallenger_ScenarioC_OutOfScopeEditsFastReject(t *testing.T) {
	root := t.TempDir()
	mock := &challengerMockExecutor{}
	r := &Runner{
		Root:         root,
		Focus:        workspace.NewFocusGuard(),
		Executor:     mock,
		ReviewerRole: plan.RoleReviewer,
		Log:          func(string, ...interface{}) {},
	}

	task := plan.Task{
		ID:          "TASK_SCOPE_VIOLATOR",
		Role:        plan.RoleWorker,
		Title:       "Modify pkg/calc/calc.go only",
		Description: "stay strictly in focus",
		Files:       []string{"pkg/calc/calc.go"},
		Output:      `{"status":"done","files_changed":["cmd/slmcode/main.go","pkg/calc/calc.go"]}`,
	}
	baseline := map[string]string{"pkg/calc/calc.go": "1:abc"}

	g := r.gatherGateSignals(context.Background(), &task, baseline)
	if g.scopeWhy == "" {
		t.Fatalf("expected scopeWhy to be non-empty when cmd/slmcode/main.go is claimed")
	}
	if !g.blocking() {
		t.Fatalf("expected blocking()=true for scope violation")
	}

	mock.reviewCalls = 0
	res, _, err := r.decideReview(context.Background(), task, g)
	if err != nil {
		t.Fatalf("decideReview error: %v", err)
	}
	if res.Approved {
		t.Fatalf("CRITICAL: scope violation was approved!")
	}
	if !strings.Contains(res.Summary, "scope violation") {
		t.Errorf("expected summary to state scope violation, got %q", res.Summary)
	}
	if atomic.LoadInt32(&mock.reviewCalls) != 0 {
		t.Fatalf("CRITICAL: decideReview invoked reviewer LLM on scope violation (calls: %d)!", mock.reviewCalls)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario D: Corrector attempt score sequence that degrades (e.g. 70 -> 50).
// Does correctionRegressed interrupt the loop immediately?
// ─────────────────────────────────────────────────────────────────────────────

func TestChallenger_ScenarioD_ScoreRegressionInterruption(t *testing.T) {
	// Step 1: Unit matrix test for correctionRegressed
	matrix := []struct {
		scores []int
		want   bool
		desc   string
	}{
		{[]int{}, false, "empty scores slice"},
		{[]int{70}, false, "single initial score"},
		{[]int{70, 70}, false, "flat score (same quality)"},
		{[]int{70, 80}, false, "improving score"},
		{[]int{70, 50}, true, "Scenario D: 70 -> 50 degradation"},
		{[]int{50, 60, 45}, true, "multi-round degradation (60 -> 45)"},
		{[]int{50, 45, 60}, false, "recovered in last round (45 -> 60)"},
		{[]int{90, 89}, true, "marginal degradation (90 -> 89)"},
		{[]int{0, 0}, false, "flat zero scores"},
		{[]int{20, 0}, true, "drop to zero"},
	}

	for _, tc := range matrix {
		got := correctionRegressed(tc.scores)
		if got != tc.want {
			t.Errorf("correctionRegressed(%v) = %v, want %v (%s)", tc.scores, got, tc.want, tc.desc)
		}
	}

	// Step 2: Integrated loop test with reviewAndCorrectTask
	root := t.TempDir()
	targetFile := filepath.Join(root, "service.go")
	if err := os.WriteFile(targetFile, []byte("package service\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mock := &challengerMockExecutor{
		reviewFn: func(_ ggagent.SubAgentRequest, callNum int) string {
			if callNum == 1 {
				// Attempt 0: initial review scores 70
				return `{"approved":false,"score":70,"verdict":"reject","summary":"missing error handling"}`
			}
			// Attempt 1: corrector made it worse! Score degrades to 50
			return `{"approved":false,"score":50,"verdict":"reject","summary":"broken error handling and new panic"}`
		},
		correctorFn: func(_ ggagent.SubAgentRequest, _ int) string {
			// Simulate corrector modifying service.go
			_ = os.WriteFile(targetFile, []byte("package service\nfunc Run() { panic(1) }\n"), 0o644)
			return `{"status":"done","files_changed":["service.go"],"summary":"attempted fix"}`
		},
	}

	r := &Runner{
		Root:          root,
		MaxRetries:    4, // High retry ceiling
		ReviewerRole:  plan.RoleReviewer,
		CorrectorRole: plan.RoleCorrector,
		Executor:      mock,
		Log:           func(string, ...interface{}) {},
	}

	task := plan.Task{
		ID:          "TASK_REGRESS",
		Role:        plan.RoleWorker,
		Title:       "Add error handling to service.go",
		Description: "handle errors cleanly",
		Files:       []string{"service.go"},
		Output:      `{"status":"done","files_changed":["service.go"]}`,
		Criteria: []plan.Criterion{
			{ID: "AC1", Text: "Adversarial review verification", Priority: plan.PriorityMust},
		},
	}
	baseline := map[string]string{"service.go": "1:initial"}

	finalTask, esc, err := r.reviewAndCorrectTask(context.Background(), task, baseline)
	if err != nil {
		t.Fatalf("unexpected error from reviewAndCorrectTask: %v", err)
	}
	if esc == nil {
		t.Fatalf("expected task to be escalated immediately on regression!")
	}

	// Total reviews MUST be exactly 2 (Attempt 0 + Attempt 1), NOT 5 (MaxRetries=4)!
	revCalls := atomic.LoadInt32(&mock.reviewCalls)
	if revCalls != 2 {
		t.Errorf("expected exactly 2 review calls before halting, got %d", revCalls)
	}
	// Corrector must have been called exactly 1 time
	corrCalls := atomic.LoadInt32(&mock.correctorCalls)
	if corrCalls != 1 {
		t.Errorf("expected exactly 1 corrector call, got %d", corrCalls)
	}
	// Escalated task must move to ColToScope
	if finalTask.Column != plan.ColToScope {
		t.Errorf("expected final task column to be %q, got %q", plan.ColToScope, finalTask.Column)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario E: An adversarial reviewer that returns `"verdict": "veto"`.
// Does the loop immediately halt retries and escalate?
// ─────────────────────────────────────────────────────────────────────────────

func TestChallenger_ScenarioE_AdversarialVetoHaltsImmediately(t *testing.T) {
	root := t.TempDir()
	targetFile := filepath.Join(root, "crypto.go")
	if err := os.WriteFile(targetFile, []byte("package crypto\nfunc Hash() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Subtest E1: Reviewer returns contradictory {"approved": true, "verdict": "veto"}
	t.Run("Contradictory Approved:true with Verdict:veto is overridden", func(t *testing.T) {
		r := &Runner{Root: root, Log: func(string, ...interface{}) {}}
		review := plan.ReviewResult{
			Approved: true,
			Score:    95,
			Verdict:  "veto",
			Summary:  "critical cryptographic flaw",
		}
		g := gateState{}
		res := r.applyHardGates(&plan.Task{ID: "T_VETO_1"}, review, g, nil)
		if res.Approved {
			t.Fatalf("expected applyHardGates to override Approved=true on verdict=veto")
		}
		if res.Score > 20 {
			t.Errorf("expected score capped at 20, got %d", res.Score)
		}
	})

	// Subtest E2: Full loop interruption on attempt 0
	t.Run("Immediate escalation on attempt 0 veto with MaxRetries=5", func(t *testing.T) {
		mock := &challengerMockExecutor{
			reviewFn: func(_ ggagent.SubAgentRequest, _ int) string {
				return `{"approved":false,"score":15,"verdict":"veto","summary":"unacceptable security vulnerability","issues":["insecure PRNG"]}`
			},
		}

		r := &Runner{
			Root:          root,
			MaxRetries:    5,
			ReviewerRole:  plan.RoleReviewer,
			CorrectorRole: plan.RoleCorrector,
			Executor:      mock,
			Log:           func(string, ...interface{}) {},
		}

		task := plan.Task{
			ID:          "TASK_CRYPTO_VETO",
			Role:        plan.RoleWorker,
			Title:       "Implement crypto hashing",
			Description: "secure hash implementation",
			Files:       []string{"crypto.go"},
			Output:      `{"status":"done","files_changed":["crypto.go"]}`,
			Criteria: []plan.Criterion{
				{ID: "AC1", Text: "Adversarial security review", Priority: plan.PriorityMust},
			},
		}
		baseline := map[string]string{"crypto.go": "1:initial"}

		finalTask, esc, err := r.reviewAndCorrectTask(context.Background(), task, baseline)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if esc == nil {
			t.Fatalf("expected immediate escalation on adversarial veto")
		}

		// Must have been reviewed EXACTLY 1 time
		revCalls := atomic.LoadInt32(&mock.reviewCalls)
		if revCalls != 1 {
			t.Errorf("expected exactly 1 review call, got %d", revCalls)
		}
		// Corrector must NEVER have been called!
		corrCalls := atomic.LoadInt32(&mock.correctorCalls)
		if corrCalls != 0 {
			t.Errorf("expected 0 corrector calls on unrecoverable veto, got %d", corrCalls)
		}
		if finalTask.Column != plan.ColToScope {
			t.Errorf("expected column %q, got %q", plan.ColToScope, finalTask.Column)
		}
	})

	// Subtest E3: Case-insensitive veto ("VETO" and "Veto")
	t.Run("Case-insensitive veto handling", func(t *testing.T) {
		for _, v := range []string{"VETO", "Veto", "veto", "REJECT", "reject"} {
			review := plan.ReviewResult{
				Approved: true,
				Score:    85,
				Verdict:  v,
				Summary:  "verdict test",
			}
			r := &Runner{Root: root, Log: func(string, ...interface{}) {}}
			res := r.applyHardGates(&plan.Task{ID: "T_CASE"}, review, gateState{}, nil)
			if res.Approved {
				t.Errorf("expected verdict %q to override Approved=true", v)
			}
			if res.Score > 20 {
				t.Errorf("expected score <= 20 for verdict %q, got %d", v, res.Score)
			}
		}
	})

	// Subtest E4: Contrast Veto (immediate halt) vs Reject (allows corrector retries)
	t.Run("Contrast Veto halts vs Reject allows corrector retries", func(t *testing.T) {
		mockReject := &challengerMockExecutor{
			reviewFn: func(_ ggagent.SubAgentRequest, callNum int) string {
				if callNum == 1 {
					// Attempt 0: normal rejection (not veto)
					return `{"approved":false,"score":60,"verdict":"reject","summary":"ordinary bug to fix"}`
				}
				// Attempt 1: corrector fixed it
				return `{"approved":true,"score":90,"verdict":"approve","summary":"looks good now"}`
			},
			correctorFn: func(_ ggagent.SubAgentRequest, _ int) string {
				return `{"status":"done","files_changed":["crypto.go"],"summary":"fixed bug"}`
			},
		}

		r := &Runner{
			Root:          root,
			MaxRetries:    3,
			ReviewerRole:  plan.RoleReviewer,
			CorrectorRole: plan.RoleCorrector,
			Executor:      mockReject,
			Log:           func(string, ...interface{}) {},
		}

		task := plan.Task{
			ID:          "TASK_CONTRAST",
			Role:        plan.RoleWorker,
			Title:       "Ordinary task",
			Description: "ordinary task",
			Files:       []string{"crypto.go"},
			Output:      `{"status":"done","files_changed":["crypto.go"]}`,
			Criteria: []plan.Criterion{
				{ID: "AC1", Text: "Adversarial review", Priority: plan.PriorityMust},
			},
		}
		baseline := map[string]string{"crypto.go": "1:initial"}

		finalTask, esc, err := r.reviewAndCorrectTask(context.Background(), task, baseline)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if esc != nil {
			t.Fatalf("expected task with ordinary rejection to succeed on attempt 1, but got escalation: %v", esc)
		}
		if finalTask.Column != plan.ColDone {
			t.Errorf("expected task to be marked Done, got %q", finalTask.Column)
		}

		// In ordinary reject, corrector WAS called
		corrCalls := atomic.LoadInt32(&mockReject.correctorCalls)
		if corrCalls != 1 {
			t.Errorf("expected corrector to be called 1 time for reject, got %d", corrCalls)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Additional Adversarial Hardening:
// 1. Evidence gate fallback when ClaimsGate is false
// 2. Non-regressing flat score sequence executes full retry budget
// ─────────────────────────────────────────────────────────────────────────────

func TestChallenger_EvidenceGateFallbackWhenClaimsGateDisabled(t *testing.T) {
	root := t.TempDir()
	r := &Runner{
		Root:       root,
		ClaimsGate: false, // ClaimsGate disabled (synthetic runner mode)
		Log:        func(string, ...interface{}) {},
	}

	if err := os.WriteFile(filepath.Join(root, "unwritten.go"), []byte("package unwritten\nfunc Unchanged() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	task := plan.Task{
		ID:          "T_NOCLAIMS",
		Role:        plan.RoleWorker,
		Title:       "Edit unwritten.go",
		Description: "implement changes in unwritten.go",
		Files:       []string{"unwritten.go"},
		Output:      `{"status":"done","summary":"all done"}`,
	}
	baseline := r.snapshotTargets(task)

	// With ClaimsGate false, gatherGateSignals will NOT set evidenceMissing
	g := r.gatherGateSignals(context.Background(), &task, baseline)
	if g.evidenceMissing {
		t.Errorf("expected evidenceMissing=false when ClaimsGate=false")
	}

	// Sycophant reviewer approves
	sycophant := plan.ReviewResult{
		Approved: true,
		Score:    95,
		Verdict:  "approve",
		Summary:  "Approved by synthetic reviewer",
	}

	// applyHardGates MUST still catch the missing evidence on disk!
	res := r.applyHardGates(&task, sycophant, g, baseline)
	if res.Approved {
		t.Fatalf("CRITICAL: applyHardGates failed to veto missing evidence when ClaimsGate=false!")
	}
	if res.Score != 0 {
		t.Errorf("expected score 0, got %d", res.Score)
	}
	if !strings.Contains(res.Summary, "evidence gate") {
		t.Errorf("expected summary to mention evidence gate, got: %s", res.Summary)
	}
}

func TestChallenger_NonRegressingScoreContinuesLadderUntilBudget(t *testing.T) {
	root := t.TempDir()
	targetFile := filepath.Join(root, "steady.go")
	if err := os.WriteFile(targetFile, []byte("package steady\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Flat scores: 60, 60, 60, 60 (never regresses, but never reaches approval)
	mock := &challengerMockExecutor{
		reviewFn: func(_ ggagent.SubAgentRequest, _ int) string {
			return `{"approved":false,"score":60,"verdict":"reject","summary":"steady quality but incomplete"}`
		},
		correctorFn: func(_ ggagent.SubAgentRequest, _ int) string {
			_ = os.WriteFile(targetFile, []byte("package steady\n// edit\n"), 0o644)
			return `{"status":"done","files_changed":["steady.go"],"summary":"attempted fix"}`
		},
	}

	r := &Runner{
		Root:          root,
		MaxRetries:    3, // 0, 1, 2, 3 = 4 total attempts
		ReviewerRole:  plan.RoleReviewer,
		CorrectorRole: plan.RoleCorrector,
		Executor:      mock,
		Log:           func(string, ...interface{}) {},
	}

	task := plan.Task{
		ID:          "TASK_STEADY",
		Role:        plan.RoleWorker,
		Title:       "Steady task",
		Description: "steady task",
		Files:       []string{"steady.go"},
		Output:      `{"status":"done","files_changed":["steady.go"]}`,
		Criteria: []plan.Criterion{
			{ID: "AC1", Text: "Adversarial review", Priority: plan.PriorityMust},
		},
	}
	baseline := map[string]string{"steady.go": "1:initial"}

	_, esc, err := r.reviewAndCorrectTask(context.Background(), task, baseline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if esc == nil {
		t.Fatalf("expected task to escalate when max retries are exhausted")
	}

	// Since score never regressed (60 == 60), it should have exhausted all 4 attempts (0, 1, 2, 3)
	revCalls := atomic.LoadInt32(&mock.reviewCalls)
	if revCalls != 4 {
		t.Errorf("expected 4 review calls before budget exhaustion, got %d", revCalls)
	}
	corrCalls := atomic.LoadInt32(&mock.correctorCalls)
	if corrCalls != 3 {
		t.Errorf("expected 3 corrector calls, got %d", corrCalls)
	}
}
