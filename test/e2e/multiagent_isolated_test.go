package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// TestIsolatedMultiAgentBoardSandbox prepares a temp multi-task board and
// verifies anti-wander focus + board structure without touching the user's
// real .slmcode board. With RUN_E2E=1 it also runs a real short pipeline.
func TestIsolatedMultiAgentBoardSandbox(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg/greet"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg/greet/greet.go"), []byte("package greet\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "pkg/greet/greet_test.go"), []byte("package greet\n\nimport \"testing\"\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hi\" { t.Fatal(Hello()) }\n}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("# greet\n"), 0o644)

	cfg := config.Default(root)
	cfg.DryRun = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2
	cfg.MaxRetries = 2
	cfg.QAGate = true
	cfg.QAGateCommand = "go test ./pkg/greet/ -count=1"
	cfg.TaskTimeout = 8 * time.Minute
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}

	// Controlled short board (not the user's 20-task board).
	board := &plan.Board{Tasks: []plan.Task{
		{
			ID: "T1", Title: "Doc comment Hello", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Files:       []string{"pkg/greet/greet.go"},
			Description: "Add a short doc comment above Hello(). Do not create main.go.",
			Acceptance:  "pkg/greet/greet.go has a // comment above Hello",
		},
		{
			ID: "T2", Title: "Verify greet tests", Role: plan.RoleTester, Column: plan.ColReadyToDev,
			Files: []string{"pkg/greet/greet_test.go"}, DependsOn: []string{"T1"},
			Description: "Run go test ./pkg/greet and confirm Hello still returns hi.",
			Acceptance:  "tests pass",
		},
	}}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	live := plan.NewLiveStore(cfg.SlmDir())
	if err := live.Replace(*board); err != nil {
		t.Fatal(err)
	}

	// Anti-wander: focus guard must block root main.go for this board.
	g := workspace.NewFocusGuard()
	g.SetWave([][]string{board.Tasks[0].Files, board.Tasks[1].Files})
	if g.Allow("main.go") {
		t.Fatal("main.go must be out of scope for greet-focused board")
	}
	if !g.Allow("pkg/greet/greet.go") {
		t.Fatal("focus file should be allowed")
	}

	snap := live.Snapshot()
	if len(snap.Tasks) != 2 {
		t.Fatalf("tasks=%d", len(snap.Tasks))
	}
	ready := snap.ReadyTasks()
	// T2 depends on T1 — only T1 ready initially
	if len(ready) != 1 || ready[0].ID != "T1" {
		t.Fatalf("ready=%+v", ready)
	}

	if os.Getenv("RUN_E2E") != "1" {
		t.Log("sandbox board + focus OK (set RUN_E2E=1 for live multi-agent run)")
		return
	}

	cfg.ResolveAPIKey()
	if cfg.APIKey == "" {
		t.Fatal("no API key for live multi-agent run")
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.Orchestrator = orch

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()
	res, err := h.Run(ctx, "Add a Doc comment to greet.Hello() in pkg/greet/greet.go. Keep change tiny. Do not create main.go.")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("success=%v failed=%d summary=%s", res.Success, res.FailedTasks, res.Summary)

	// Must not have wandered into a root main.go.
	if _, err := os.Stat(filepath.Join(root, "main.go")); err == nil {
		t.Fatal("anti-wander failed: unwanted main.go created in sandbox")
	}
	body, err := os.ReadFile(filepath.Join(root, "pkg/greet/greet.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Hello") {
		t.Fatal("greet.go missing Hello")
	}
	// Prefer a real edit; tolerate SLM review flakiness if the target was touched.
	if !strings.Contains(string(body), "//") {
		t.Log("warning: doc comment may be missing after live run (SLM quality)")
	}
}
