package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// A Python file inside a Go project: the harness HAS a smoke command
// (python -m py_compile), so HasSmokeCommand is true, but RunPostWorkerSmoke
// refuses to run it (command language != project language) and therefore
// attaches no "## Deterministic smoke" section. smokeMissing must not fire on
// a smoke the harness itself declined to run.
func TestSmokeMissingNotSetWhenSmokeCannotRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tool.py"), []byte("def main():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Root: root, RequireSmoke: true, PostWorkerSmoke: true, Log: func(string, ...interface{}) {}}
	task := &plan.Task{
		ID: "T1", Role: plan.RoleWorker, Title: "Add main to tool.py",
		Files:  []string{"tool.py"},
		Output: `{"status":"done","files_changed":["tool.py"]}`,
	}
	base := map[string]string{"tool.py": "1:deadbeef"}
	g := r.gatherGateSignals(context.Background(), task, base)
	if g.smokeMissing {
		t.Fatalf("smokeMissing fired for a task whose smoke command the harness refuses to run — task can never be approved")
	}
}

// A role that ShouldSmokeTask deliberately excludes (docs) but whose focus
// files are Go: HasSmokeCommand is true, the smoke never runs for that role,
// and the task is blocked forever.
func TestSmokeMissingNotSetForNonSmokeRole(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package x\n\n// A does a.\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Root: root, RequireSmoke: true, PostWorkerSmoke: true, Log: func(string, ...interface{}) {}}
	task := &plan.Task{
		ID: "T2", Role: "docs", Title: "Document A",
		Files:  []string{"a.go"},
		Output: `{"status":"done","files_changed":["a.go"]}`,
	}
	g := r.gatherGateSignals(context.Background(), task, map[string]string{"a.go": "1:old"})
	if g.smokeMissing {
		t.Fatalf("smokeMissing fired for role=docs, which quality.ShouldSmokeTask excludes from smoking")
	}
}

// A task whose whole job is to DELETE its focus file. After the worker
// succeeds, every declared target is missing on disk — which the evidence gate
// reads as "hallucinated paths" and rejects, forever.
func TestEvidenceGateAcceptsCompletedDeletion(t *testing.T) {
	root := t.TempDir()
	// legacy.go existed at wave start (baseline holds its fingerprint) and the
	// worker deleted it.
	baseline := map[string]string{"pkg/old/legacy.go": "42:abc123"}
	r := &Runner{Root: root, ClaimsGate: true, Log: func(string, ...interface{}) {}}
	task := plan.Task{
		ID: "T3", Role: plan.RoleWorker,
		Title:       "Delete pkg/old/legacy.go",
		Description: "Remove the dead legacy helper.",
		Acceptance:  "pkg/old/legacy.go no longer exists",
		Files:       []string{"pkg/old/legacy.go"},
		Output:      "Removed the file.\n" + `{"status":"done","files_changed":["pkg/old/legacy.go"]}`,
	}
	ok, why := r.evidenceOK(task, baseline)
	if !ok {
		t.Fatalf("evidence gate rejected a completed deletion: %s", why)
	}
}

// An interrupted ReAct checkpoint is saved with tool calls still pending. The
// finalize-steer nudge must not be appended as a user message after the
// unanswered assistant tool_calls message — the executor is about to append the
// tool results, and a user message between them is an HTTP 400.
func TestResumeDoesNotOrphanPendingToolCallsWithSteer(t *testing.T) {
	root := t.TempDir()
	r := &Runner{
		Root: root, TurnID: "turn1", FinalizeWarn: true,
		Log: func(string, ...interface{}) {},
	}
	msgs := []session.ReactMessage{
		{Role: "system", Content: "you are a worker"},
		{Role: "user", Content: "do the thing"},
		{Role: "assistant", ToolCalls: []session.ReactToolCall{{ID: "c1", Name: "ws_edit"}}},
	}
	cp := session.ReactCheckpoint{
		SchemaVersion: session.ReactSchemaVersion,
		TurnID:        "turn1", TaskID: "T9", AgentID: plan.RoleWorker,
		Iteration: 14, MaxIterations: 16, Messages: msgs,
		PendingToolCalls: []session.ReactToolCall{{ID: "c1", Name: "ws_edit"}},
	}
	if err := session.SaveReactCheckpoint(filepath.Join(root, ".slmcode"), cp); err != nil {
		t.Fatal(err)
	}
	req := ggagent.SubAgentRequest{AgentID: plan.RoleWorker}
	if !r.applyResumeRequest(&req, "T9") {
		t.Fatal("expected resume to apply")
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "assistant" {
		t.Fatalf("resume appended a %q message after an unanswered assistant tool_calls message: %q",
			last.Role, last.Content)
	}
	if !strings.Contains(req.Input, "finaliz") && !strings.Contains(strings.ToLower(req.Input), "turn") {
		t.Fatalf("finalize steer must still reach the model through req.Input, got %q", req.Input)
	}
}

// A worker that claims done but changed nothing on disk must be flagged weak so
// the self-critique pass runs. With a nil baseline every write detector reads a
// pre-existing file as "created by this wave", so the clause never fired.
func TestOutputWeakUsesWaveBaseline(t *testing.T) {
	root := t.TempDir()
	src := "package p\n\nfunc F() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Root: root, ClaimsGate: true, Log: func(string, ...interface{}) {}}
	task := plan.Task{
		ID: "T1", Role: plan.RoleWorker,
		Title: "Update F in a.go", Description: "change F to return 2",
		Acceptance: "F returns 2", Files: []string{"a.go"},
		Output: `{"status":"done","summary":"updated F"}`,
	}
	baseline := r.snapshotTargets(task) // file untouched since the snapshot

	if !r.outputWeak(task, baseline, false) {
		t.Fatal("a done-claim with no disk change was not flagged weak — self-critique never runs")
	}
	// Once the file really changes, the same task is no longer weak.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n\nfunc F() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r.outputWeak(task, baseline, false) {
		t.Fatal("a real disk change was still flagged weak")
	}
}

func TestDeterministicGatePrecedence_MissingEvidenceFastRejects(t *testing.T) {
	root := t.TempDir()
	src := "package p\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Root: root, ClaimsGate: true, Log: func(string, ...interface{}) {}}
	task := plan.Task{
		ID:          "T1",
		Role:        plan.RoleWorker,
		Title:       "Edit a.go",
		Description: "implement feature",
		Files:       []string{"a.go"},
		Output:      `{"status":"done","summary":"edited a.go"}`,
	}
	baseline := r.snapshotTargets(task) // file untouched since snapshot

	g := r.gatherGateSignals(context.Background(), &task, baseline)
	if !g.evidenceMissing {
		t.Fatalf("expected evidenceMissing to be true when no write occurred")
	}
	if !g.blocking() {
		t.Fatalf("expected blocking() to be true when evidence is missing")
	}

	review, _, err := r.decideReview(context.Background(), task, g)
	if err != nil {
		t.Fatalf("decideReview failed: %v", err)
	}
	if review.Approved {
		t.Errorf("expected review.Approved=false on missing evidence fast reject, got true")
	}
	if review.Score != 0 {
		t.Errorf("expected review.Score=0, got %d", review.Score)
	}
	if !strings.Contains(review.Summary, "evidence gate") {
		t.Errorf("expected summary to mention evidence gate, got: %s", review.Summary)
	}
}

func TestDeterministicGatePrecedence_ScopeViolationFastRejects(t *testing.T) {
	root := t.TempDir()
	r := &Runner{
		Root:  root,
		Focus: workspace.NewFocusGuard(),
		Log:   func(string, ...interface{}) {},
	}
	task := plan.Task{
		ID:          "T2",
		Role:        plan.RoleWorker,
		Title:       "Edit a.go only",
		Description: "stay in scope",
		Files:       []string{"pkg/a/a.go"},
		Output:      `{"status":"done","files_changed":["cmd/main.go"]}`,
	}
	baseline := map[string]string{"pkg/a/a.go": "1:abc"}

	g := r.gatherGateSignals(context.Background(), &task, baseline)
	if g.scopeWhy == "" {
		t.Fatalf("expected scopeWhy to be non-empty for scope violation")
	}
	if !g.blocking() {
		t.Fatalf("expected blocking() to be true when scope is violated")
	}

	review, _, err := r.decideReview(context.Background(), task, g)
	if err != nil {
		t.Fatalf("decideReview failed: %v", err)
	}
	if review.Approved {
		t.Errorf("expected review.Approved=false on scope violation fast reject, got true")
	}
	if !strings.Contains(review.Summary, "scope violation") {
		t.Errorf("expected summary to mention scope violation, got: %s", review.Summary)
	}
}

func TestHardGateOverride_SycophantReviewerVetoedBySmoke(t *testing.T) {
	root := t.TempDir()
	r := &Runner{Root: root, ClaimsGate: true, Log: func(string, ...interface{}) {}}
	task := plan.Task{
		ID:     "T3",
		Role:   plan.RoleWorker,
		Files:  []string{"a.go"},
		Output: "code with compiler error",
	}

	review := plan.ReviewResult{
		Approved: true,
		Score:    95,
		Verdict:  "approve",
		Summary:  "looks marvelous",
	}
	g := gateState{smokeFail: true}

	res := r.applyHardGates(&task, review, g, nil)
	if res.Approved {
		t.Errorf("expected applyHardGates to override sycophant review, got Approved=true")
	}
	if res.Score > 20 {
		t.Errorf("expected score capped at 20, got %d", res.Score)
	}
	if !strings.Contains(res.Summary, "deterministic smoke failed") {
		t.Errorf("expected summary to mention deterministic smoke failed, got: %s", res.Summary)
	}
}

func TestHardGateOverride_SycophantReviewerVetoedByCriteria(t *testing.T) {
	root := t.TempDir()
	r := &Runner{Root: root, ClaimsGate: true, Log: func(string, ...interface{}) {}}
	task := plan.Task{
		ID:     "T4",
		Role:   plan.RoleWorker,
		Files:  []string{"b.go"},
		Output: "code with failing tests",
	}

	review := plan.ReviewResult{
		Approved: true,
		Score:    90,
		Verdict:  "approve",
		Summary:  "approving despite broken tests",
	}
	g := gateState{criteriaFail: true}

	res := r.applyHardGates(&task, review, g, nil)
	if res.Approved {
		t.Errorf("expected applyHardGates to override review on criteria failure, got Approved=true")
	}
	if res.Score > 20 {
		t.Errorf("expected score capped at 20, got %d", res.Score)
	}
	if !strings.Contains(res.Summary, "acceptance criterion failed") {
		t.Errorf("expected summary to mention acceptance criterion failed, got: %s", res.Summary)
	}
}

func TestHardGateOverride_AdversarialVeto(t *testing.T) {
	root := t.TempDir()
	r := &Runner{Root: root, ClaimsGate: true, Log: func(string, ...interface{}) {}}
	task := plan.Task{
		ID:     "T5",
		Role:   plan.RoleWorker,
		Files:  []string{"c.go"},
		Output: "code with architectural flaw",
	}

	// Model erroneously set approved: true but verdict: veto
	review := plan.ReviewResult{
		Approved: true,
		Score:    85,
		Verdict:  "veto",
		Summary:  "architectural vulnerability discovered",
	}
	g := gateState{}

	res := r.applyHardGates(&task, review, g, nil)
	if res.Approved {
		t.Errorf("expected applyHardGates to override approved:true on adversarial verdict=veto, got Approved=true")
	}
	if res.Score > 20 {
		t.Errorf("expected score capped at <= 20, got %d", res.Score)
	}
}

func TestCorrectionRegressedDetection(t *testing.T) {
	if !correctionRegressed([]int{70, 40}) {
		t.Errorf("expected 70 -> 40 to trigger correctionRegressed")
	}
	if !correctionRegressed([]int{80, 70, 50}) {
		t.Errorf("expected 80 -> 70 -> 50 to trigger correctionRegressed")
	}
	if correctionRegressed([]int{40, 70}) {
		t.Errorf("expected 40 -> 70 not to trigger correctionRegressed")
	}
	if correctionRegressed([]int{70, 70}) {
		t.Errorf("expected 70 -> 70 not to trigger correctionRegressed")
	}
	if correctionRegressed([]int{50}) {
		t.Errorf("single score must not trigger correctionRegressed")
	}
}

type mockSubAgentRunner struct {
	fn func(req ggagent.SubAgentRequest) string
}

func (m *mockSubAgentRunner) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		res := `{"approved":true,"score":85}`
		if m.fn != nil {
			res = m.fn(req)
		}
		out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Output: res}
	}
	return out, nil
}

func TestAdversarialVeto_InterruptsRetryLadder(t *testing.T) {
	root := t.TempDir()
	callCount := 0
	exec := &mockSubAgentRunner{
		fn: func(req ggagent.SubAgentRequest) string {
			callCount++
			return `{"approved": false, "score": 20, "verdict": "veto", "summary": "unrecoverable architectural flaw", "issues": ["broken core design"]}`
		},
	}
	r := &Runner{
		Root:         root,
		MaxRetries:   3,
		ReviewerRole: plan.RoleReviewer,
		Executor:     exec,
		Log:          func(string, ...interface{}) {},
	}
	task := plan.Task{
		ID:     "T_VETO",
		Role:   plan.RoleWorker,
		Files:  []string{"core.go"},
		Output: `{"status":"done","files_changed":["core.go"]}`,
		Criteria: []plan.Criterion{
			{ID: "AC1", Text: "Requires adversarial review verification", Priority: plan.PriorityMust},
		},
	}
	// Write dummy core.go so write evidence is present
	if err := os.WriteFile(filepath.Join(root, "core.go"), []byte("package core\nfunc F(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline := map[string]string{"core.go": "1:old"}

	finalTask, esc, err := r.reviewAndCorrectTask(context.Background(), task, baseline)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if esc == nil {
		t.Fatalf("expected task to be escalated immediately on adversarial veto")
	}
	if finalTask.Column != plan.ColToScope {
		t.Errorf("expected task column to be %q, got %q", plan.ColToScope, finalTask.Column)
	}
	// Crucial assertion: reviewer should only be called for attempt 0, NOT 4 times
	if callCount != 1 {
		t.Errorf("expected exactly 1 review call before ladder interruption, got %d", callCount)
	}
}
