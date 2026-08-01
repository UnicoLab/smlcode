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
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// TestEvalHarnessOffline covers deterministic eval helpers + gates (no LLM).
func TestEvalHarnessOffline(t *testing.T) {
	cases := eval.DefaultCases()
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	if quality.ClassifyIntervention("repeated_tool_call") != quality.InterventionLoop {
		t.Fatal("intervention classify")
	}
	if stream.KindIntervention == "" || stream.KindTurn == "" {
		t.Fatal("stream kinds")
	}
	// Shell whitelist blocks rm — quality/UX gate used by eval-sensitive runs.
	if workspace.IsSafeBash("rm -rf /tmp/x", workspace.BuiltinSafePrefixes) {
		t.Fatal("rm must not be safe")
	}
	if !workspace.IsSafeBash("go test ./pkg -short", workspace.BuiltinSafePrefixes) {
		t.Fatal("go test should be safe")
	}
}

// TestLiveEvalHarness runs DefaultCases against local oMLX when RUN_E2E=1.
func TestLiveEvalHarness(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to run live eval harness")
	}
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Endpoint = "http://127.0.0.1:8000/v1"
	cfg.Model = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"
	cfg.ThinkPasses = 1
	cfg.MaxRetries = 1
	cfg.TaskTimeout = 10 * time.Minute
	cfg.ResolveAPIKey()
	if cfg.APIKey == "" {
		t.Fatal("oMLX api key not resolved")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	// Run a single lean case for CI-friendly live time.
	cases := []eval.Case{eval.DefaultCases()[0]}
	cases[0].Timeout = 12 * time.Minute
	rep := eval.RunAll(ctx, cases, cfg)
	out := filepath.Join(t.TempDir(), "eval-report.json")
	if err := eval.WriteReport(out, rep); err != nil {
		t.Fatal(err)
	}
	if rep.Passed+rep.Failed != len(cases) {
		t.Fatalf("report counts: %+v", rep)
	}
	if rep.Failed > 0 {
		var msgs []string
		for _, r := range rep.Results {
			if !r.OK {
				msgs = append(msgs, r.ID+": "+r.Error)
			}
		}
		t.Fatalf("eval failed: %s", strings.Join(msgs, "; "))
	}
}
