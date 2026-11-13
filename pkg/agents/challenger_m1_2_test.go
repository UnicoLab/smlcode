package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/schema"
)

// TestChallenger2_SpecsRegistryIntegrity tests:
// 1. Role definitions have non-conflicting, unique IDs.
// 2. All 4 Teamwork roles are present with valid configurations.
// 3. Normalized decoding directives: JSONOnly vs SerialTools, StopSequences, SchemaRole.
func TestChallenger2_SpecsRegistryIntegrity(t *testing.T) {
	specs := Specs()
	seen := make(map[string]bool)

	teamworkRoles := map[string]struct {
		wantJSONOnly    bool
		wantSerialTools bool
		wantSchemaRole  string
		wantHasTools    bool
		minMaxTokens    int
	}{
		RoleTeamworkSpec: {
			wantJSONOnly:    true,
			wantSerialTools: false,
			wantSchemaRole:  schema.RoleTeamworkSpec,
			wantHasTools:    false,
			minMaxTokens:    1024,
		},
		RoleTeamworkPlan: {
			wantJSONOnly:    true,
			wantSerialTools: false,
			wantSchemaRole:  schema.RoleTeamworkPlan,
			wantHasTools:    false,
			minMaxTokens:    1024,
		},
		RoleTeamworkWorker: {
			wantJSONOnly:    false,
			wantSerialTools: true,
			wantSchemaRole:  schema.RoleTeamworkWorker,
			wantHasTools:    true,
			minMaxTokens:    2048,
		},
		RoleTeamworkReview: {
			wantJSONOnly:    true,
			wantSerialTools: false,
			wantSchemaRole:  schema.RoleTeamworkReview,
			wantHasTools:    false,
			minMaxTokens:    768,
		},
	}

	foundTeamwork := make(map[string]bool)

	for _, s := range specs {
		if strings.TrimSpace(s.ID) == "" {
			t.Errorf("empty role ID found in Specs()")
		}
		if seen[s.ID] {
			t.Errorf("duplicate role ID found in Specs(): %q", s.ID)
		}
		seen[s.ID] = true

		if expected, ok := teamworkRoles[s.ID]; ok {
			foundTeamwork[s.ID] = true

			if s.JSONOnly != expected.wantJSONOnly {
				t.Errorf("%s: JSONOnly = %v, want %v", s.ID, s.JSONOnly, expected.wantJSONOnly)
			}
			if s.SerialTools != expected.wantSerialTools {
				t.Errorf("%s: SerialTools = %v, want %v", s.ID, s.SerialTools, expected.wantSerialTools)
			}
			if s.SchemaRole != expected.wantSchemaRole {
				t.Errorf("%s: SchemaRole = %q, want %q", s.ID, s.SchemaRole, expected.wantSchemaRole)
			}
			hasTools := len(s.Tools) > 0
			if hasTools != expected.wantHasTools {
				t.Errorf("%s: hasTools = %v, want %v (tools: %v)", s.ID, hasTools, expected.wantHasTools, s.Tools)
			}
			if s.MaxTokens < expected.minMaxTokens {
				t.Errorf("%s: MaxTokens = %d, want at least %d", s.ID, s.MaxTokens, expected.minMaxTokens)
			}
			if s.Temperature < 0 || s.Temperature > 1.0 {
				t.Errorf("%s: invalid temperature %f", s.ID, s.Temperature)
			}
			if s.MaxIter < 1 {
				t.Errorf("%s: invalid MaxIter %d", s.ID, s.MaxIter)
			}
			if s.JSONOnly && len(s.StopSequences) == 0 {
				t.Errorf("%s: JSONOnly role missing StopSequences", s.ID)
			}
		}
	}

	for id := range teamworkRoles {
		if !foundTeamwork[id] {
			t.Errorf("required teamwork role %q not found in Specs()", id)
		}
	}
}

// TestChallenger2_RoleResolutionAndAliasing checks role resolution,
// hyphens, underscores, case normalization, and light/coding classification.
func TestChallenger2_RoleResolutionAndAliasing(t *testing.T) {
	testCases := []struct {
		query          string
		wantID         string
		wantKnown      bool
		wantLight      bool
		wantCoding     bool
		wantSchemaRole string
	}{
		// Hyphenated
		{"teamwork-spec", RoleTeamworkSpec, true, true, false, schema.RoleTeamworkSpec},
		{"teamwork-plan", RoleTeamworkPlan, true, true, false, schema.RoleTeamworkPlan},
		{"teamwork-worker", RoleTeamworkWorker, true, false, true, schema.RoleTeamworkWorker},
		{"teamwork-review", RoleTeamworkReview, true, true, false, schema.RoleTeamworkReview},

		// Underscored
		{"teamwork_spec", RoleTeamworkSpec, true, true, false, schema.RoleTeamworkSpec},
		{"teamwork_plan", RoleTeamworkPlan, true, true, false, schema.RoleTeamworkPlan},
		{"teamwork_worker", RoleTeamworkWorker, true, false, true, schema.RoleTeamworkWorker},
		{"teamwork_review", RoleTeamworkReview, true, true, false, schema.RoleTeamworkReview},

		// Mixed Case & Aliases
		{"Teamwork-Spec", RoleTeamworkSpec, true, true, false, schema.RoleTeamworkSpec},
		{"TEAMWORK_PLAN", RoleTeamworkPlan, true, true, false, schema.RoleTeamworkPlan},
		{"Teamwork_Worker", RoleTeamworkWorker, true, false, true, schema.RoleTeamworkWorker},
		{"TEAMWORK-REVIEW", RoleTeamworkReview, true, true, false, schema.RoleTeamworkReview},

		// Escalated Roles
		{EscalatedRoleID(RoleTeamworkWorker, 1), "", false, false, true, ""},
		{EscalatedRoleID(RoleTeamworkReview, 2), "", false, true, false, ""},
		{EscalatedRoleID(RoleTeamworkSpec, 1), "", false, true, false, ""},
		{EscalatedRoleID(RoleTeamworkPlan, 1), "", false, true, false, ""},

		// Non-existent
		{"teamwork-unknown", "", false, false, false, ""},
		{"teamwork_something", "", false, false, false, ""},
	}

	for _, tc := range testCases {
		t.Run(tc.query, func(t *testing.T) {
			spec := FindSpec(tc.query)
			if tc.wantID == "" {
				if spec != nil && !strings.Contains(tc.query, EscalationSuffix) {
					t.Fatalf("FindSpec(%q) = %v, want nil", tc.query, spec)
				}
			} else {
				if spec == nil {
					t.Fatalf("FindSpec(%q) returned nil, want ID %q", tc.query, tc.wantID)
				}
				if spec.ID != tc.wantID {
					t.Errorf("FindSpec(%q).ID = %q, want %q", tc.query, spec.ID, tc.wantID)
				}
			}

			isKnown := IsKnownRole(tc.query)
			if isKnown != tc.wantKnown {
				t.Errorf("IsKnownRole(%q) = %v, want %v", tc.query, isKnown, tc.wantKnown)
			}

			cleanQuery := strings.ToLower(strings.TrimSpace(tc.query))
			isLight := isLightAgent(cleanQuery)
			if isLight != tc.wantLight {
				t.Errorf("isLightAgent(%q) = %v, want %v", tc.query, isLight, tc.wantLight)
			}

			isCoding := isCodingRole(cleanQuery)
			if isCoding != tc.wantCoding {
				t.Errorf("isCodingRole(%q) = %v, want %v", tc.query, isCoding, tc.wantCoding)
			}

			if tc.wantSchemaRole != "" {
				gen := genericRole(tc.query)
				sc, ok := schema.For(gen)
				if !ok {
					t.Errorf("schema.For(genericRole(%q)=%q) failed", tc.query, gen)
				} else if sc.Name != tc.wantSchemaRole {
					t.Errorf("schema.For(%q).Name = %q, want %q", gen, sc.Name, tc.wantSchemaRole)
				}
			}
		})
	}
}

// TestChallenger2_PromptTokenBudgetsAndStructure tests:
// 1. Prompt lengths in bytes and estimated tokens are within ceilings.
// 2. Exact contract placement at the tail.
// 3. Required markers embedded in worker prompt.
func TestChallenger2_PromptTokenBudgetsAndStructure(t *testing.T) {
	prompts := map[string]struct {
		prompt    string
		maxBytes  int
		maxTokens int
	}{
		RoleTeamworkSpec:   {PromptTeamworkSpec, 1800, 450},
		RoleTeamworkPlan:   {PromptTeamworkPlan, 1800, 450},
		RoleTeamworkWorker: {PromptTeamworkWorker, 2600, 700},
		RoleTeamworkReview: {PromptTeamworkReview, 1800, 450},
	}

	for id, tc := range prompts {
		t.Run(id, func(t *testing.T) {
			byteLen := len(tc.prompt)
			if byteLen > tc.maxBytes {
				t.Errorf("%s prompt bytes = %d, exceeds ceiling %d", id, byteLen, tc.maxBytes)
			}

			tokenEst := contextstore.DefaultTokenCounter(tc.prompt)
			if tokenEst > tc.maxTokens {
				t.Errorf("%s estimated tokens = %d, exceeds ceiling %d", id, tokenEst, tc.maxTokens)
			}

			// Contract marker check
			const marker = "OUTPUT — "
			count := strings.Count(tc.prompt, marker)
			if count != 1 {
				t.Fatalf("%s has %d occurrences of %q, want exactly 1", id, count, marker)
			}

			idx := strings.LastIndex(tc.prompt, marker)
			tail := tc.prompt[idx:]
			if len(tail) > 900 {
				t.Errorf("%s tail length after marker is %d bytes (limit 900)", id, len(tail))
			}
		})
	}

	// Teamwork Worker must embed required guardrail sections
	workerReqs := []struct {
		name    string
		snippet string
	}{
		{"AntiWanderCore", AntiWanderCore},
		{"OneToolPerTurn", OneToolPerTurn},
		{"SmokeLine", SmokeLine},
		{"EditContract", "WORKED EXAMPLE"},
		{"RepairExample", "REPAIRING A FAILED EDIT"},
	}
	for _, req := range workerReqs {
		if !strings.Contains(PromptTeamworkWorker, req.snippet) {
			t.Errorf("PromptTeamworkWorker is missing required guardrail snippet %q", req.name)
		}
	}
}

// TestChallenger2_DetectRoleAdversarial tests contract identification:
// 1. With empty hint, correctly identifies all 4 roles.
// 2. With hostile/conflicting hints, correctly identifies contract from prompt body.
// 3. Prompt without contract falls back to hint.
func TestChallenger2_DetectRoleAdversarial(t *testing.T) {
	cases := []struct {
		name      string
		prompt    string
		hint      string
		wantRole  string
		wantFound bool
	}{
		{"Spec clean without hint", PromptTeamworkSpec, "", schema.RoleTeamworkSpec, true},
		{"Plan clean without hint", PromptTeamworkPlan, "", schema.RoleTeamworkPlan, true},
		{"Worker clean without hint", PromptTeamworkWorker, "", schema.RoleTeamworkWorker, true},
		{"Review clean without hint", PromptTeamworkReview, "", schema.RoleTeamworkReview, true},

		// Hostile hints (prompt is Review, but caller passed hint "worker")
		{"Review with hostile hint worker", PromptTeamworkReview, "worker", schema.RoleTeamworkReview, true},
		{"Spec with hostile hint plan", PromptTeamworkSpec, "teamwork_plan", schema.RoleTeamworkSpec, true},
		{"Plan with hostile hint review", PromptTeamworkPlan, "teamwork_review", schema.RoleTeamworkPlan, true},
		{"Worker with hostile hint spec", PromptTeamworkWorker, "teamwork_spec", schema.RoleTeamworkWorker, true},

		// Prompt without contract falls back to hint
		{"No contract falls back to hint", "Just write some code please.", "teamwork_worker", schema.RoleTeamworkWorker, true},
		{"Empty prompt falls back to hint", "", "teamwork_review", schema.RoleTeamworkReview, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detected, ok := schema.DetectRole(tc.prompt, tc.hint)
			if ok != tc.wantFound {
				t.Fatalf("DetectRole ok = %v, want %v", ok, tc.wantFound)
			}
			if ok && detected.Name != tc.wantRole {
				t.Errorf("DetectRole returned %q, want %q", detected.Name, tc.wantRole)
			}
		})
	}
}

// TestChallenger2_ContractDriftOracles tests contractDrift oracle on valid, out-of-order,
// missing-required, and unknown key payloads for all 4 Teamwork schemas.
func TestChallenger2_ContractDriftOracles(t *testing.T) {
	t.Run("teamwork_review drift oracle", func(t *testing.T) {
		sc, ok := schema.For(schema.RoleTeamworkReview)
		if !ok {
			t.Fatal("RoleTeamworkReview not registered")
		}

		// Valid document (GBNF key order: approved, score, verdict, summary, challenge_scenarios, edge_cases, issues)
		valid := `{"approved":true,"score":90,"verdict":"approve","summary":"LGTM","challenge_scenarios":["stress"],"edge_cases":["empty"],"issues":["none"]}`
		if drift := contractDrift(sc.Schema, []byte(valid), ""); drift != "" {
			t.Errorf("unexpected drift on valid doc: %s", drift)
		}

		// Swapped required keys: score before approved
		swappedReq := `{"score":90,"approved":true,"verdict":"approve","summary":"LGTM"}`
		if drift := contractDrift(sc.Schema, []byte(swappedReq), ""); drift == "" {
			t.Error("expected drift for swapped required keys (score before approved), got none")
		}

		// Optional key before required: issues before summary
		optBeforeReq := `{"approved":true,"score":90,"verdict":"approve","issues":[],"summary":"LGTM"}`
		if drift := contractDrift(sc.Schema, []byte(optBeforeReq), ""); drift == "" {
			t.Error("expected drift for optional key before required key, got none")
		}

		// Swapped optional keys: issues before challenge_scenarios (alphabetical drift)
		swappedOpt := `{"approved":true,"score":90,"verdict":"approve","summary":"LGTM","issues":[],"challenge_scenarios":[]}`
		if drift := contractDrift(sc.Schema, []byte(swappedOpt), ""); drift == "" {
			t.Error("expected drift for alphabetical optional key drift, got none")
		}

		// Unknown key
		unknownKey := `{"approved":true,"score":90,"verdict":"approve","summary":"LGTM","unknown_field":"wat"}`
		if drift := contractDrift(sc.Schema, []byte(unknownKey), ""); drift == "" {
			t.Error("expected drift for unknown key, got none")
		}
	})

	t.Run("teamwork_worker drift oracle", func(t *testing.T) {
		sc, ok := schema.For(schema.RoleTeamworkWorker)
		if !ok {
			t.Fatal("RoleTeamworkWorker not registered")
		}

		// Valid: status, summary, files_changed, action_plan, diff_summary, notes
		valid := `{"status":"done","summary":"ok","files_changed":["a.go"],"action_plan":["ws_read"],"diff_summary":"diff","notes":"note"}`
		if drift := contractDrift(sc.Schema, []byte(valid), ""); drift != "" {
			t.Errorf("unexpected drift on valid doc: %s", drift)
		}

		// Swapped required: summary before status
		swapped := `{"summary":"ok","status":"done","files_changed":["a.go"]}`
		if drift := contractDrift(sc.Schema, []byte(swapped), ""); drift == "" {
			t.Error("expected drift for summary before status, got none")
		}

		// Optional key order: notes before diff_summary
		optSwapped := `{"status":"done","summary":"ok","files_changed":["a.go"],"notes":"note","diff_summary":"diff"}`
		if drift := contractDrift(sc.Schema, []byte(optSwapped), ""); drift == "" {
			t.Error("expected drift for notes before diff_summary, got none")
		}
	})

	t.Run("teamwork_plan drift oracle with nested arrays", func(t *testing.T) {
		sc, ok := schema.For(schema.RoleTeamworkPlan)
		if !ok {
			t.Fatal("RoleTeamworkPlan not registered")
		}

		// Valid doc: strategy, milestones, file_ownership, contracts, risks
		valid := `{"strategy":"strategy_overview","milestones":[{"id":"M1","title":"T1","acceptance":"acc","depends_on":[],"description":"desc"}],"file_ownership":[{"owner":"w1","paths":["*"]}],"contracts":[{"interface_id":"I1","provider":"p1","spec":"spec","consumers":["c1"]}],"risks":["none"]}`
		if drift := contractDrift(sc.Schema, []byte(valid), ""); drift != "" {
			t.Errorf("unexpected drift on valid doc: %s", drift)
		}

		// Nested milestone drift: title before id
		nestedDrift := `{"strategy":"strategy_overview","milestones":[{"title":"T1","id":"M1","acceptance":"acc"}],"file_ownership":[{"owner":"w1","paths":["*"]}]}`
		if drift := contractDrift(sc.Schema, []byte(nestedDrift), ""); drift == "" {
			t.Error("expected drift for nested milestone key order (title before id), got none")
		}
	})

	t.Run("teamwork_spec drift oracle with nested questions", func(t *testing.T) {
		sc, ok := schema.For(schema.RoleTeamworkSpec)
		if !ok {
			t.Fatal("RoleTeamworkSpec not registered")
		}

		// Valid doc: needs_user, assumptions, acceptance, entrypoint, features, language, non_goals, questions
		valid := `{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"entrypoint":"e","features":[{"id":"F1","title":"t","acceptance":"acc","priority":"must","description":"d"}],"language":"go","non_goals":["ng"],"questions":[{"id":"q1","header":"h","question":"q","options":[{"label":"l","description":"d","recommended":true}],"allow_freeform":false,"facet":"architecture","recommended":"l"}]}`
		if drift := contractDrift(sc.Schema, []byte(valid), ""); drift != "" {
			t.Errorf("unexpected drift on valid doc: %s", drift)
		}

		// Nested option drift: description before label
		nestedOptionDrift := `{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"questions":[{"id":"q1","header":"h","question":"q","options":[{"description":"d","label":"l"}]}]}`
		if drift := contractDrift(sc.Schema, []byte(nestedOptionDrift), ""); drift == "" {
			t.Error("expected drift for nested option key order (description before label), got none")
		}
	})
}

// TestChallenger2_GBNFAcceptanceStrictness directly tests generated GBNF grammar
// against unstructured prose and fenced code blocks.
func TestChallenger2_GBNFAcceptanceStrictness(t *testing.T) {
	roles := []string{
		schema.RoleTeamworkSpec,
		schema.RoleTeamworkPlan,
		schema.RoleTeamworkWorker,
		schema.RoleTeamworkReview,
	}

	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			sc, ok := schema.For(role)
			if !ok {
				t.Fatalf("role %q not found", role)
			}
			grammar := schema.GBNF(sc)
			if strings.TrimSpace(grammar) == "" {
				t.Fatalf("empty GBNF grammar for %q", role)
			}

			// Must reject plain prose
			prose := "Sure, I have completed the task and here are the results..."
			accepted, err := schema.AcceptsGBNF(grammar, prose)
			if err != nil {
				t.Fatalf("AcceptsGBNF error: %v", err)
			}
			if accepted {
				t.Errorf("GBNF for %q accepted unstructured prose", role)
			}

			// Must reject markdown code fences around JSON
			fenced := "```json\n{}\n```"
			accepted, err = schema.AcceptsGBNF(grammar, fenced)
			if err != nil {
				t.Fatalf("AcceptsGBNF error: %v", err)
			}
			if accepted {
				t.Errorf("GBNF for %q accepted markdown code block", role)
			}
		})
	}
}

// TestChallenger2_SchemaValidationBoundaries tests boundary and invalid inputs for all 4 roles.
func TestChallenger2_SchemaValidationBoundaries(t *testing.T) {
	// Worker invalid status enum
	badWorker := map[string]any{
		"status":        "working", // only "done" or "blocked" allowed
		"summary":       "working on it",
		"files_changed": []string{"a.go"},
	}
	raw, _ := json.Marshal(badWorker)
	if err := schema.Validate(schema.RoleTeamworkWorker, raw); err == nil {
		t.Error("expected validation error for invalid status 'working', got nil")
	}

	// Review score out of bounds
	badReviewScoreLow := map[string]any{
		"approved": false,
		"score":    -5,
		"verdict":  "reject",
		"summary":  "bad",
	}
	raw, _ = json.Marshal(badReviewScoreLow)
	if err := schema.Validate(schema.RoleTeamworkReview, raw); err == nil {
		t.Error("expected validation error for score -5, got nil")
	}

	badReviewScoreHigh := map[string]any{
		"approved": true,
		"score":    101,
		"verdict":  "approve",
		"summary":  "good",
	}
	raw, _ = json.Marshal(badReviewScoreHigh)
	if err := schema.Validate(schema.RoleTeamworkReview, raw); err == nil {
		t.Error("expected validation error for score 101, got nil")
	}

	// Plan milestones minItems: 1
	emptyMilestones := map[string]any{
		"strategy":       "strategy_overview",
		"milestones":     []any{},
		"file_ownership": []any{map[string]any{"owner": "o", "paths": []string{"*"}}},
	}
	raw, _ = json.Marshal(emptyMilestones)
	if err := schema.Validate(schema.RoleTeamworkPlan, raw); err == nil {
		t.Error("expected validation error for empty milestones array, got nil")
	}

	// Spec bad feature priority enum
	badSpecPriority := map[string]any{
		"needs_user":  false,
		"assumptions": []string{"a"},
		"acceptance":  []string{"b"},
		"features": []map[string]any{
			{
				"id":         "F1",
				"title":      "title",
				"acceptance": "acc",
				"priority":   "critical", // only "must", "should", "nice" allowed
			},
		},
	}
	raw, _ = json.Marshal(badSpecPriority)
	if err := schema.Validate(schema.RoleTeamworkSpec, raw); err == nil {
		t.Error("expected validation error for invalid feature priority 'critical', got nil")
	}
}

// TestChallenger2_FactoryAndEmitsSchemaIntegration tests Factory PublicSpecs,
// AllSpecs, EmitsSchema, and genericRole prefix inheritance.
func TestChallenger2_FactoryAndEmitsSchemaIntegration(t *testing.T) {
	factory := &Factory{}

	// 1. EmitsSchema verification
	if !factory.EmitsSchema(RoleTeamworkSpec, schema.RoleTeamworkSpec) {
		t.Errorf("EmitsSchema(%q, %q) = false, want true", RoleTeamworkSpec, schema.RoleTeamworkSpec)
	}
	if !factory.EmitsSchema(RoleTeamworkPlan, schema.RoleTeamworkPlan) {
		t.Errorf("EmitsSchema(%q, %q) = false, want true", RoleTeamworkPlan, schema.RoleTeamworkPlan)
	}
	if !factory.EmitsSchema(RoleTeamworkWorker, schema.RoleTeamworkWorker) {
		t.Errorf("EmitsSchema(%q, %q) = false, want true", RoleTeamworkWorker, schema.RoleTeamworkWorker)
	}
	if !factory.EmitsSchema(RoleTeamworkReview, schema.RoleTeamworkReview) {
		t.Errorf("EmitsSchema(%q, %q) = false, want true", RoleTeamworkReview, schema.RoleTeamworkReview)
	}

	// Mismatches must return false
	if factory.EmitsSchema(RoleTeamworkWorker, schema.RoleWorker) {
		t.Errorf("EmitsSchema(%q, %q) = true, want false", RoleTeamworkWorker, schema.RoleWorker)
	}
	if factory.EmitsSchema(RoleTeamworkReview, schema.RoleReview) {
		t.Errorf("EmitsSchema(%q, %q) = true, want false", RoleTeamworkReview, schema.RoleReview)
	}

	// 2. PublicSpecs must contain all 4 roles without prompts
	public := PublicSpecs()
	foundInPublic := make(map[string]bool)
	for _, p := range public {
		id, _ := p["id"].(string)
		if strings.HasPrefix(id, "teamwork-") {
			foundInPublic[id] = true
			if prompt, has := p["system_prompt"]; has && prompt != "" {
				t.Errorf("PublicSpecs for %q leaked system_prompt", id)
			}
		}
	}
	for _, id := range []string{RoleTeamworkSpec, RoleTeamworkPlan, RoleTeamworkWorker, RoleTeamworkReview} {
		if !foundInPublic[id] {
			t.Errorf("Role %q not found in PublicSpecs()", id)
		}
	}

	// 3. Language-specialized role prefix inheritance via genericRole
	cases := []struct {
		specialized string
		wantBase    string
	}{
		{"go-teamwork-worker", "teamwork-worker"},
		{"python-teamwork-spec", "teamwork-spec"},
		{"rust-teamwork-plan", "teamwork-plan"},
		{"ts-teamwork-review", "teamwork-review"},
	}
	for _, tc := range cases {
		gen := genericRole(tc.specialized)
		if gen != tc.wantBase {
			t.Errorf("genericRole(%q) = %q, want %q", tc.specialized, gen, tc.wantBase)
		}
	}
}

// TestChallenger2_GBNFDeterminism verifies that GBNF grammar generation is byte-deterministic.
func TestChallenger2_GBNFDeterminism(t *testing.T) {
	roles := []string{
		schema.RoleTeamworkSpec,
		schema.RoleTeamworkPlan,
		schema.RoleTeamworkWorker,
		schema.RoleTeamworkReview,
	}

	for _, role := range roles {
		sc, ok := schema.For(role)
		if !ok {
			t.Fatalf("Role %q not found", role)
		}
		baseline := schema.GBNF(sc)
		for i := 0; i < 25; i++ {
			iteration := schema.GBNF(sc)
			if iteration != baseline {
				t.Fatalf("GBNF for %q is non-deterministic at iteration %d", role, i)
			}
		}
	}
}
