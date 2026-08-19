package main

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/readiness"
)

func TestFormatReadinessCLIShowsFailuresAndFixes(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DynamicPipeline = false
	report := readiness.Build(cfg, 1)
	out := formatReadinessCLI(report, false)
	for _, want := range []string{
		"SLM Readiness",
		"dynamic_pipeline",
		"Static pipeline only",
		"fix: Enable dynamic pipeline",
		"slmcode readiness --fix",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReadinessCommandSupportsNoProbeAlias(t *testing.T) {
	cmd := readinessCmd()
	if cmd.Flags().Lookup("no-probe") == nil {
		t.Fatal("readiness command missing --no-probe alias")
	}
}

func TestFormatReadinessCLIShowsHintsEndpointAndDetails(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Model = "tiny-local"
	cfg.MaxParallel = 4
	cfg.ModelProfiles = map[string]config.ModelProfile{
		"default":    config.DefaultModelProfiles()["default"],
		"tiny-local": {ContextLimit: 4096, MaxTokens: 1024, MaxTurns: 8},
	}
	report := readiness.Build(cfg, 1)
	report.Checks = append(report.Checks, readiness.Check{
		ID:       "provider_model",
		Label:    "Provider Model",
		OK:       false,
		Severity: "critical",
		Message:  "omlx endpoint unavailable",
		Endpoint: "http://127.0.0.1:8000/v1",
		Latency:  42,
		FixHint:  "Start the oMLX/OpenAI-compatible server.",
		Details: map[string]interface{}{
			"provider":     "omlx",
			"model":        "tiny-local",
			"models_count": 0,
		},
	})
	out := formatReadinessCLI(report, false)
	for _, want := range []string{
		"parallel_budget",
		"hint: Lower parallelism",
		"details: context_limit=4096 max_parallel=4 recommended_max_parallel=1",
		"endpoint: http://127.0.0.1:8000/v1",
		"latency: 42 ms",
		"hint: Start the oMLX",
		"details: model=tiny-local models_count=0 provider=omlx",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatReadinessCLIShowsFixedState(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DynamicPipeline = true
	cfg.ContextCompact = true
	cfg.ReactCompact = true
	cfg.SessionEventLog = true
	cfg.WriteGuard = true
	cfg.ReadBeforeEdit = true
	cfg.FileCheckpoints = true
	cfg.ShellWriteGuard = true
	cfg.ShellWhitelist = true
	cfg.QAGate = true
	cfg.RequireSmoke = true
	cfg.ClaimsGate = true
	cfg.OverEditGuard = true
	report := readiness.Build(cfg, 1)
	out := formatReadinessCLI(report, true)
	if !strings.Contains(out, "applied readiness fixes") {
		t.Fatalf("missing fixed message:\n%s", out)
	}
	if strings.Contains(out, "slmcode readiness --fix") {
		t.Fatalf("ready report should not suggest fix:\n%s", out)
	}
}
