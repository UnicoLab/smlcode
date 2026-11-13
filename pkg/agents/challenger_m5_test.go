package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

func TestEmpiricalTeamworkAgentsContractsAndConstraints(t *testing.T) {
	cases := []struct {
		name       string
		prompt     string
		roleID     string
		schemaRole string
		minTokens  int
	}{
		{"spec", PromptTeamworkSpec, RoleTeamworkSpec, schema.RoleTeamworkSpec, 1024},
		{"plan", PromptTeamworkPlan, RoleTeamworkPlan, schema.RoleTeamworkPlan, 1024},
		{"worker", PromptTeamworkWorker, RoleTeamworkWorker, schema.RoleTeamworkWorker, 2048},
		{"review", PromptTeamworkReview, RoleTeamworkReview, schema.RoleTeamworkReview, 768},
	}

	specs := Specs()
	specMap := map[string]RoleSpec{}
	for _, s := range specs {
		specMap[s.ID] = s
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := specMap[tc.roleID]
			if !ok {
				t.Fatalf("Role %q not found in Specs()", tc.roleID)
			}
			if s.SchemaRole != tc.schemaRole {
				t.Errorf("SchemaRole = %q; want %q", s.SchemaRole, tc.schemaRole)
			}
			if s.MaxTokens < tc.minTokens {
				t.Errorf("MaxTokens = %d; want at least %d", s.MaxTokens, tc.minTokens)
			}

			// Extract OUTPUT JSON block
			idx := strings.Index(tc.prompt, "OUTPUT — ")
			if idx < 0 {
				t.Fatalf("prompt %q missing OUTPUT — marker", tc.name)
			}
			block := tc.prompt[idx:]
			braceIdx := strings.Index(block, "{")
			if braceIdx < 0 {
				t.Fatalf("prompt %q missing opening brace in output block", tc.name)
			}
			jsonStr := block[braceIdx:]
			// Trim any trailing commentary
			if endIdx := strings.LastIndex(jsonStr, "}"); endIdx >= 0 {
				jsonStr = jsonStr[:endIdx+1]
			}

			// Validate JSON
			var parsed map[string]any
			if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
				t.Fatalf("Failed to unmarshal example JSON: %v", err)
			}

			// Validate against schema
			sc, ok := schema.For(tc.schemaRole)
			if !ok {
				t.Fatalf("schema role %q not found", tc.schemaRole)
			}
			if err := schema.ValidateSpec(sc, []byte(jsonStr)); err != nil {
				t.Errorf("Example JSON failed ValidateSpec: %v", err)
			}

			// Validate against GBNF
			grammar := schema.GBNF(sc)
			accepted, err := schema.AcceptsGBNF(grammar, jsonStr)
			if err != nil {
				t.Fatalf("AcceptsGBNF error: %v", err)
			}
			if !accepted {
				t.Errorf("GBNF rejected example JSON: %s", jsonStr)
			}
		})
	}
}
