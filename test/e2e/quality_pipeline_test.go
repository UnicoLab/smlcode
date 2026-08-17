package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// TestQualityPipelineOffline covers the deterministic pieces that make
// greenfield Python production reliable (no live LLM required).
func TestQualityPipelineOffline(t *testing.T) {
	if !plan.NeedsClarification("build an agent") {
		t.Fatal("vague query should need clarification")
	}
	if plan.NeedsClarification("Add JWT checks in pkg/auth/jwt.go with go test") {
		t.Fatal("concrete query should skip clarification")
	}

	detail := agents.AgentDetail("tester", nil)
	if detail == nil {
		t.Fatal("missing tester detail")
	}
	sp, _ := detail["system_prompt"].(string)
	if !strings.Contains(sp, "ws_shell") {
		t.Fatalf("tester prompt must require ws_shell: %q", sp)
	}

	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.py"), []byte("def main():\n    print('hi')\n\nif __name__ == '__main__':\n    main()\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "requirements.txt"), []byte("# none\n"), 0o644)

	tasks := []plan.Task{
		{ID: "T1", Title: "Create main.py", Role: plan.RoleWorker, Files: []string{"main.py"}},
		{ID: "T2", Title: "Create lib.py", Role: plan.RoleWorker, Files: []string{"lib.py"}},
		{ID: "T3", Title: "Create tests", Role: plan.RoleWorker, Files: []string{"tests/test_main.py"}},
	}
	out := plan.SanitizeTasks(tasks, "", "Create a minimal Python project with main.py")
	hasTester := false
	for _, tk := range out {
		if tk.Role == plan.RoleTester {
			hasTester = true
		}
	}
	if !hasTester {
		t.Fatalf("expected auto tester task, got %+v", out)
	}

	r := plan.ParseTesterJSON(`{"passed":true,"summary":"files look ok","failures":[]}`)
	if r.Passed {
		t.Fatal("soft-pass without commands must fail")
	}

	got := agents.AgentDetail("worker", nil)
	if got == nil || !strings.Contains(got["system_prompt"].(string), "HARD SCOPE") {
		t.Fatal("worker detail incomplete")
	}
}

// TestLiveQualityGreenfieldPython runs a short live pipeline against local oMLX
// when RUN_E2E=1. Verifies clarifier/plan structure + QA gate smoke path.
func TestLiveQualityGreenfieldPython(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to hit local oMLX")
	}
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Provider = "omlx"
	cfg.Endpoint = "http://127.0.0.1:8000/v1"
	cfg.Model = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"
	cfg.QAGate = true
	cfg.ThinkPasses = 1
	cfg.MaxRetries = 2
	cfg.TaskTimeout = 18 * time.Minute
	cfg.CompactMode = true
	cfg.EscalateAsk = "auto" // don't block on HITL in CI
	cfg.ContinueAsk = "auto" // don't block on HITL in CI
	cfg.AutoApprove = true   // skip plan/shell/clarify waits
	cfg.ResolveAPIKey()
	if cfg.APIKey == "" {
		t.Fatal("oMLX api key not resolved from ~/.omlx/settings.json")
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

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	res, err := h.Run(ctx, "Create a tiny Python CLI in main.py that prints hello and supports --help. Keep it minimal.")
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	mainPath := filepath.Join(root, "main.py")
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("expected main.py to exist: %v (summary=%s)", err, res.Summary)
	}
	planMD, err := os.ReadFile(filepath.Join(root, ".slmcode", "PLAN.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(planMD)), "{") {
		t.Fatalf("PLAN.md still starts with raw JSON:\n%s", planMD)
	}
	scratch, _ := os.ReadFile(filepath.Join(root, ".slmcode", "SCRATCH.md"))
	if !strings.Contains(string(scratch), "QA") && !strings.Contains(string(scratch), "Verification") {
		t.Logf("warning: SCRATCH.md missing QA/Verification section:\n%s", scratch)
	}
}
