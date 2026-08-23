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

// TestEvalOfflineThroughTheRealBinary drives `slmcode eval --offline` — the
// mode that is supposed to prove a harness change helped without a model.
//
// pkg/eval's own tests assert the replay maths. This one asserts that the
// COMMAND a person actually types reaches them: that the fixtures are embedded
// in the shipped binary (a `go:embed` that silently loses a fixture is exactly
// the kind of break a library test cannot see), that the report names every
// fixture including the control arm, that the A/B moves the metrics the repair
// ladder is meant to move, and that the verdict comes out as a documented exit
// code rather than as prose.
func TestEvalOfflineThroughTheRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary under test")
	}
	dir := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")

	out, code := slm(t, dir, home, "eval", "--offline")
	if code != 0 {
		t.Fatalf("eval --offline exited %d (0 = the current arm beat the baseline):\n%s", code, out)
	}

	// Every embedded fixture must be replayed and named, the control arm
	// included — an A/B with no control cannot support a claim.
	for _, want := range []string{
		"no model called",
		"repair-ladder-go",
		"edit-format-fallback-py",
		"unrepairable-js",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("eval --offline output does not mention %q:\n%s", want, out)
		}
	}
	// The metrics the repair ladder exists to move, and the verdict.
	for _, want := range []string{
		"repair-rule hit rate",
		"failures fixed from memory",
		"LLM calls per task",
		"Verdict: improved",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("eval --offline did not report %q — the A/B proves nothing:\n%s", want, out)
		}
	}
	// Deterministic: a replay that moves between runs cannot support a claim
	// about a change, and the CLI is where anyone would notice.
	second, code2 := slm(t, dir, home, "eval", "--offline")
	if code2 != 0 {
		t.Fatalf("second eval --offline exited %d", code2)
	}
	if second != out {
		t.Errorf("eval --offline is not byte-identical between runs:\n--- first ---\n%s\n--- second ---\n%s", out, second)
	}
	// It must not have needed (or invented) a workspace: --offline is the mode
	// you can run in a bare checkout.
	if _, err := os.Stat(filepath.Join(dir, ".slmcode", "config.yaml")); err == nil {
		t.Error("eval --offline scaffolded a workspace — it is supposed to touch nothing")
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
