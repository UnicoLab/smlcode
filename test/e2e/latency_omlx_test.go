package e2e_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// TestLiveOMLXLatencyBenchmark runs a tiny multi-turn pipeline against local oMLX
// and reports per-role latency. Requires a reachable oMLX with API key.
//
//	RUN_E2E=1 go test ./test/e2e/ -run TestLiveOMLXLatencyBenchmark -timeout 60m -v
func TestLiveOMLXLatencyBenchmark(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to hit local oMLX")
	}

	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg/greet"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg/greet/greet.go"),
		[]byte("package greet\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module toy\n\ngo 1.22\n"), 0o644)

	cfg := config.Default(root)
	cfg.Verbose = true
	cfg.DryRun = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2
	cfg.MaxRetries = 1
	cfg.QAGate = false
	cfg.TaskTimeout = 6 * time.Minute
	if m := os.Getenv("SLMCODE_MODEL"); m != "" {
		cfg.Model = m
	}
	cfg.ResolveAPIKey()
	if cfg.APIKey == "" {
		t.Fatal("no oMLX API key — set OMLX_API_KEY or ~/.omlx/settings.json auth.api_key")
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

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	type turn struct {
		name  string
		query string
	}
	turns := []turn{
		{"t1-doc", "Add a one-line Go doc comment above Hello() in pkg/greet/greet.go only. Tiny change."},
		{"t2-rename", "In pkg/greet/greet.go only, rename Hello to Greet and update the return to \"hello\". Do not create files."},
	}

	var report strings.Builder
	report.WriteString("\n=== oMLX multi-turn latency benchmark ===\n")
	fmt.Fprintf(&report, "provider=%s model=%s endpoint=%s\n", cfg.Provider, cfg.Model, cfg.Endpoint)

	for _, tr := range turns {
		start := time.Now()
		res, err := h.Run(ctx, tr.query)
		wall := time.Since(start)
		if err != nil {
			t.Fatalf("%s: %v", tr.name, err)
		}
		fmt.Fprintf(&report, "\n--- %s success=%v wall=%s ---\n", tr.name, res.Success, wall.Round(time.Millisecond))
		if len(res.LatencyMs) == 0 {
			report.WriteString("(no per-role latency recorded)\n")
			continue
		}
		keys := make([]string, 0, len(res.LatencyMs))
		for k := range res.LatencyMs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&report, "  %-14s %6dms\n", k, res.LatencyMs[k])
		}
		fmt.Fprintf(&report, "  %-14s %6dms\n", "TOTAL", wall.Milliseconds())
		t.Logf("%s wall=%s latency=%v success=%v", tr.name, wall, res.LatencyMs, res.Success)
	}

	body := filepath.Join(root, "pkg/greet/greet.go")
	src, _ := os.ReadFile(body)
	report.WriteString("\nfinal greet.go:\n" + string(src) + "\n")
	t.Log(report.String())

	// Persist a copy under the repo for docs when BENCH_OUT is set.
	if out := os.Getenv("BENCH_OUT"); out != "" {
		_ = os.WriteFile(out, []byte(report.String()), 0o644)
	}
}
