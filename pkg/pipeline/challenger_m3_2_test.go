package pipeline

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestChallenger_TeamworkYamlAndConstructorParity performs an exhaustive, field-by-field
// comparison between pkg/blocks/bundled/pipelines/teamwork.yaml and pipeline.Teamwork().
func TestChallenger_TeamworkYamlAndConstructorParity(t *testing.T) {
	yamlPath := filepath.Join("..", "blocks", "bundled", "pipelines", "teamwork.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("failed to read teamwork.yaml: %v", err)
	}

	// Unmarshal YAML block wrapper
	var block struct {
		APIVersion  string `yaml:"api_version"`
		Kind        string `yaml:"kind"`
		ID          string `yaml:"id"`
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Version     string `yaml:"version"`
		Spec        Config `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &block); err != nil {
		t.Fatalf("failed to unmarshal teamwork.yaml: %v", err)
	}

	yamlCfg := block.Spec
	goCfg := Teamwork()

	// 1. Version check
	if yamlCfg.Version != goCfg.Version {
		t.Errorf("Version mismatch: yaml=%d, go=%d", yamlCfg.Version, goCfg.Version)
	}

	// 2. Order check (exact sequence of 16 phases)
	wantPhases := []string{
		"init", "skills", "context", "explore", "docs", "clarify",
		"architect", "plan", "split", "coord", "execute", "learn",
		"polish", "test", "memory", "done",
	}
	if len(yamlCfg.Order) != len(wantPhases) {
		t.Fatalf("yaml Order length = %d, want %d", len(yamlCfg.Order), len(wantPhases))
	}
	if len(goCfg.Order) != len(wantPhases) {
		t.Fatalf("go Order length = %d, want %d", len(goCfg.Order), len(wantPhases))
	}
	for i, phase := range wantPhases {
		if yamlCfg.Order[i] != phase {
			t.Errorf("yaml Order[%d] = %q, want %q", i, yamlCfg.Order[i], phase)
		}
		if goCfg.Order[i] != phase {
			t.Errorf("go Order[%d] = %q, want %q", i, goCfg.Order[i], phase)
		}
	}

	// 3. Groups check (5 groups with exact steps and labels)
	if len(yamlCfg.Groups) != 5 {
		t.Fatalf("yaml Groups count = %d, want 5", len(yamlCfg.Groups))
	}
	if len(goCfg.Groups) != 5 {
		t.Fatalf("go Groups count = %d, want 5", len(goCfg.Groups))
	}
	for i := range yamlCfg.Groups {
		yg := yamlCfg.Groups[i]
		gg := goCfg.Groups[i]
		if yg.ID != gg.ID {
			t.Errorf("Group[%d] ID mismatch: yaml=%q, go=%q", i, yg.ID, gg.ID)
		}
		if yg.Label != gg.Label {
			t.Errorf("Group[%d] Label mismatch: yaml=%q, go=%q", i, yg.Label, gg.Label)
		}
		if !reflect.DeepEqual(yg.Steps, gg.Steps) {
			t.Errorf("Group[%d] Steps mismatch: yaml=%v, go=%v", i, yg.Steps, gg.Steps)
		}
	}

	// 4. Exhaustive phase comparison across all 16 phases
	for _, phase := range wantPhases {
		yp, okY := yamlCfg.Phases[phase]
		if !okY {
			t.Errorf("Phase %q missing in teamwork.yaml", phase)
			continue
		}
		gp, okG := goCfg.Phases[phase]
		if !okG {
			t.Errorf("Phase %q missing in Teamwork() Go constructor", phase)
			continue
		}

		if yp.Agent != gp.Agent {
			t.Errorf("Phase %q Agent mismatch: yaml=%q, go=%q", phase, yp.Agent, gp.Agent)
		}
		if yp.When != gp.When {
			t.Errorf("Phase %q When mismatch: yaml=%q, go=%q", phase, yp.When, gp.When)
		}
		if yp.Label != gp.Label {
			t.Errorf("Phase %q Label mismatch: yaml=%q, go=%q", phase, yp.Label, gp.Label)
		}
		if yp.Tip != gp.Tip {
			t.Errorf("Phase %q Tip mismatch: yaml=%q, go=%q", phase, yp.Tip, gp.Tip)
		}
		if yp.Group != gp.Group {
			t.Errorf("Phase %q Group mismatch: yaml=%q, go=%q", phase, yp.Group, gp.Group)
		}
		if yp.EnabledOrDefault() != gp.EnabledOrDefault() {
			t.Errorf("Phase %q EnabledOrDefault mismatch: yaml=%v, go=%v", phase, yp.EnabledOrDefault(), gp.EnabledOrDefault())
		}
	}

	// 5. ExecuteLoop check
	if yamlCfg.Execute.DefaultRole != goCfg.Execute.DefaultRole {
		t.Errorf("Execute.DefaultRole mismatch: yaml=%q, go=%q", yamlCfg.Execute.DefaultRole, goCfg.Execute.DefaultRole)
	}
	if yamlCfg.Execute.Reviewer != goCfg.Execute.Reviewer {
		t.Errorf("Execute.Reviewer mismatch: yaml=%q, go=%q", yamlCfg.Execute.Reviewer, goCfg.Execute.Reviewer)
	}
	if yamlCfg.Execute.Corrector != goCfg.Execute.Corrector {
		t.Errorf("Execute.Corrector mismatch: yaml=%q, go=%q", yamlCfg.Execute.Corrector, goCfg.Execute.Corrector)
	}
	if yamlCfg.Execute.MaxWaves != goCfg.Execute.MaxWaves {
		t.Errorf("Execute.MaxWaves mismatch: yaml=%d, go=%d", yamlCfg.Execute.MaxWaves, goCfg.Execute.MaxWaves)
	}
}

// TestChallenger_PipelineUnmarshalStress tests unmarshaling resilience with malformed,
// unexpected, and boundary inputs.
func TestChallenger_PipelineUnmarshalStress(t *testing.T) {
	// Subtest 1: Malformed syntax YAML
	t.Run("malformed_yaml_syntax", func(t *testing.T) {
		malformed := `
version: 1
phases: [unclosed list
order: {bad: dictionary}
`
		var cfg Config
		if err := yaml.Unmarshal([]byte(malformed), &cfg); err == nil {
			t.Fatal("expected yaml.Unmarshal to fail on syntax error, but got nil")
		}
	})

	// Subtest 2: Extra unknown fields should unmarshal without error into Config struct
	t.Run("extra_unknown_fields", func(t *testing.T) {
		extraYAML := `
version: 1
unknown_future_field: "some-value"
another_arbitrary_key: 12345
phases:
  clarify:
    agent: "teamwork-spec"
    when: "always"
    label: "Clarify"
    unknown_nested_flag: true
order: ["clarify"]
`
		var cfg Config
		if err := yaml.Unmarshal([]byte(extraYAML), &cfg); err != nil {
			t.Fatalf("unexpected unmarshal failure on extra fields: %v", err)
		}
		if cfg.Phases["clarify"].Agent != "teamwork-spec" {
			t.Errorf("expected clarify agent to be teamwork-spec, got %q", cfg.Phases["clarify"].Agent)
		}
	})

	// Subtest 3: Validation catches invalid group scenarios
	t.Run("group_validation_stress", func(t *testing.T) {
		// A: Empty group ID
		cEmpty := Teamwork()
		cEmpty.Groups = append(cEmpty.Groups, GroupMeta{ID: "", Label: "Empty", Steps: []string{"init"}})
		if err := cEmpty.Validate(); err == nil || !strings.Contains(err.Error(), "empty id") {
			t.Errorf("expected error for empty group id, got %v", err)
		}

		// B: Duplicate group ID
		cDup := Teamwork()
		cDup.Groups = append(cDup.Groups, GroupMeta{ID: GroupInterview, Label: "Duplicate", Steps: []string{}})
		if err := cDup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate group id") {
			t.Errorf("expected error for duplicate group id, got %v", err)
		}

		// C: Group referencing unknown phase
		cUnknown := Teamwork()
		cUnknown.Groups[0].Steps = append(cUnknown.Groups[0].Steps, "non_existent_phase")
		if err := cUnknown.Validate(); err == nil || !strings.Contains(err.Error(), "unknown phase") {
			t.Errorf("expected error for unknown phase in group, got %v", err)
		}

		// D: Phase assigned to multiple groups
		cOverlap := Teamwork()
		cOverlap.Groups[1].Steps = append(cOverlap.Groups[1].Steps, "clarify") // clarify is already in interview
		if err := cOverlap.Validate(); err == nil || !strings.Contains(err.Error(), "assigned to multiple groups") {
			t.Errorf("expected error for phase assigned to multiple groups, got %v", err)
		}
	})

	// Subtest 4: Validation catches invalid slot configurations
	t.Run("slot_validation_stress", func(t *testing.T) {
		// A: Invalid slot ID format (uppercase, bad symbols)
		cBadSlotID := Teamwork()
		cBadSlotID.Slots = []Slot{{ID: "INVALID_SLOT!", Agent: "custom", Before: "clarify"}}
		if err := cBadSlotID.Validate(); err == nil || !strings.Contains(err.Error(), "invalid id") {
			t.Errorf("expected error for invalid slot id, got %v", err)
		}

		// B: Slot missing agent
		cNoAgent := Teamwork()
		cNoAgent.Slots = []Slot{{ID: "valid-slot", Agent: "", Before: "clarify"}}
		if err := cNoAgent.Validate(); err == nil || !strings.Contains(err.Error(), "agent required") {
			t.Errorf("expected error for slot without agent, got %v", err)
		}

		// C: Slot unpositioned (no before, after, or replace)
		cUnanchored := Teamwork()
		cUnanchored.Slots = []Slot{{ID: "valid-slot", Agent: "custom"}}
		if err := cUnanchored.Validate(); err == nil || !strings.Contains(err.Error(), "set before, after, or replace") {
			t.Errorf("expected error for unanchored slot, got %v", err)
		}

		// D: Slot anchoring to unknown phase
		cUnknownAnchor := Teamwork()
		cUnknownAnchor.Slots = []Slot{{ID: "valid-slot", Agent: "custom", Before: "mars_rover_phase"}}
		if err := cUnknownAnchor.Validate(); err == nil || !strings.Contains(err.Error(), "unknown phase anchor") {
			t.Errorf("expected error for unknown phase anchor, got %v", err)
		}
	})
}

// TestChallenger_PipelineNormalizeAndDefaultMergeStress tests Normalize() semantics
// ensuring defaults do not clobber user overrides, and missing fields are filled cleanly.
func TestChallenger_PipelineNormalizeAndDefaultMergeStress(t *testing.T) {
	// A partial config specifying only a custom clarify agent and empty groups
	partial := Config{
		Version: 1,
		Phases: map[string]PhaseSpec{
			"clarify": {Agent: "custom-clarifier", When: WhenAlways},
		},
		Order: []string{"clarify"},
	}

	partial.Normalize()

	// Validation must pass after normalization
	if err := partial.Validate(); err != nil {
		t.Fatalf("validation failed after normalize: %v", err)
	}

	// Custom clarify agent must NOT be overwritten by default
	if partial.Phases["clarify"].Agent != "custom-clarifier" {
		t.Errorf("custom clarify agent was overwritten: got %q", partial.Phases["clarify"].Agent)
	}

	// Other phases (e.g. init, plan, test) must be backfilled from Default()
	if _, ok := partial.Phases["plan"]; !ok {
		t.Errorf("expected missing phase 'plan' to be backfilled by Normalize()")
	}

	// Order was provided with 1 item, Normalize() preserves provided order
	if len(partial.Order) != 1 || partial.Order[0] != "clarify" {
		t.Errorf("provided order should be preserved: got %v", partial.Order)
	}
}
