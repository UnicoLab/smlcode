package composer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func TestFitHintsFlagsWeakLocalComposition(t *testing.T) {
	comp := Composition{
		Phases: []PhaseChoice{
			{ID: "context", Enabled: true},
			{ID: "explore", Enabled: true},
			{ID: "docs", Enabled: true},
			{ID: "architect", Enabled: true},
			{ID: "plan", Enabled: true},
			{ID: "split", Enabled: true},
			{ID: "coord", Enabled: true},
			{ID: "execute", Agent: "worker", Enabled: true},
			{ID: "test", Agent: "tester", Enabled: true},
			{ID: "polish", Enabled: true},
			{ID: "memory", Enabled: true},
		},
		Execute: ExecuteChoice{DefaultRole: "worker", MaxWaves: 5},
		Slots: []pipeline.Slot{
			{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"},
		},
	}
	got := strings.Join(FitHints(comp, true, 4096), "\n")
	for _, want := range []string{
		"11 enabled phases",
		"handoff is empty",
		"team is empty",
		"worker role is generic",
		"max_waves=5",
		"4 slots",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint missing %q:\n%s", want, got)
		}
	}
}

func TestAnnotateNormalizesAndFlattensJSON(t *testing.T) {
	annotated := Annotate(Composition{
		Summary: " local ",
		Handoff: []string{" Verify with go test ./... "},
		Phases: []PhaseChoice{
			{ID: " Execute ", Agent: " Worker ", Enabled: true},
		},
		Execute: ExecuteChoice{DefaultRole: " Worker ", MaxWaves: 1},
	}, true, 8192)
	if annotated.Summary != "local" || annotated.Phases[0].ID != "execute" {
		t.Fatalf("not normalized: %+v", annotated)
	}
	body, err := json.Marshal(annotated)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{`"summary":"local"`, `"slm_fit":[`, `"phases":[`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestFitHintsReportsCompactComposition(t *testing.T) {
	got := strings.Join(FitHints(Composition{
		Handoff: []string{"Touch pkg/foo.go", "Verify with go test ./..."},
		Phases: []PhaseChoice{
			{ID: "plan", Enabled: true},
			{ID: "execute", Agent: "go-worker", Enabled: true},
			{ID: "test", Agent: "go-tester", Enabled: true},
		},
		Execute: ExecuteChoice{DefaultRole: "go-worker", MaxWaves: 2},
		Team:    []TeamMember{{Role: "go-worker"}},
	}, true, 8192), "\n")
	for _, want := range []string{"3 enabled phases selected", "2 handoff bullets"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "generic") || strings.Contains(got, "team is empty") {
		t.Fatalf("unexpected weak hints:\n%s", got)
	}
}
