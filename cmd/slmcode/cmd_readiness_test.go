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
