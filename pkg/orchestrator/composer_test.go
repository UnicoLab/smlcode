package orchestrator

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func TestCompositionMarkdown(t *testing.T) {
	c := composer.Composition{
		Summary: "assemble a Go worker",
		Phases:  []composer.PhaseChoice{{ID: "execute", Agent: "worker", Enabled: true}},
		Execute: composer.ExecuteChoice{DefaultRole: "worker", Reviewer: "reviewer", Corrector: "corrector"},
		Team:    []composer.TeamMember{{Role: "worker", Skills: []string{"atomic-coding"}}},
	}
	md := compositionMarkdown(c)
	for _, want := range []string{"assemble a Go worker", "execute", "worker", "atomic-coding", "reviewer", "corrector"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestSanitizeCompositionDropsUnknownAgents(t *testing.T) {
	f := agents.NewFactory(nil, nil, "m", "p")
	o := &Orchestrator{factory: f}

	comp := &composer.Composition{
		Phases: []composer.PhaseChoice{
			{ID: "execute", Agent: "worker", Enabled: true},
			{ID: "test", Agent: "bogus-agent", Enabled: true},
		},
		Execute: composer.ExecuteChoice{DefaultRole: "worker", Reviewer: "nope", Corrector: "corrector"},
		Team: []composer.TeamMember{
			{Role: "worker", Skills: []string{"atomic-coding"}},
			{Role: "ghost", Skills: []string{"atomic-coding"}},
		},
		Slots: []pipeline.Slot{{ID: "s", Agent: "also-bogus", After: "execute"}},
	}

	dropped := o.sanitizeComposition(comp)
	joined := strings.Join(dropped, ",")
	for _, want := range []string{"bogus-agent", "nope", "ghost", "also-bogus"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in dropped %v", want, dropped)
		}
	}

	if comp.Phases[1].Agent != "" {
		t.Fatalf("unknown phase agent should be cleared: %q", comp.Phases[1].Agent)
	}
	if comp.Execute.Reviewer != "" {
		t.Fatalf("unknown reviewer should be cleared: %q", comp.Execute.Reviewer)
	}
	if comp.Phases[0].Agent != "worker" || comp.Execute.Corrector != "corrector" {
		t.Fatalf("known agents must survive: %+v", comp)
	}
	if len(comp.Team) != 1 || comp.Team[0].Role != "worker" {
		t.Fatalf("unknown team member must be dropped: %+v", comp.Team)
	}
}

func TestEnsureCriticalPhases(t *testing.T) {
	cfg := pipeline.Default()
	// Disable execute + test the way an SLM composer omission would.
	enabled := false
	for _, id := range []string{"execute", "test"} {
		ps := cfg.Phases[id]
		ps.Enabled = &enabled
		ps.When = pipeline.WhenNever
		cfg.Phases[id] = ps
	}

	got := ensureCriticalPhases(&cfg)
	joined := strings.Join(got, ",")
	for _, want := range []string{"execute", "test"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in reenabled %v", want, got)
		}
	}
	if !cfg.PhaseEnabled("execute") || !cfg.PhaseEnabled("test") {
		t.Fatalf("critical phases still disabled: %+v", cfg.Phases)
	}
	if cfg.Phases["execute"].Agent != "worker" || cfg.Phases["test"].Agent != "tester" {
		t.Fatalf("critical phases lost default agents: %+v", cfg.Phases)
	}
}

func TestEnsureCriticalPhasesNoopWhenEnabled(t *testing.T) {
	cfg := pipeline.Default()
	if got := ensureCriticalPhases(&cfg); len(got) != 0 {
		t.Fatalf("expected no-op on default pipeline, got %v", got)
	}
}

func TestQueryLanguageSpecialists(t *testing.T) {
	cases := []struct {
		query  string
		worker string
		tester string
	}{
		{"Generate an HTML + JavaScript battleship game", "web-worker", "web-tester"},
		{"Add a Rust function using cargo", "rust-worker", "rust-tester"},
		{"Create a Java class with Maven", "java-worker", "java-tester"},
		{"Build a C++ project with CMake", "cpp-worker", "cpp-tester"},
		{"Write a Python FastAPI endpoint", "python-worker", "python-tester"},
		{"A TypeScript React component", "react-worker", "react-tester"},
		{"Refactor this mystery codebase", "", ""},
	}
	for _, c := range cases {
		w, tst := queryLanguageSpecialists(c.query)
		if w != c.worker || tst != c.tester {
			t.Fatalf("query %q → (%q,%q), want (%q,%q)", c.query, w, tst, c.worker, c.tester)
		}
	}
}

func TestQueryLanguageSpecialistsJavaVsJavaScript(t *testing.T) {
	if w, _ := queryLanguageSpecialists("Add a JavaScript module"); w == "java-worker" {
		t.Fatalf("javascript must not map to java-worker")
	}
	if w, _ := queryLanguageSpecialists("Create a Java class"); w != "java-worker" {
		t.Fatalf("java must map to java-worker, got %q", w)
	}
}
