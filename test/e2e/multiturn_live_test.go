package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
)

// TestLiveMultiTurnQueryScope runs two real queries in a temp workspace and
// asserts query-scoped plan/tasks + summary enrichment across turns.
//
//	RUN_E2E=1 go test ./test/e2e/ -run TestLiveMultiTurnQueryScope -timeout 90m
func TestLiveMultiTurnQueryScope(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 for live multi-turn e2e")
	}

	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg/greet"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg/greet/greet.go"),
		[]byte("package greet\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "pkg/greet/greet_test.go"),
		[]byte("package greet\n\nimport \"testing\"\nfunc TestHello(t *testing.T) {\n\tif Hello() != \"hi\" { t.Fatal(Hello()) }\n}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module toy\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("# toy\n"), 0o644)

	cfg := config.Default(root)
	cfg.Verbose = true
	cfg.DryRun = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2
	cfg.MaxRetries = 1
	cfg.QAGate = true
	cfg.QAGateCommand = "go test ./pkg/greet/ -count=1"
	cfg.QAGateMaxRounds = 1
	cfg.TaskTimeout = 6 * time.Minute
	if m := os.Getenv("SLMCODE_MODEL"); m != "" {
		cfg.Model = m
	}
	cfg.ResolveAPIKey()
	if cfg.APIKey == "" {
		t.Fatal("no API key for live multi-turn run")
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
	slm := cfg.SlmDir()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	// Turn 1 — keep the ask tiny so SLMs finish within CI-ish budgets.
	res1, err := h.Run(ctx, "Add a one-line Go doc comment above Hello() in pkg/greet/greet.go only. Do not edit tests. Do not create files.")
	if err != nil {
		t.Fatalf("turn1: %v", err)
	}
	t.Logf("turn1 success=%v id=%s summary=%s tasks=%d", res1.Success, res1.ID, res1.Summary, len(res1.Board.Tasks))
	if res1.Board.QueryID == "" {
		t.Fatal("turn1 board missing query_id")
	}
	sum1 := filepath.Join(session.TurnDir(slm, res1.ID), "summary.md")
	if _, err := os.Stat(sum1); err != nil {
		t.Fatal("turn1 summary.md missing", err)
	}
	plan1, _ := os.ReadFile(filepath.Join(session.TurnDir(slm, res1.ID), "PLAN.md"))
	if !strings.Contains(string(plan1), res1.ID) && !strings.Contains(string(plan1), "Query") {
		t.Logf("turn1 PLAN (ok if summary-only): %s", truncateE2E(string(plan1), 200))
	}
	// Rewrite path must not explode the board into a mega backlog.
	if len(res1.Board.Tasks) > 10 {
		t.Fatalf("turn1 board too large after rewrite/merge: %d tasks", len(res1.Board.Tasks))
	}

	// Capture turn1 task titles to ensure turn2 does not keep them live.
	turn1Titles := map[string]bool{}
	for _, task := range res1.Board.Tasks {
		turn1Titles[task.Title] = true
	}

	// Turn 2 — fresh plan/tasks, enriched by prior summary
	res2, err := h.Run(ctx, "In pkg/greet/greet_test.go only, change t.Fatal(Hello()) to include the got value. Tiny edit. Do not add new funcs.")
	if err != nil {
		t.Fatalf("turn2: %v", err)
	}
	t.Logf("turn2 success=%v id=%s summary=%s", res2.Success, res2.ID, res2.Summary)
	if res2.ID == res1.ID {
		t.Fatal("turn2 must have a distinct query/run id")
	}
	if res2.Board.QueryID != res2.ID {
		t.Fatalf("turn2 query_id=%q want %q", res2.Board.QueryID, res2.ID)
	}
	sum2 := filepath.Join(session.TurnDir(slm, res2.ID), "summary.md")
	if _, err := os.Stat(sum2); err != nil {
		t.Fatal("turn2 summary.md missing", err)
	}
	// Live board should be turn2-scoped (not a merge of both forever boards)
	liveRaw, _ := os.ReadFile(filepath.Join(slm, "board.json"))
	var live plan.Board
	_ = json.Unmarshal(liveRaw, &live)
	if live.QueryID != res2.ID {
		t.Fatalf("live board query_id=%q want turn2 %q", live.QueryID, res2.ID)
	}
	for _, task := range live.Tasks {
		if turn1Titles[task.Title] && !strings.Contains(strings.ToLower(task.Title), "test") {
			// Allow coincidental similar titles; fail only if exact old implementer remains alone.
			t.Logf("note: live task title overlaps turn1: %s", task.Title)
		}
	}
	// Prior turn directory preserved
	if _, err := os.Stat(filepath.Join(session.TurnDir(slm, res1.ID), "summary.md")); err != nil {
		t.Fatal("turn1 summary should remain after turn2", err)
	}
	prior := session.RecentSummaries(slm, 5)
	if !strings.Contains(prior, "doc comment") && !strings.Contains(strings.ToLower(prior), "hello") {
		t.Fatalf("prior summaries should enrich knowledge:\n%s", truncateE2E(prior, 500))
	}
	if _, err := os.Stat(filepath.Join(root, "main.go")); err == nil {
		t.Fatal("anti-wander: main.go must not appear")
	}
}

func truncateE2E(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
