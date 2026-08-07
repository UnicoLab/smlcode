package e2e_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/models"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/stacks"
)

// TestLiveStacksOMLXAndDeepSeek runs short live pipelines against omlx-local
// and deepseek stacks (temp workspace — does not mutate the user's project).
//
//	RUN_E2E=1 go test ./test/e2e/ -run TestLiveStacks -timeout 60m -v
func TestLiveStacksOMLXAndDeepSeek(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 for live omlx/deepseek stack runs")
	}
	repo := findRepoRoot(t)
	t.Setenv("SLMCODE_STACKS", filepath.Join(repo, "stacks"))

	t.Run("omlx-local", func(t *testing.T) {
		runLiveStackTiny(t, "omlx-local", true)
	})
	t.Run("deepseek", func(t *testing.T) {
		runLiveStackTiny(t, "deepseek", false)
	})
}

func runLiveStackTiny(t *testing.T, stackID string, requireLocalKey bool) {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg", "greet"), 0o755)
	// Minimal module so worker smoke (`go test ./pkg/greet`) works without wander.
	_ = os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module livefixture\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "pkg/greet/greet.go"),
		[]byte("package greet\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "pkg/greet/greet_test.go"),
		[]byte("package greet\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hi\" { t.Fatal(Hello()) }\n}\n"), 0o644)

	cfg := config.Default(root)
	st, err := stacks.Load(stackID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stacks.Apply(cfg, st, cfg.AgentsDir(), stacks.ApplyOptions{ClearAgentLLM: true}); err != nil {
		t.Fatal(err)
	}
	// Re-assert live-smoke knobs — stacks.Apply overwrites retries/think/HITL from YAML.
	cfg.DryRun = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 1
	cfg.MaxRetries = 1
	cfg.QAGate = true
	cfg.QAGateCommand = "go test ./pkg/greet/ -count=1"
	cfg.QAGateMaxRounds = 1
	cfg.TaskTimeout = 8 * time.Minute
	cfg.PlanApprove = "auto"
	cfg.ClarifyMode = "auto"
	// off = no HITL rewrite loops (auto can thrash local SLMs for minutes).
	cfg.EscalateAsk = "off"
	cfg.ContinueAsk = "off"
	cfg.AutoApprove = true
	cfg.SessionEventLog = true
	cfg.ContextCompact = true
	cfg.PostWorkerSmoke = true
	cfg.WorkerCritique = false
	cfg.QualityMonitor = false
	cfg.StaticQuality = false
	cfg.ResolveAPIKey()
	auth := models.ResolveAuth(cfg)
	if !auth.Configured {
		if requireLocalKey {
			t.Fatalf("%s auth missing: %+v", stackID, auth)
		}
		t.Skipf("%s auth not configured (%s) — set DEEPSEEK_API_KEY or auth.json", stackID, auth.Message)
	}

	// Catalog / find_models path (auth-aware).
	cat := models.Find(context.Background(), cfg, "", 8)
	t.Logf("%s models n=%d err=%q auth=%s", stackID, len(cat.Models), cat.Error, cat.Auth.Source)
	if auth.Required && !auth.Configured && len(cat.Matches) == 0 {
		t.Fatalf("fail-closed catalog empty: %+v", cat)
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

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	query := "Add a one-line doc comment above Hello() in pkg/greet/greet.go. Tiny change only. Do not create main.go."
	res, err := h.Run(ctx, query)
	timedOut := err != nil && (errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") ||
		strings.Contains(strings.ToLower(err.Error()), "context canceled"))
	if err != nil && !timedOut {
		// Transport / protocol failures (e.g. DeepSeek unpaired tool_calls) must fail hard.
		t.Fatalf("%s run: %v", stackID, err)
	}
	if res != nil {
		t.Logf("%s success=%v failed=%d summary=%s usage=%v",
			stackID, res.Success, res.FailedTasks, res.Summary, res.Usage)
	} else if timedOut {
		t.Logf("%s run timed out — checking disk evidence", stackID)
	}

	if _, err := os.Stat(filepath.Join(root, "main.go")); err == nil {
		t.Fatalf("%s anti-wander: unwanted main.go", stackID)
	}
	body, err := os.ReadFile(filepath.Join(root, "pkg/greet/greet.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "Hello") {
		t.Fatal("greet.go missing Hello")
	}
	if !strings.Contains(text, "//") {
		if timedOut {
			t.Fatalf("%s timed out without writing a doc comment:\n%s", stackID, text)
		}
		t.Fatalf("%s expected a doc comment in greet.go, got:\n%s", stackID, text)
	}
	cmd := exec.Command("go", "test", "./pkg/greet/", "-count=1")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s go test after run failed: %v\n%s", stackID, err, out)
	}
	// Events log should exist when session_event_log is on.
	if cfg.SessionEventLog && res != nil && res.ID != "" {
		evPath := filepath.Join(cfg.SlmDir(), "queries", res.ID, "events.jsonl")
		if _, err := os.Stat(evPath); err != nil {
			t.Logf("warning: events.jsonl missing at %s", evPath)
		}
	}
	// Pipeline Success / wall-clock can flap on local SLM tester loops; disk+tests are the smoke gate.
	if timedOut {
		t.Logf("%s note: timed out with disk+tests OK", stackID)
	} else if res != nil && !res.Success {
		t.Logf("%s note: Success=false with disk+tests OK — summary=%s", stackID, res.Summary)
	}
}
