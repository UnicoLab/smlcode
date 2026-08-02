package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/eval"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

// TestRealQueriesOfflineReferenceBar compares engine planning + quality gates
// to what a senior engineer would ship for each real query (no LLM).
func TestRealQueriesOfflineReferenceBar(t *testing.T) {
	for _, rq := range eval.RealQueries() {
		t.Run(rq.ID+"/harness", func(t *testing.T) {
			res := eval.EvaluateHarnessPlan(rq)
			if !res.OK {
				t.Fatalf("planning below expert bar: %v", res.Gaps)
			}
		})
	}

	t.Run("langgraph/garbage_rejected", func(t *testing.T) {
		root := t.TempDir()
		if err := eval.WriteLangGraphGarbageFixture(root); err != nil {
			t.Fatal(err)
		}
		rq := eval.RealQueries()[0]
		issues := eval.EvaluateWorkspaceAgainstReference(root, rq)
		if len(issues) < 3 {
			t.Fatalf("garbage accepted: %#v", issues)
		}
		// Weak QA must not be enough to promote this board.
		if !quality.IsWeakQACommand("python -m compileall -q .") {
			t.Fatal("compileall should be weak")
		}
		board := &plan.Board{Query: rq.Query, Tasks: []plan.Task{{
			ID: "T2", Role: plan.RoleWorker, Column: plan.ColInReview,
			Files:  []string{"src/lg_agent/agents/agent.py"},
			Output: `{"status":"done","summary":"created agent"}`,
		}}}
		// Completeness report must be non-empty for Studio/HITL.
		if rep := quality.FormatCompletenessReport(issues); !strings.Contains(rep, "gap") {
			t.Fatalf("bad report: %s", rep)
		}
		_ = board
	})

	t.Run("langgraph/reference_accepted", func(t *testing.T) {
		root := t.TempDir()
		if err := eval.WriteLangGraphReferenceScaffold(root); err != nil {
			t.Fatal(err)
		}
		rq := eval.RealQueries()[0]
		issues := eval.EvaluateWorkspaceAgainstReference(root, rq)
		if len(issues) != 0 {
			t.Fatalf("expert scaffold rejected: %#v", issues)
		}
		cmd := quality.DetectProjectCommand(root)
		if !strings.Contains(cmd, "pytest") {
			t.Fatalf("reference with tests/ should pick pytest, got %q", cmd)
		}
	})
}

// TestLiveRealQueryLangGraph runs the original failing user query against oMLX
// and scores the workspace against the expert reference bar.
func TestLiveRealQueryLangGraph(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to run live real-query eval")
	}
	rq := eval.RealQueries()[0]
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Provider = "omlx"
	cfg.Endpoint = "http://127.0.0.1:8000/v1"
	cfg.Model = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"
	cfg.QAGate = true
	cfg.ThinkPasses = 1
	cfg.MaxRetries = 2
	cfg.PlaceholderPass = true
	cfg.ContinueAsk = "auto"
	cfg.EscalateAsk = "auto"
	cfg.TaskTimeout = 18 * time.Minute
	cfg.CompactMode = true
	cfg.ResolveAPIKey()
	if cfg.APIKey == "" {
		t.Fatal("oMLX api key not resolved")
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
	res, err := h.Run(ctx, rq.Query)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}

	issues := quality.CheckProjectCompleteness(root, rq.Query)
	if len(issues) > 0 {
		t.Fatalf("live output below expert reference bar (%d gaps):\n%s\nsummary=%s",
			len(issues), quality.FormatCompletenessReport(issues), res.Summary)
	}
	for _, f := range []string{"requirements.txt", "main.py"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Fatalf("missing %s after live run", f)
		}
	}
}
