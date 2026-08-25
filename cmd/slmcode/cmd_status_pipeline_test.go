package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestFormatPipelineStatusShowsDynamicPipelineState(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.ActivePack = "go"
	cfg.ActivePipeline = "go"
	cfg.Model = "qwen2.5-coder:7b"

	out := formatPipelineStatus(cfg)
	for _, want := range []string{
		"Pipeline",
		"dynamic",
		"enabled (composer selects phases, agents, slots)",
		"active_pack",
		"go",
		"active_pipeline",
		"plan_approve",
		"timeout=2m0s",
		"plan_gate",
		"pauses before execute",
		"--on-gate-timeout",
		"slm_budget",
		"context=8192",
		// The default is endpoint-aware and config.Default points at the
		// built-in LOCAL endpoint, so the measured local knee is what a
		// default config reports. This used to assert 4, which encoded the
		// pre-measurement assumption that every backend scales like a hosted
		// API.
		"max_parallel=" + strconv.Itoa(config.DefaultMaxParallelLocal),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("pipeline status missing %q:\n%s", want, out)
		}
	}
}

func TestFormatPipelineStatusExplainsStaticAndSpecialistModes(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DynamicPipeline = false
	cfg.Mode = config.ModeSpecialist
	cfg.Specialist = "tester"
	cfg.ActivePack = ""
	cfg.ActivePipeline = ""

	out := formatPipelineStatus(cfg)
	for _, want := range []string{
		"mode",
		config.ModeSpecialist,
		"specialist",
		"tester",
		"disabled (static configured pipeline)",
		"active_pack",
		"(none)",
		"active_pipeline",
		"(default)",
		"enable task-adaptive composition",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("pipeline status missing %q:\n%s", want, out)
		}
	}
}

func TestFormatPipelineStatusShowsPendingPlanApproval(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PlanApprove = "ask"
	ask := plan.PlanApproveAsk{ID: "plan-20260819T100000.000000000", TaskCount: 3}
	if err := hitl.WriteAsk(cfg.SlmDir(), "plan", ask); err != nil {
		t.Fatal(err)
	}

	out := formatPipelineStatus(cfg)
	for _, want := range []string{
		"plan_gate",
		"waiting for approval",
		"id=plan-20260819T100000.000000000",
		"tasks=3",
		"timeout=2m0s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("pipeline status missing %q:\n%s", want, out)
		}
	}
}

func TestPipelinePlanGateStatusExplainsNonBlockingModes(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PlanApprove = "auto"
	if got := pipelinePlanGateStatus(cfg); !strings.Contains(got, "execute continues") {
		t.Fatalf("auto plan gate not explained: %q", got)
	}
	cfg.PlanApprove = "off"
	if got := pipelinePlanGateStatus(cfg); !strings.Contains(got, "without approval") {
		t.Fatalf("off plan gate not explained: %q", got)
	}
	cfg.AutoApprove = true
	cfg.PlanApprove = "ask"
	if got := pipelinePlanGateStatus(cfg); !strings.Contains(got, "auto_approve=true") {
		t.Fatalf("auto_approve plan gate not explained: %q", got)
	}
}

func TestFormatLatestCompositionStatusShowsSelectedPipeline(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Model = "qwen2.5-coder:7b"
	comp := &composer.Composition{
		Summary: "focused go patch",
		Handoff: []string{
			"Touch pkg/a.go only",
			"Verify with go test ./pkg/...",
		},
		Phases: []composer.PhaseChoice{
			{ID: "execute", Agent: "go-worker", Enabled: true, When: pipeline.WhenAlways},
			{ID: "test", Agent: "go-tester", Enabled: true, When: pipeline.WhenAlways},
		},
		Execute: composer.ExecuteChoice{DefaultRole: "go-worker", Reviewer: "reviewer", Corrector: "corrector", MaxWaves: 2},
		Team:    []composer.TeamMember{{Role: "go-worker"}, {Role: "go-tester"}},
	}
	if err := composer.SaveDynamic(cfg.SlmDir(), comp); err != nil {
		t.Fatal(err)
	}
	out := formatLatestCompositionStatus(cfg)
	for _, want := range []string{
		"Latest Composition",
		"focused go patch",
		"execute@go-worker",
		"test@go-tester",
		"go-worker, go-tester",
		"worker=go-worker",
		"slm_fit",
		"2 enabled phases",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("composition status missing %q:\n%s", want, out)
		}
	}
}

func TestFormatLatestCompositionStatusIsQuietWithoutSavedComposition(t *testing.T) {
	cfg := config.Default(t.TempDir())
	if out := formatLatestCompositionStatus(cfg); out != "" {
		t.Fatalf("expected no output without saved composition, got:\n%s", out)
	}
}

func TestFormatLatestCompositionStatusWarnsOnCorruptSavedComposition(t *testing.T) {
	cfg := config.Default(t.TempDir())
	if err := os.MkdirAll(cfg.SlmDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.SlmDir(), composer.DynamicFileName), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := formatLatestCompositionStatus(cfg)
	for _, want := range []string{
		"Latest Composition",
		"could not be read",
		"read dynamic composition",
		"preview a fresh composition",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("composition warning missing %q:\n%s", want, out)
		}
	}
}
