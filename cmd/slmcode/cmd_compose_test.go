package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func TestFormatCompositionCLIShowsAgentsHandoffAndLoop(t *testing.T) {
	resp := compositionCLIResponse{
		Mode:           "preview",
		DynamicEnabled: true,
		ModelProfile:   config.ModelProfile{ContextLimit: 8192, MaxTokens: 2048, ThinkingBudgetTokens: 512, SkillTokenBudget: 900},
		Composition: composer.Composition{
			Summary:  "focused Go pipeline",
			Strategy: "targeted edit",
			Handoff:  []string{"Touch pkg/foo.go only", "Verify with go test ./..."},
			Phases: []composer.PhaseChoice{
				{ID: "plan", Agent: "planner", Enabled: true, When: pipeline.WhenAlways},
				{ID: "execute", Agent: "go-worker", Enabled: true, When: pipeline.WhenAlways},
				{ID: "test", Agent: "go-tester", Enabled: true, When: pipeline.WhenAlways},
			},
			Execute: composer.ExecuteChoice{DefaultRole: "go-worker", Reviewer: "reviewer", Corrector: "corrector", MaxWaves: 2},
			Team: []composer.TeamMember{
				{Role: "go-worker", Skills: []string{"atomic-coding"}},
				{Role: "go-tester", Skills: []string{"multipass-quality"}},
			},
		},
	}

	out := formatCompositionCLI(resp)
	for _, want := range []string{
		"Composition Preview",
		"focused Go pipeline",
		"Touch pkg/foo.go only",
		"execute",
		"go-worker",
		"go-tester",
		"reviewer",
		"atomic-coding",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatCompositionCLIWarnsWhenDynamicDisabled(t *testing.T) {
	out := formatCompositionCLI(compositionCLIResponse{
		Mode:           "preview",
		DynamicEnabled: false,
		Composition:    composer.Composition{Summary: "preview only"},
	})
	if !strings.Contains(out, "dynamic_pipeline is disabled") {
		t.Fatalf("missing disabled warning:\n%s", out)
	}
}

func TestReadLatestComposition(t *testing.T) {
	dir := t.TempDir()
	want := composer.Composition{
		Summary: "latest",
		Handoff: []string{"Use existing files"},
		Phases:  []composer.PhaseChoice{{ID: "execute", Agent: "worker", Enabled: true}},
		Execute: composer.ExecuteChoice{DefaultRole: "worker", Reviewer: "reviewer", Corrector: "corrector"},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, composer.DynamicFileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readLatestComposition(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != want.Summary || got.Execute.DefaultRole != "worker" {
		t.Fatalf("latest composition mismatch: %+v", got)
	}
	if _, err := readLatestComposition(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected missing latest composition to fail")
	}
}
