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

// TestLiveStaticWebBattleship runs the exact real-world request that previously
// failed (a pile of .js files, no usable HTML) against local oMLX and asserts a
// usable HTML entrypoint is produced. It also exercises the dynamic pipeline
// (default-on) and the static-web quality gate.
//
//	RUN_E2E=1 go test ./test/e2e/ -run TestLiveStaticWebBattleship -timeout 45m -v
func TestLiveStaticWebBattleship(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to hit local oMLX")
	}

	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Provider = "omlx"
	cfg.Endpoint = "http://127.0.0.1:8000/v1"
	cfg.Model = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"
	cfg.DynamicPipeline = true
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2
	cfg.MaxRetries = 2
	cfg.TaskTimeout = 12 * time.Minute
	cfg.EscalateAsk = "auto"
	cfg.ContinueAsk = "auto"
	cfg.AutoApprove = true
	cfg.ClarifyMode = "auto"
	cfg.QAGate = true
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

	res, err := h.Run(ctx, "Generate an HTML + JavaScript battleship game that works in the browser. Keep it simple and playable.")
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	t.Logf("success=%v summary=%s failed=%d tasks=%d duration=%s",
		res.Success, res.Summary, res.FailedTasks, len(res.Board.Tasks), res.Duration)

	// A usable HTML entrypoint must exist (the original failure produced only .js).
	htmlPath := findHTMLFile(root)
	if htmlPath == "" {
		t.Fatalf("no .html entrypoint after static-web run (regression: only .js files)")
	}
	body, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read %s: %v", htmlPath, err)
	}
	lower := strings.ToLower(string(body))
	if !strings.Contains(lower, "<html") {
		t.Fatalf("%s is not real HTML (no <html>):\n%s", htmlPath, body)
	}
	if len(strings.TrimSpace(string(body))) < 120 {
		t.Fatalf("%s is suspiciously small (%d bytes):\n%s", htmlPath, len(body), body)
	}
	// It must be interactive (script or canvas) — not a static stub.
	if !strings.Contains(lower, "<script") && !strings.Contains(lower, "<canvas") {
		t.Fatalf("%s has no script/canvas — not a playable game:\n%s", htmlPath, body)
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

	// The dynamic pipeline should have persisted its composed config.
	if _, err := os.Stat(filepath.Join(root, ".slmcode", "pipeline.dynamic.yaml")); err != nil {
		t.Logf("note: pipeline.dynamic.yaml not written (composer may have fallen back): %v", err)
	}
}

// findHTMLFile returns the path of the first non-empty .html/.htm file under
// root (excluding .slmcode), or "" if none exists. Matches the web quality
// gate's "any usable HTML entrypoint" rule rather than hardcoding index.html.
func findHTMLFile(root string) string {
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".slmcode" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(d.Name())
		if (strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".htm")) && d.Name() != "" {
			if info, err := d.Info(); err == nil && info.Size() > 0 {
				found = path
			}
		}
		return nil
	})
	return found
}
