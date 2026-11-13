package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

type mockSubAgentExec struct {
	fn func(agentID, input string) string
}

func (m *mockSubAgentExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	var res []ggagent.SubAgentResult
	for _, req := range reqs {
		out := `{"ok":true}`
		if m.fn != nil {
			out = m.fn(req.AgentID, req.Input)
		}
		res = append(res, ggagent.SubAgentResult{Output: out})
	}
	return res, nil
}

func setupTestOrchestrator(t *testing.T, exec *mockSubAgentExec) *Orchestrator {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	pipe := pipeline.Teamwork()
	pipe.Normalize()

	store := contextstore.New(cfg.SlmDir())
	o := &Orchestrator{
		cfg:        cfg,
		store:      store,
		pipe:       &pipe,
		factory:    agents.NewFactory(nil, nil, "", ""),
		executor:   exec,
		shared:     ggagent.NewSharedState(),
		focus:      workspace.NewFocusGuard(),
		boardStore: plan.NewLiveStore(cfg.SlmDir()),
	}
	return o
}

func TestClarifyPhaseTransitionsAndLocksDocPRD(t *testing.T) {
	specJSON := `{
		"needs_user": false,
		"assumptions": ["Fast in-memory cache"],
		"acceptance": ["go test ./pkg/cache/... passes"],
		"language": "go",
		"entrypoint": "main.go",
		"features": [
			{
				"id": "F1",
				"title": "LRU Eviction",
				"description": "Evict oldest keys on capacity limit",
				"acceptance": "go test ./pkg/cache/ -run TestLRU",
				"priority": "must"
			}
		],
		"non_goals": ["Distributed Redis sync"],
		"questions": []
	}`

	var invokedAgent string
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			invokedAgent = agentID
			return specJSON
		},
	}
	o := setupTestOrchestrator(t, exec)
	o.cfg.ClarifyMode = plan.ClarifyAuto

	interview := o.runScopeInterview(context.Background(), "build an in-memory cache", "files: cache.go")

	if invokedAgent != "teamwork-spec" {
		t.Errorf("expected clarify to invoke teamwork-spec, got %q", invokedAgent)
	}
	if len(interview.Features) != 1 || interview.Features[0].ID != "F1" {
		t.Fatalf("unexpected interview features: %+v", interview.Features)
	}

	// Verify DocPRD (PRD.md) was written to disk
	prdContent, err := o.store.Read(contextstore.DocPRD)
	if err != nil || prdContent == "" {
		t.Fatalf("expected DocPRD (PRD.md) to be written, err=%v, content=%q", err, prdContent)
	}
	if !strings.Contains(prdContent, "### Features") || !strings.Contains(prdContent, "**[F1] LRU Eviction**") {
		t.Errorf("PRD.md missing features section:\n%s", prdContent)
	}
}

func TestPlanPhaseDecomposesContractsAndMilestones(t *testing.T) {
	planJSON := `{
		"strategy": "Three-tier architecture",
		"milestones": [
			{
				"id": "M1",
				"title": "Database Schema",
				"description": "Define users table",
				"acceptance": "go test ./pkg/db/...",
				"depends_on": []
			}
		],
		"file_ownership": [
			{"owner": "teamwork-worker", "paths": ["pkg/db/db.go"]}
		],
		"contracts": [
			{
				"interface_id": "IUserStore",
				"provider": "pkg/db",
				"consumers": ["pkg/auth"],
				"spec": "type UserStore interface { Find(id string) (*User, error) }"
			}
		],
		"risks": ["Connection pool exhaustion"]
	}`

	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return planJSON
		},
	}
	o := setupTestOrchestrator(t, exec)

	in := planSplitInput{
		RunID:      "test-run",
		Query:      "create user store",
		ExploreOut: "existing files",
		PRD:        plan.ScopePRD{Summary: "user store"},
	}

	_, pl, err := o.runPlanPhase(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("runPlanPhase failed: %v", err)
	}
	if pl.Summary != "Three-tier architecture" {
		t.Errorf("unexpected plan summary: %q", pl.Summary)
	}

	// Verify CONTRACT.md (DocContract) was frozen
	contractMD, err := o.store.Read(contextstore.DocContract)
	if err != nil || contractMD == "" {
		t.Fatalf("expected DocContract (CONTRACT.md) to be written, err=%v", err)
	}
	if !strings.Contains(contractMD, "# Interface Contracts") || !strings.Contains(contractMD, "IUserStore") {
		t.Errorf("CONTRACT.md missing contract details:\n%s", contractMD)
	}
}

func TestSplitPhaseGeneratesStructuredCriteria(t *testing.T) {
	tasksJSON := `{"tasks":[
		{
			"id": "T1",
			"title": "Implement User Model",
			"role": "teamwork-worker",
			"files": ["pkg/user/user.go"],
			"acceptance": "go test ./pkg/user/... -run TestUserModel",
			"dependencies": []
		}
	]}`

	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return tasksJSON
		},
	}
	o := setupTestOrchestrator(t, exec)

	in := planSplitInput{
		RunID:      "test-run",
		Query:      "implement user model",
		ExploreOut: "pkg/user/user.go",
		PRD:        plan.ScopePRD{Summary: "user model"},
	}
	pl := plan.Plan{Summary: "plan", Steps: []string{"Step 1", "Step 2"}}

	board, err := o.runSplitPhase(context.Background(), in, pl, `{"steps":["Step 1","Step 2"]}`, nil)
	if err != nil {
		t.Fatalf("runSplitPhase failed: %v", err)
	}
	if len(board.Tasks) == 0 {
		t.Fatal("expected at least 1 task on board")
	}

	t1 := board.Tasks[0]
	if len(t1.Criteria) == 0 {
		t.Fatalf("expected Task.Criteria to be populated, got 0")
	}
	if !t1.Criteria[0].Blocking() {
		t.Errorf("expected Criterion to be Blocking (PriorityMust), got %q", t1.Criteria[0].Priority)
	}
	if t1.Criteria[0].Verify == "" {
		t.Errorf("expected Criterion.Verify to be populated, got empty")
	}
}

func TestFinalizePersistsDocReview(t *testing.T) {
	exec := &mockSubAgentExec{}
	o := setupTestOrchestrator(t, exec)

	board := &plan.Board{
		QueryID: "test-run",
		Query:   "finalize review test",
		Tasks: []plan.Task{
			{ID: "T1", Title: "Task 1", Status: plan.StatusDone, Column: plan.ColDone},
		},
	}

	testOut := `{"passed":true,"summary":"all 12 unit tests pass","commands":["go test ./..."],"failures":[]}`
	start := time.Now()

	_, err := o.completeRun(context.Background(), "test-run", "test query", "", board, testOut, false, false, "go test ./...", start)
	if err != nil {
		t.Fatalf("completeRun failed: %v", err)
	}

	// Verify DocReview (REVIEW.md) was written to disk
	reviewContent, err := o.store.Read(contextstore.DocReview)
	if err != nil || reviewContent == "" {
		t.Fatalf("expected DocReview (REVIEW.md) to be written, err=%v, content=%q", err, reviewContent)
	}
	if !strings.Contains(reviewContent, "# Verification & Adversarial Review Report") ||
		!strings.Contains(reviewContent, "all 12 unit tests pass") {
		t.Errorf("REVIEW.md missing expected report content:\n%s", reviewContent)
	}
}

func TestResumeReloadsDurableDocs(t *testing.T) {
	exec := &mockSubAgentExec{}
	o := setupTestOrchestrator(t, exec)

	slmDir := o.cfg.SlmDir()
	if err := os.MkdirAll(slmDir, 0o755); err != nil {
		t.Fatal(err)
	}

	prdExpected := "# Locked PRD\n\nFeature 1 must pass."
	contractExpected := "# Interface Contracts\n\nInterface IAuth."
	reviewExpected := "# Verification & Adversarial Review Report\n\nAll tests pass."
	if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocPRD), []byte(prdExpected), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocContract), []byte(contractExpected), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocReview), []byte(reviewExpected), 0o644); err != nil {
		t.Fatal(err)
	}

	// Execute o.reloadDurableDocs() directly against real *Orchestrator instance
	o.reloadDurableDocs()

	prdVal, ok := o.shared.GetGlobal("prd")
	if !ok || prdVal != prdExpected {
		t.Errorf("prd global = %q (ok=%v), want %q", prdVal, ok, prdExpected)
	}
	contractVal, ok := o.shared.GetGlobal("contract")
	if !ok || contractVal != contractExpected {
		t.Errorf("contract global = %q (ok=%v), want %q", contractVal, ok, contractExpected)
	}
	reviewVal, ok := o.shared.GetGlobal("review")
	if !ok || reviewVal != reviewExpected {
		t.Errorf("review global = %q (ok=%v), want %q", reviewVal, ok, reviewExpected)
	}

	prdStored, err := o.store.Read(contextstore.DocPRD)
	if err != nil || prdStored != prdExpected {
		t.Errorf("prd stored = %q, want %q (err=%v)", prdStored, prdExpected, err)
	}
	contractStored, err := o.store.Read(contextstore.DocContract)
	if err != nil || contractStored != contractExpected {
		t.Errorf("contract stored = %q, want %q (err=%v)", contractStored, contractExpected, err)
	}
	reviewStored, err := o.store.Read(contextstore.DocReview)
	if err != nil || reviewStored != reviewExpected {
		t.Errorf("review stored = %q, want %q (err=%v)", reviewStored, reviewExpected, err)
	}

	// Verify nil safety
	var nilOrch *Orchestrator
	nilOrch.reloadDurableDocs()

	nilCfgOrch := &Orchestrator{}
	nilCfgOrch.reloadDurableDocs()
}

func TestTesterPhasePreTestSupremacyOverridesTesterPass(t *testing.T) {
	// Tester returns passed: true with shell execution evidence, but deterministic pre-test was red and recheck remains red.
	testerPassJSON := "exit code: 0\n" + `{"passed":true,"summary":"all good","commands":["go test ./pkg/nonexistent/..."],"failures":[]}`
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return testerPassJSON
		},
	}
	o := setupTestOrchestrator(t, exec)

	board := &plan.Board{
		QueryID: "test-run",
		Query:   "run pretest supremacy test",
		Tasks: []plan.Task{
			{ID: "T1", Title: "Failing test task", Status: plan.StatusReady, Column: plan.ColReadyToDev},
		},
	}

	pre := preTest{
		Cmd:      "go test ./pkg/nonexistent/...",
		OK:       false,
		Ran:      true,
		FailJSON: `{"failed_commands":["go test ./pkg/nonexistent/..."]}`,
	}

	verdict, err := o.runTesterPhase(context.Background(), "test query", board, pre)
	if err != nil {
		t.Fatalf("runTesterPhase failed: %v", err)
	}

	tr := plan.ParseTesterJSON(verdict.Output)
	if tr.Passed {
		t.Errorf("expected deterministic pre-test failure to override tester pass, but got passed=true")
	}
	if !strings.Contains(verdict.Output, "deterministic re-test still failing") {
		t.Errorf("expected deterministic re-test failure notice in output, got: %s", verdict.Output)
	}
}

func TestTesterPhaseDiskEvidenceSupremacyClearsRejection(t *testing.T) {
	// Mock subagent returns tester failure (passed=false), but disk rename evidence must prevail
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return `{"passed":false,"summary":"tester thought file was missing","failures":["not found"]}`
		},
	}
	o := setupTestOrchestrator(t, exec)

	// Create real file on disk in o.cfg.Root simulating a satisfied rename
	newFile := filepath.Join(o.cfg.Root, "new_name.go")
	if err := os.WriteFile(newFile, []byte("package test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	board := &plan.Board{
		QueryID: "rename-run",
		Query:   "rename old_name.go to new_name.go",
		Tasks: []plan.Task{
			{
				ID:     "T1",
				Title:  "rename old_name.go to new_name.go",
				Files:  []string{"new_name.go"},
				Status: plan.StatusDone,
				Column: plan.ColInReview,
			},
		},
	}

	// Confirm renameDiskOK recognizes rename on disk
	if !renameDiskOK(o.cfg.Root, board.Query, board) {
		t.Fatalf("expected renameDiskOK to return true for verified disk rename")
	}

	// Execute actual production tester phase
	verdict, err := o.runTesterPhase(context.Background(), board.Query, board, preTest{})
	if err != nil {
		t.Fatalf("runTesterPhase failed: %v", err)
	}

	if !verdict.FromDisk {
		t.Errorf("expected verdict.FromDisk to be true for verified disk rename")
	}
	tr := plan.ParseTesterJSON(verdict.Output)
	if !tr.Passed {
		t.Errorf("expected disk evidence supremacy to override tester rejection, but got passed=false: %s", verdict.Output)
	}
	if !strings.Contains(verdict.Output, "rename verified on disk") {
		t.Errorf("expected summary to indicate rename verified on disk, got: %s", verdict.Output)
	}
}
