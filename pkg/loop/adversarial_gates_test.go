package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
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
	r := &Runner{Root: root, Log: func(string, ...interface{}) {}}
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
	r := &Runner{Root: root, Log: func(string, ...interface{}) {}}
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
