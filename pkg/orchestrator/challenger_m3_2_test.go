package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// TestChallenger_ClarifyPhaseMultiFeaturesAndCeiling stress-tests clarify interview
// with a complex 10-feature breakdown, checks PRD.md persistence, and asserts bounded ceilings.
func TestChallenger_ClarifyPhaseMultiFeaturesAndCeiling(t *testing.T) {
	// Construct 10 features with varied priorities and acceptance tests
	var featuresJSON strings.Builder
	for i := 1; i <= 10; i++ {
		var priority string
		switch i % 3 {
		case 1:
			priority = "should"
		case 2:
			priority = "could"
		default:
			priority = "must"
		}
		if i > 1 {
			featuresJSON.WriteString(",")
		}
		fmt.Fprintf(&featuresJSON, `{
			"id": "F%02d",
			"title": "Feature Number %d",
			"description": "Comprehensive specification for feature %d under multi-agent harness",
			"acceptance": "go test ./pkg/feature%d/... -run TestFeature%d",
			"priority": "%s"
		}`, i, i, i, i, i, priority)
	}

	specJSON := fmt.Sprintf(`{
		"needs_user": false,
		"assumptions": ["Fast in-memory bus", "POSIX filesystem semantics"],
		"acceptance": ["go test ./... passes cleanly"],
		"language": "go",
		"entrypoint": "cmd/server/main.go",
		"features": [%s],
		"non_goals": ["Cloud deployment", "Distributed Raft consensus"],
		"questions": []
	}`, featuresJSON.String())

	var invokedAgent string
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			invokedAgent = agentID
			return specJSON
		},
	}

	o := setupTestOrchestrator(t, exec)
	o.cfg.ClarifyMode = plan.ClarifyAuto

	interview := o.runScopeInterview(context.Background(), "build enterprise multi-feature engine", "files: engine.go")

	if invokedAgent != "teamwork-spec" {
		t.Fatalf("expected clarify agent teamwork-spec, got %q", invokedAgent)
	}

	if len(interview.Features) != 10 {
		t.Fatalf("expected 10 features parsed, got %d", len(interview.Features))
	}

	// Verify DocPRD (PRD.md) on disk
	prdContent, err := o.store.Read(contextstore.DocPRD)
	if err != nil || prdContent == "" {
		t.Fatalf("expected DocPRD to be written, err=%v", err)
	}

	if !strings.Contains(prdContent, "## Locked PRD") {
		t.Errorf("PRD.md missing '## Locked PRD'")
	}
	if !strings.Contains(prdContent, "### Features") {
		t.Errorf("PRD.md missing '### Features'")
	}
	for i := 1; i <= 10; i++ {
		featMarker := fmt.Sprintf("[F%02d] Feature Number %d", i, i)
		if !strings.Contains(prdContent, featMarker) {
			t.Errorf("PRD.md missing feature marker %q", featMarker)
		}
	}

	// Verify shared global
	prdGlobal, ok := o.shared.GetGlobal("prd")
	if !ok || prdGlobal.(string) != prdContent {
		t.Errorf("shared global 'prd' mismatch: ok=%v, prdGlobal=%v", ok, prdGlobal)
	}

	// Stress-test bounded ceiling on DocPRD (24 KB ceiling)
	// Write content exceeding 24 KB (e.g. 35 KB)
	massivePRD := "# Locked PRD\n\n## Overview\n" + strings.Repeat("Very long requirement details that consume significant storage.\n", 600)
	if len(massivePRD) <= 24*1024 {
		t.Fatalf("massivePRD should exceed 24KB, len=%d", len(massivePRD))
	}

	if err := o.store.WriteBounded(contextstore.DocPRD, massivePRD); err != nil {
		t.Fatalf("WriteBounded failed: %v", err)
	}

	boundedContent, err := o.store.Read(contextstore.DocPRD)
	if err != nil {
		t.Fatalf("failed to read bounded PRD: %v", err)
	}
	if len(boundedContent) > 24*1024 {
		t.Errorf("DocPRD exceeded 24KB ceiling: len=%d, max=%d", len(boundedContent), 24*1024)
	}
	if !strings.HasPrefix(boundedContent, "# Locked PRD") {
		t.Errorf("DocPRD lost its header during ceiling enforcement")
	}
}

// TestChallenger_PlanPhaseMultiContractsAndCeiling stress-tests plan phase with
// 3 distinct Go interface contracts, verifies CONTRACT.md rendering, and asserts bounded ceilings.
func TestChallenger_PlanPhaseMultiContractsAndCeiling(t *testing.T) {
	planJSON := `{
		"strategy": "Domain-Driven Layered Architecture",
		"milestones": [
			{"id": "M1", "title": "Auth Domain", "description": "Token and login", "acceptance": "go test ./pkg/auth/...", "depends_on": []},
			{"id": "M2", "title": "User Domain", "description": "User storage", "acceptance": "go test ./pkg/db/...", "depends_on": ["M1"]}
		],
		"file_ownership": [
			{"owner": "teamwork-worker", "paths": ["pkg/auth/auth.go", "pkg/db/user.go"]}
		],
		"contracts": [
			{
				"interface_id": "IAuthService",
				"provider": "pkg/auth",
				"consumers": ["pkg/api", "pkg/gateway"],
				"spec": "type AuthService interface {\n\tLogin(ctx context.Context, email, pass string) (*Token, error)\n\tVerify(token string) (bool, error)\n}"
			},
			{
				"interface_id": "IUserRepository",
				"provider": "pkg/db",
				"consumers": ["pkg/auth"],
				"spec": "type UserRepository interface {\n\tFindByEmail(ctx context.Context, email string) (*User, error)\n\tCreate(ctx context.Context, u *User) error\n}"
			},
			{
				"interface_id": "ITokenSigner",
				"provider": "pkg/crypto",
				"consumers": ["pkg/auth"],
				"spec": "type TokenSigner interface {\n\tSign(claims Claims) (string, error)\n\tParse(token string) (*Claims, error)\n}"
			}
		],
		"risks": ["Key rotation complexity", "Deadlock on user update"]
	}`

	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return planJSON
		},
	}
	o := setupTestOrchestrator(t, exec)

	in := planSplitInput{
		RunID:      "test-run-contracts",
		Query:      "implement auth and user repository with crypto signing",
		ExploreOut: "pkg/auth/auth.go\npkg/db/user.go",
		PRD:        plan.ScopePRD{Summary: "auth and db"},
	}

	_, pl, err := o.runPlanPhase(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("runPlanPhase failed: %v", err)
	}
	if pl.Summary != "Domain-Driven Layered Architecture" {
		t.Errorf("unexpected plan summary: %q", pl.Summary)
	}

	// Verify CONTRACT.md (DocContract)
	contractContent, err := o.store.Read(contextstore.DocContract)
	if err != nil || contractContent == "" {
		t.Fatalf("expected DocContract to be written, err=%v", err)
	}

	for _, iface := range []string{"IAuthService", "IUserRepository", "ITokenSigner"} {
		if !strings.Contains(contractContent, iface) {
			t.Errorf("CONTRACT.md missing interface %q:\n%s", iface, contractContent)
		}
	}
	if !strings.Contains(contractContent, "pkg/auth") || !strings.Contains(contractContent, "pkg/db") || !strings.Contains(contractContent, "pkg/crypto") {
		t.Errorf("CONTRACT.md missing providers/consumers details")
	}

	// Verify shared global
	contractGlobal, ok := o.shared.GetGlobal("contract")
	if !ok || contractGlobal != contractContent {
		t.Errorf("shared global 'contract' mismatch: ok=%v", ok)
	}

	// Stress-test bounded ceiling on DocContract (16 KB ceiling)
	massiveContract := "# Interface Contracts\n\n" + strings.Repeat("type StubInterface interface {\n\tDoSomething()\n}\n", 500)
	if len(massiveContract) <= 16*1024 {
		t.Fatalf("massiveContract should exceed 16KB, len=%d", len(massiveContract))
	}

	if err := o.store.WriteBounded(contextstore.DocContract, massiveContract); err != nil {
		t.Fatalf("WriteBounded failed: %v", err)
	}

	boundedContract, err := o.store.Read(contextstore.DocContract)
	if err != nil {
		t.Fatalf("failed to read bounded contract: %v", err)
	}
	if len(boundedContract) > 16*1024 {
		t.Errorf("DocContract exceeded 16KB ceiling: len=%d, max=%d", len(boundedContract), 16*1024)
	}
}

// TestChallenger_SplitPhaseStructuredCriteriaGeneration stress-tests Task.Criteria
// generation across various task acceptance styles.
func TestChallenger_SplitPhaseStructuredCriteriaGeneration(t *testing.T) {
	tasksJSON := `{"tasks":[
		{
			"id": "T1",
			"title": "Implement User Model",
			"role": "teamwork-worker",
			"files": ["pkg/user/user.go"],
			"acceptance": "go test ./pkg/user/... -run TestUserModel",
			"dependencies": []
		},
		{
			"id": "T2",
			"title": "UI Component Integration",
			"role": "teamwork-worker",
			"files": ["web/src/User.tsx"],
			"acceptance": "run npm test in web to verify ui",
			"dependencies": ["T1"]
		},
		{
			"id": "T3",
			"title": "Task with Pre-existing Criteria",
			"role": "teamwork-worker",
			"files": ["pkg/api/api.go"],
			"acceptance": "curl localhost:8080/health",
			"criteria": [
				{"id": "CUSTOM1", "text": "Must be healthy", "verify": "curl localhost:8080/health", "priority": "must"}
			],
			"dependencies": []
		},
		{
			"id": "T4",
			"title": "Task with Empty Acceptance",
			"role": "teamwork-worker",
			"files": ["pkg/empty/empty.go"],
			"acceptance": "",
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
		RunID:      "test-run-split",
		Query:      "decompose user management tasks",
		ExploreOut: "pkg/user/user.go",
		PRD:        plan.ScopePRD{Summary: "user management"},
	}
	pl := plan.Plan{Summary: "plan", Steps: []string{"Step 1", "Step 2", "Step 3", "Step 4"}}

	board, err := o.runSplitPhase(context.Background(), in, pl, `{"steps":["Step 1"]}`, nil)
	if err != nil {
		t.Fatalf("runSplitPhase failed: %v", err)
	}
	if len(board.Tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(board.Tasks))
	}

	// Task 1: Go test command
	t1 := board.Tasks[0]
	if len(t1.Criteria) == 0 {
		t.Fatalf("t1 Criteria missing")
	}
	if !t1.Criteria[0].Blocking() || t1.Criteria[0].Priority != plan.PriorityMust {
		t.Errorf("t1 expected PriorityMust, got %q", t1.Criteria[0].Priority)
	}
	if t1.Criteria[0].Verify != "go test ./pkg/user/... -run TestUserModel" {
		t.Errorf("t1 Verify command = %q, want 'go test ./pkg/user/... -run TestUserModel'", t1.Criteria[0].Verify)
	}

	// Task 2: UI test command extracted
	t2 := board.Tasks[1]
	if len(t2.Criteria) == 0 {
		t.Fatalf("t2 Criteria missing")
	}
	if t2.Criteria[0].Priority != plan.PriorityMust {
		t.Errorf("t2 expected PriorityMust, got %q", t2.Criteria[0].Priority)
	}
	if t2.Criteria[0].Verify == "" {
		t.Errorf("t2 expected non-empty Verify command, got empty")
	}

	// Task 3: Pre-existing criteria preserved
	t3 := board.Tasks[2]
	if len(t3.Criteria) != 1 || t3.Criteria[0].ID != "CUSTOM1" {
		t.Errorf("t3 pre-existing criteria altered: %+v", t3.Criteria)
	}

	// Task 4: Empty acceptance -> EnsureTaskPRDs automatically seeds verifiable scope
	t4 := board.Tasks[3]
	if len(t4.Criteria) == 0 {
		t.Errorf("t4 expected seeded criteria from PRD, got 0")
	}
	if t4.Criteria[0].Priority != plan.PriorityMust {
		t.Errorf("t4 expected PriorityMust, got %q", t4.Criteria[0].Priority)
	}
}

// TestChallenger_TesterPhaseDeterministicSupremacyOverridesPass tests that
// an LLM tester falsely claiming pass is strictly overridden when pre-test fails and recheck fails.
func TestChallenger_TesterPhaseDeterministicSupremacyOverridesPass(t *testing.T) {
	testerPassJSON := "exit code: 0\n" + `{"passed":true,"summary":"all 100 tests pass perfectly","commands":["go test ./..."],"failures":[]}`
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return testerPassJSON
		},
	}
	o := setupTestOrchestrator(t, exec)

	board := &plan.Board{
		QueryID: "pretest-supremacy-run",
		Query:   "verify failing test handling",
		Tasks: []plan.Task{
			{ID: "T1", Title: "Task failing test", Status: plan.StatusReady, Column: plan.ColReadyToDev},
		},
	}

	pre := preTest{
		Cmd:      "go test ./pkg/completely_nonexistent_dir/...",
		OK:       false,
		Ran:      true,
		FailJSON: `{"failed_commands":["go test ./pkg/completely_nonexistent_dir/..."]}`,
	}

	verdict, err := o.runTesterPhase(context.Background(), "test query", board, pre)
	if err != nil {
		t.Fatalf("runTesterPhase failed: %v", err)
	}

	tr := plan.ParseTesterJSON(verdict.Output)
	if tr.Passed {
		t.Errorf("expected deterministic pre-test failure to veto tester pass, but got passed=true")
	}
	if !strings.Contains(verdict.Output, "deterministic re-test still failing") {
		t.Errorf("expected 'deterministic re-test still failing' in output, got: %s", verdict.Output)
	}
}

// TestChallenger_TesterPhaseDiskRenameSupremacyOverridesReject tests that
// disk evidence of a satisfied file rename clears and overrides a false negative tester rejection.
func TestChallenger_TesterPhaseDiskRenameSupremacyOverridesReject(t *testing.T) {
	root := t.TempDir()
	targetFile := filepath.Join(root, "renamed_service.go")
	if err := os.WriteFile(targetFile, []byte("package service\n\ntype Service struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default(root)
	pipe := pipeline.Teamwork()
	pipe.Normalize()

	store := contextstore.New(cfg.SlmDir())
	o := &Orchestrator{
		cfg:        cfg,
		store:      store,
		pipe:       &pipe,
		factory:    agents.NewFactory(nil, nil, "", ""),
		shared:     ggagent.NewSharedState(),
		focus:      workspace.NewFocusGuard(),
		boardStore: plan.NewLiveStore(cfg.SlmDir()),
	}

	board := &plan.Board{
		QueryID: "rename-supremacy-run",
		Query:   "rename old_service.go to renamed_service.go",
		Tasks: []plan.Task{
			{
				ID:     "T1",
				Title:  "rename old_service.go to renamed_service.go",
				Files:  []string{"renamed_service.go"},
				Status: plan.StatusDone,
				Column: plan.ColInReview,
			},
		},
	}

	// 1. renameDiskOK must report true
	if !renameDiskOK(root, board.Query, board) {
		t.Fatalf("renameDiskOK should report true for verified disk rename")
	}

	// 2. speculateTester must return fromDisk=true
	testOut, fromDisk, err := o.speculateTester(context.Background(), board.Query, board, "base prompt")
	if err != nil {
		t.Fatalf("speculateTester error: %v", err)
	}
	if !fromDisk {
		t.Errorf("expected speculateTester to return fromDisk=true")
	}
	tr := plan.ParseTesterJSON(testOut)
	if !tr.Passed {
		t.Errorf("expected speculateTester output passed=true, got %v (output: %s)", tr.Passed, testOut)
	}

	// 3. runTesterPhase with fromDisk=true passes through
	pre := preTest{OK: true, Ran: false}
	verdict, err := o.runTesterPhase(context.Background(), board.Query, board, pre)
	if err != nil {
		t.Fatalf("runTesterPhase error: %v", err)
	}
	if !verdict.FromDisk {
		t.Errorf("expected verdict.FromDisk=true")
	}
	verdictTr := plan.ParseTesterJSON(verdict.Output)
	if !verdictTr.Passed {
		t.Errorf("expected verdictTr.Passed=true")
	}
}

// TestChallenger_ResumeFlowReloadsDocPRDAndDocContract tests that resuming an interrupted run
// reliably reloads DocPRD and DocContract into both contextstore and shared globals.
func TestChallenger_ResumeFlowReloadsDocPRDAndDocContract(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Normalize()
	pipe := pipeline.Teamwork()
	// Disable execute phase to avoid launching background worker loop during test
	en := false
	execPhase := pipe.Phases["execute"]
	execPhase.Enabled = &en
	pipe.Phases["execute"] = execPhase
	pipe.Normalize()

	slmDir := cfg.SlmDir()
	if err := os.MkdirAll(slmDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write distinct DocPRD and DocContract to .slmcode/
	expectedPRD := "# Locked PRD\n\nEmpirical Challenger PRD requirement.\n"
	expectedContract := "# Interface Contracts\n\ninterface IChallengerService {}\n"

	if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocPRD), []byte(expectedPRD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocContract), []byte(expectedContract), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an interrupted turn
	turn, err := session.BeginTurn(slmDir, "resume-challenger-run", "test resume reload query")
	if err != nil {
		t.Fatal(err)
	}
	resumeBoard := plan.Board{
		QueryID: turn.ID,
		Query:   turn.Query,
		Tasks: []plan.Task{
			{ID: "T1", Title: "Completed Task", Status: plan.StatusDone, Column: plan.ColDone},
		},
	}
	if err := session.MarkInterrupted(slmDir, turn, resumeBoard, session.PhaseExecute); err != nil {
		t.Fatal(err)
	}

	exec := &mockSubAgentExec{}
	store := contextstore.New(slmDir)
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

	// Resume the turn
	res, err := o.Resume(context.Background(), turn.ID)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil Result from Resume")
	}

	// Assert DocPRD was reloaded into shared global
	prdVal, ok := o.shared.GetGlobal("prd")
	if !ok || prdVal != expectedPRD {
		t.Errorf("global 'prd' mismatch: ok=%v, got=%q, want=%q", ok, prdVal, expectedPRD)
	}

	// Assert DocContract was reloaded into shared global
	contractVal, ok := o.shared.GetGlobal("contract")
	if !ok || contractVal != expectedContract {
		t.Errorf("global 'contract' mismatch: ok=%v, got=%q, want=%q", ok, contractVal, expectedContract)
	}

	// Assert store Read reflects reloaded contents
	readPRD, err := o.store.Read(contextstore.DocPRD)
	if err != nil || readPRD != expectedPRD {
		t.Errorf("store.Read(DocPRD) mismatch: err=%v, got=%q", err, readPRD)
	}
	readContract, err := o.store.Read(contextstore.DocContract)
	if err != nil || readContract != expectedContract {
		t.Errorf("store.Read(DocContract) mismatch: err=%v, got=%q", err, readContract)
	}
}
