package composer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/pipeline"
)

func TestParseNormalizes(t *testing.T) {
	raw := `{
		"summary": "  Build the thing ",
		"strategy": "keep it small",
		"handoff": [" Keep target files authoritative ", "keep target files authoritative"],
		"phases": [
			{"id": "EXPLORE", "enabled": true},
			{"id": "plan", "agent": "PLANNER", "enabled": true},
			{"id": "plan", "agent": "planner", "enabled": true}
		],
		"execute": {"default_role": "WORKER", "max_waves": -1},
		"team": [
			{"role": "WORKER", "skills": [" Atomic-Coding ", "atomic-coding"]},
			{"role": "worker", "skills": ["other"]}
		]
	}`
	c, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Summary != "Build the thing" {
		t.Fatalf("summary=%q", c.Summary)
	}
	if c.Execute.DefaultRole != "worker" {
		t.Fatalf("default_role=%q", c.Execute.DefaultRole)
	}
	if c.Execute.MaxWaves != 0 {
		t.Fatalf("max_waves=%d", c.Execute.MaxWaves)
	}
	if len(c.Phases) != 2 {
		t.Fatalf("phases=%d (expected dedupe to 2)", len(c.Phases))
	}
	if len(c.Team) != 1 || len(c.Team[0].Skills) != 1 {
		t.Fatalf("team=%+v", c.Team)
	}
	if len(c.Handoff) != 1 || c.Handoff[0] != "Keep target files authoritative" {
		t.Fatalf("handoff=%+v", c.Handoff)
	}
}

func TestSaveDynamicPersistsFullComposition(t *testing.T) {
	dir := t.TempDir()
	c := &Composition{
		Summary: " dynamic ",
		Handoff: []string{" Verify with go test ./... ", "Verify with go test ./..."},
		Phases: []PhaseChoice{
			{ID: " Execute ", Agent: " Go-Worker ", Enabled: true},
		},
		Team: []TeamMember{{Role: " Go-Worker ", Skills: []string{" Atomic-Coding ", "atomic-coding"}}},
	}
	if err := SaveDynamic(dir, c); err != nil {
		t.Fatal(err)
	}
	if c.Summary != " dynamic " || c.Team[0].Role != " Go-Worker " {
		t.Fatalf("SaveDynamic should not mutate caller: %+v", c)
	}
	body, err := os.ReadFile(filepath.Join(dir, DynamicFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"summary": "dynamic"`, "Verify with go test", `"id": "execute"`, `"agent": "go-worker"`, `"role": "go-worker"`, `"atomic-coding"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("saved composition missing %q:\n%s", want, body)
		}
	}
	if strings.Count(string(body), "Verify with go test") != 1 || strings.Count(string(body), "atomic-coding") != 1 {
		t.Fatalf("saved composition should be deduped:\n%s", body)
	}
}

func TestLoadDynamicNormalizesMissingAndCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := LoadDynamic(dir); err != nil || ok {
		t.Fatalf("missing ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(dir, DynamicFileName), []byte(`{"summary":" x ","phases":[{"id":" Execute ","enabled":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadDynamic(dir)
	if err != nil || !ok {
		t.Fatalf("load ok=%v err=%v", ok, err)
	}
	if got.Summary != "x" || len(got.Phases) != 1 || got.Phases[0].ID != "execute" {
		t.Fatalf("not normalized: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, DynamicFileName), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadDynamic(dir); err == nil || ok || !strings.Contains(err.Error(), "read dynamic composition") {
		t.Fatalf("corrupt ok=%v err=%v", ok, err)
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := Parse("not json at all"); err == nil {
		t.Fatal("expected error for non-JSON")
	}
}

func TestApplyDisablesOmittedPhases(t *testing.T) {
	comp := Composition{
		Summary: "tiny edit",
		Phases: []PhaseChoice{
			{ID: "context", Enabled: true},
			{ID: "plan", Agent: "planner", Enabled: true},
			{ID: "split", Agent: "splitter", Enabled: true},
			{ID: "execute", Agent: "worker", Enabled: true},
			{ID: "test", Agent: "tester", Enabled: true},
		},
		Execute: ExecuteChoice{DefaultRole: "worker", Reviewer: "reviewer", Corrector: "corrector", MaxWaves: 1},
	}
	cfg, err := Apply(comp)
	if err != nil {
		t.Fatal(err)
	}

	// Omitted non-structural phase must be disabled.
	ps := cfg.Phases["explore"]
	if ps.EnabledOrDefault() || ps.When != pipeline.WhenNever {
		t.Fatalf("explore should be disabled, got when=%q enabled=%v", ps.When, ps.Enabled)
	}
	if cfg.PhaseEnabled("explore") {
		t.Fatal("explore should be disabled")
	}

	// Enabled phase keeps its default heuristic unless forced.
	if !cfg.PhaseEnabled("plan") || cfg.Phases["plan"].Agent != "planner" {
		t.Fatalf("plan binding wrong: %+v", cfg.Phases["plan"])
	}

	if cfg.Execute.DefaultRole != "worker" || cfg.Execute.Reviewer != "reviewer" || cfg.Execute.Corrector != "corrector" {
		t.Fatalf("execute loop not applied: %+v", cfg.Execute)
	}
	if cfg.Execute.MaxWaves != 1 {
		t.Fatalf("max_waves=%d", cfg.Execute.MaxWaves)
	}
}

func TestApplyPreservesStructuralPhases(t *testing.T) {
	comp := Composition{Summary: "x", Phases: []PhaseChoice{{ID: "execute", Agent: "worker", Enabled: true}}}
	cfg, err := Apply(comp)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"init", "skills", "done"} {
		if !cfg.PhaseEnabled(id) {
			t.Fatalf("structural phase %s must stay enabled", id)
		}
	}
}

func TestApplyForcedWhenAndSlots(t *testing.T) {
	comp := Composition{
		Summary: "force explore",
		Phases: []PhaseChoice{
			{ID: "explore", Enabled: true, When: pipeline.WhenAlways},
			{ID: "execute", Agent: "go-worker", Enabled: true},
		},
		Slots: []pipeline.Slot{
			{ID: "extra-check", Agent: "tester", After: "execute", Title: "extra check"},
		},
	}
	cfg, err := Apply(comp)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhaseWhen("explore") != pipeline.WhenAlways {
		t.Fatalf("explore when=%q", cfg.PhaseWhen("explore"))
	}
	if cfg.Phases["execute"].Agent != "go-worker" {
		t.Fatalf("execute agent=%q", cfg.Phases["execute"].Agent)
	}
	if len(cfg.Slots) != 1 {
		t.Fatalf("slots=%d", len(cfg.Slots))
	}
	if got := cfg.SlotsAt("execute", "after"); len(got) != 1 {
		t.Fatalf("after slots=%d", len(got))
	}
}

func TestSkillsByRoleAndAgentSet(t *testing.T) {
	comp := Composition{
		Phases:  []PhaseChoice{{ID: "execute", Agent: "worker", Enabled: true}, {ID: "test", Agent: "tester", Enabled: true}},
		Execute: ExecuteChoice{DefaultRole: "worker", Reviewer: "reviewer", Corrector: "corrector"},
		Team: []TeamMember{
			{Role: "worker", Skills: []string{"atomic-coding", "go-modules"}},
			{Role: "tester", Skills: []string{"atomic-coding"}},
		},
		Slots: []pipeline.Slot{{ID: "lint", Agent: "go-tester", After: "test"}},
	}
	byRole := comp.SkillsByRole()
	if len(byRole["worker"]) != 2 || byRole["worker"][0] != "atomic-coding" {
		t.Fatalf("skills=%+v", byRole)
	}
	agents := comp.AgentSet()
	for _, want := range []string{"worker", "tester", "reviewer", "corrector", "go-tester"} {
		if !agents[want] {
			t.Fatalf("missing agent %q in %v", want, agents)
		}
	}
}

// TestOmittedEnabledMeansEnabled is the regression guard for the single most
// common SLM JSON slip: listing a phase without an "enabled" key.
//
// Go's zero value made that mean DISABLED, so a composer emitting
// {"id":"plan"},{"id":"split"} silently killed planning and splitting and the
// run fell through to the loop's fallback tasks. Listing a phase now means
// enabling it; only an explicit false turns one off.
func TestOmittedEnabledMeansEnabled(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantEnabled map[string]bool
	}{
		{
			name:        "every phase listed with no enabled key survives Apply",
			raw:         `{"phases":[{"id":"plan"},{"id":"split"},{"id":"execute"},{"id":"test"}]}`,
			wantEnabled: map[string]bool{"plan": true, "split": true, "execute": true, "test": true},
		},
		{
			name:        "explicit false still disables",
			raw:         `{"phases":[{"id":"plan","enabled":false},{"id":"execute"}]}`,
			wantEnabled: map[string]bool{"plan": false, "execute": true},
		},
		{
			name:        "explicit true is unchanged",
			raw:         `{"phases":[{"id":"plan","enabled":true},{"id":"execute","enabled":true}]}`,
			wantEnabled: map[string]bool{"plan": true, "execute": true},
		},
		{
			name:        "mixed keys in one document are decoded independently",
			raw:         `{"phases":[{"id":"plan"},{"id":"split","enabled":false},{"id":"execute","enabled":true}]}`,
			wantEnabled: map[string]bool{"plan": true, "split": false, "execute": true},
		},
		{
			name:        "an agent override without enabled still enables",
			raw:         `{"phases":[{"id":"execute","agent":"go-worker"},{"id":"test"}]}`,
			wantEnabled: map[string]bool{"execute": true, "test": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := Parse(tc.raw)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := map[string]bool{}
			for _, p := range comp.Phases {
				got[p.ID] = p.Enabled
			}
			for id, want := range tc.wantEnabled {
				if got[id] != want {
					t.Fatalf("phase %q Enabled = %v, want %v (parsed %+v)", id, got[id], want, comp.Phases)
				}
			}

			cfg, err := Apply(comp)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			for id, want := range tc.wantEnabled {
				ps, ok := cfg.Phases[id]
				if !ok {
					t.Fatalf("phase %q missing from the applied pipeline", id)
				}
				live := ps.EnabledOrDefault() && ps.When != pipeline.WhenNever
				if live != want {
					t.Fatalf("phase %q after Apply: enabled=%v when=%q, want live=%v",
						id, ps.EnabledOrDefault(), ps.When, want)
				}
			}
		})
	}
}

// TestGoConstructedPhaseChoiceIsUnaffected pins the decode-boundary scope of
// the default: the field is still a plain bool, so Go callers keep full
// control and pkg/orchestrator's raw-JSON workaround stays compile-compatible.
func TestGoConstructedPhaseChoiceIsUnaffected(t *testing.T) {
	p := PhaseChoice{ID: "plan"}
	if p.Enabled {
		t.Fatal("a Go-constructed PhaseChoice must keep its zero value; the default belongs to the JSON decoder")
	}
}
