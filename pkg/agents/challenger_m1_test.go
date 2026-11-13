package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

// TestChallengerTeamworkFactorySpecs empirically checks all 4 role registrations.
func TestChallengerTeamworkFactorySpecs(t *testing.T) {
	expected := []struct {
		id         string
		schemaRole string
		maxTokens  int
		maxIter    int
		temp       float64
		hasTools   bool
		isLight    bool
		isCoding   bool
	}{
		{RoleTeamworkSpec, schema.RoleTeamworkSpec, 1536, 2, 0.2, false, true, false},
		{RoleTeamworkPlan, schema.RoleTeamworkPlan, 1536, 2, 0.2, false, true, false},
		{RoleTeamworkWorker, schema.RoleTeamworkWorker, 3072, 16, 0.12, true, false, true},
		{RoleTeamworkReview, schema.RoleTeamworkReview, 1024, 2, 0.05, false, true, false},
	}

	for _, exp := range expected {
		spec := FindSpec(exp.id)
		if spec == nil {
			t.Fatalf("FindSpec(%q) returned nil", exp.id)
		}
		if spec.SchemaRole != exp.schemaRole {
			t.Errorf("Role %q: expected SchemaRole=%q, got %q", exp.id, exp.schemaRole, spec.SchemaRole)
		}
		if spec.MaxTokens != exp.maxTokens {
			t.Errorf("Role %q: expected MaxTokens=%d, got %d", exp.id, exp.maxTokens, spec.MaxTokens)
		}
		if spec.MaxIter != exp.maxIter {
			t.Errorf("Role %q: expected MaxIter=%d, got %d", exp.id, exp.maxIter, spec.MaxIter)
		}
		if spec.Temperature != exp.temp {
			t.Errorf("Role %q: expected Temperature=%v, got %v", exp.id, exp.temp, spec.Temperature)
		}
		if (len(spec.Tools) > 0) != exp.hasTools {
			t.Errorf("Role %q: expected hasTools=%v, got %v", exp.id, exp.hasTools, len(spec.Tools) > 0)
		}

		// Check alias resolution for underscore variant
		underscoreID := strings.ReplaceAll(exp.id, "-", "_")
		specUnder := FindSpec(underscoreID)
		if specUnder == nil {
			t.Errorf("FindSpec(%q) returned nil for underscore variant", underscoreID)
		} else if specUnder.ID != exp.id {
			t.Errorf("FindSpec(%q) ID = %q, want %q", underscoreID, specUnder.ID, exp.id)
		}

		// Check lightAgents set
		if lightAgents[exp.id] != exp.isLight {
			t.Errorf("Role %q: expected lightAgents=%v, got %v", exp.id, exp.isLight, lightAgents[exp.id])
		}

		// Check isCodingRole
		if isCodingRole(exp.id) != exp.isCoding {
			t.Errorf("Role %q: expected isCodingRole=%v, got %v", exp.id, exp.isCoding, isCodingRole(exp.id))
		}
	}
}

// TestChallengerTeamworkPromptContractsZeroDrift verifies prompt contract syntax, GBNF matching, and detection.
func TestChallengerTeamworkPromptContractsZeroDrift(t *testing.T) {
	prompts := []struct {
		name       string
		prompt     string
		schemaRole string
	}{
		{"spec", PromptTeamworkSpec, schema.RoleTeamworkSpec},
		{"plan", PromptTeamworkPlan, schema.RoleTeamworkPlan},
		{"worker", PromptTeamworkWorker, schema.RoleTeamworkWorker},
		{"review", PromptTeamworkReview, schema.RoleTeamworkReview},
	}

	for _, p := range prompts {
		t.Run(p.name, func(t *testing.T) {
			idx := strings.Index(p.prompt, "OUTPUT — ")
			if idx == -1 {
				t.Fatalf("Prompt %s missing OUTPUT — marker", p.name)
			}
			block := p.prompt[idx:]
			braceIdx := strings.Index(block, "{")
			if braceIdx == -1 {
				t.Fatalf("Prompt %s OUTPUT — missing JSON object open brace", p.name)
			}
			jsonStr := block[braceIdx:]
			if endIdx := strings.LastIndex(jsonStr, "}"); endIdx != -1 {
				jsonStr = jsonStr[:endIdx+1]
			}

			// 1. Must be valid JSON
			var parsed map[string]any
			if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
				t.Fatalf("Prompt %s OUTPUT — contains invalid JSON: %v", p.name, err)
			}

			// 2. Must pass ValidateSpec
			sc, ok := schema.For(p.schemaRole)
			if !ok {
				t.Fatalf("schema.For(%q) returned false", p.schemaRole)
			}
			if err := schema.ValidateSpec(sc, []byte(jsonStr)); err != nil {
				t.Fatalf("Prompt %s OUTPUT — failed ValidateSpec: %v", p.name, err)
			}

			// 3. Must pass AcceptsGBNF
			grammar, ok := schema.GBNFForRole(p.schemaRole)
			if !ok {
				t.Fatalf("schema.GBNFForRole(%q) returned false", p.schemaRole)
			}
			accepted, err := schema.AcceptsGBNF(grammar, jsonStr)
			if err != nil {
				t.Fatalf("Prompt %s AcceptsGBNF error: %v", p.name, err)
			}
			if !accepted {
				t.Fatalf("Prompt %s OUTPUT — REJECTED by its GBNF grammar", p.name)
			}

			// 4. Must be detected by DetectRole with empty hint
			detected, ok := schema.DetectRole(p.prompt, "")
			if !ok {
				t.Fatalf("schema.DetectRole(%s, \"\") returned false", p.name)
			}
			if detected.Name != p.schemaRole {
				t.Errorf("schema.DetectRole(%s, \"\") detected %q, want %q", p.name, detected.Name, p.schemaRole)
			}
		})
	}
}

// TestChallengerTeamworkWorkerPromptInvariants verifies critical guardrail directives in worker prompt.
func TestChallengerTeamworkWorkerPromptInvariants(t *testing.T) {
	requiredDirectives := []struct {
		name string
		text string
	}{
		{"AntiWanderCore", AntiWanderCore},
		{"OneToolPerTurn", OneToolPerTurn},
		{"SmokeLine", SmokeLine},
		{"EditContract", EditContract},
	}

	for _, dir := range requiredDirectives {
		if !strings.Contains(PromptTeamworkWorker, dir.text) {
			t.Errorf("PromptTeamworkWorker MISSING directive %s", dir.name)
		}
	}
}

// TestChallengerTeamworkDecodingDirectives verifies decoding configuration for each role.
func TestChallengerTeamworkDecodingDirectives(t *testing.T) {
	// Tool-less JSON roles
	for _, role := range []string{RoleTeamworkSpec, RoleTeamworkPlan, RoleTeamworkReview} {
		spec := FindSpec(role)
		if spec == nil {
			t.Fatalf("FindSpec(%q) returned nil", role)
		}
		if !spec.JSONOnly {
			t.Errorf("Role %q expected JSONOnly=true, got false", role)
		}
		if spec.SerialTools {
			t.Errorf("Role %q expected SerialTools=false, got true", role)
		}
		if len(spec.Tools) != 0 {
			t.Errorf("Role %q expected 0 tools, got %d", role, len(spec.Tools))
		}
		if len(spec.StopSequences) == 0 {
			t.Errorf("Role %q expected StopSequences to include JSONTailStop", role)
		}
	}

	// Tool-using coding worker role
	workerSpec := FindSpec(RoleTeamworkWorker)
	if workerSpec == nil {
		t.Fatalf("FindSpec(%q) returned nil", RoleTeamworkWorker)
	}
	if workerSpec.JSONOnly {
		t.Errorf("Role %q expected JSONOnly=false, got true", RoleTeamworkWorker)
	}
	if !workerSpec.SerialTools {
		t.Errorf("Role %q expected SerialTools=true, got false", RoleTeamworkWorker)
	}
	if len(workerSpec.Tools) == 0 {
		t.Errorf("Role %q expected coding tools, got 0", RoleTeamworkWorker)
	}
}
