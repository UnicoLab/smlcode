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
)

// TestLiveOMLXPipeline runs a tiny real pipeline against local oMLX.
//
//	RUN_E2E=1 go test ./test/e2e/ -run TestLiveOMLX -timeout 45m
func TestLiveOMLXPipeline(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to hit local oMLX")
	}

	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)

	cfg := config.Default(root)
	cfg.Verbose = true
	cfg.DryRun = false // real edits — proves SLM quality end-to-end
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2
	cfg.MaxRetries = 2
	cfg.TaskTimeout = 10 * time.Minute
	if m := os.Getenv("SLMCODE_MODEL"); m != "" {
		cfg.Model = m
	}
	cfg.ResolveAPIKey()
	if cfg.APIKey == "" {
		t.Fatal("no oMLX API key — set OMLX_API_KEY or configure ~/.omlx/settings.json")
	}
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
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

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	res, err := h.Run(ctx, "Add a Doc comment to Hello() explaining it returns a greeting. Keep the change tiny.")
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	t.Logf("success=%v summary=%s failed=%d duration=%s", res.Success, res.Summary, res.FailedTasks, res.Duration)
	if len(res.Board.Tasks) == 0 {
		t.Fatal("expected tasks on board")
	}
	done := 0
	for _, task := range res.Board.Tasks {
		if task.Column == "done" || task.Status == "done" {
			done++
		}
	}
	if done == 0 {
		t.Fatalf("expected at least one done task; board=%+v", res.Board.Tasks)
	}
	body, err := os.ReadFile(filepath.Join(root, "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Hello") {
		t.Fatalf("hello.go unexpectedly empty of Hello: %s", body)
	}
	// Require a real doc comment — success alone is not enough (SLMs can approve without edits)
	if !strings.Contains(string(body), "//") {
		t.Fatalf("expected doc comment in hello.go; body=\n%s", body)
	}
	if !res.Success {
		t.Fatalf("expected success with doc comment written; failed=%d summary=%s", res.FailedTasks, res.Summary)
	}
}
