package schema

import (
	"encoding/json"
	"testing"
)

// TestChallengerAllFourRolesExistAndStrict verifies registration and strictness.
func TestChallengerAllFourRolesExistAndStrict(t *testing.T) {
	roles := []struct {
		role   string
		strict bool
	}{
		{RoleTeamworkSpec, false},
		{RoleTeamworkPlan, true},
		{RoleTeamworkWorker, true},
		{RoleTeamworkReview, true},
	}

	for _, tc := range roles {
		spec, ok := For(tc.role)
		if !ok {
			t.Fatalf("Role %q not found in registry", tc.role)
		}
		if spec.Strict != tc.strict {
			t.Errorf("Role %q: expected Strict=%v, got %v", tc.role, tc.strict, spec.Strict)
		}
	}
}

// TestChallengerAliasNormalizationEdgeCases tests backward compatibility and aliases.
func TestChallengerAliasNormalizationEdgeCases(t *testing.T) {
	// 1. Teamwork aliases must resolve to Teamwork roles
	teamworkMappings := []struct {
		input string
		want  string
	}{
		{"teamwork_spec", RoleTeamworkSpec},
		{"teamwork-spec", RoleTeamworkSpec},
		{"spec", RoleTeamworkSpec},
		{"interview", RoleTeamworkSpec},
		{"teamwork_interview", RoleTeamworkSpec},
		{"teamwork-interview", RoleTeamworkSpec},
		{" TEAMWORK_SPEC ", RoleTeamworkSpec},
		{"\tTeamwork-Spec\n", RoleTeamworkSpec},

		{"teamwork_plan", RoleTeamworkPlan},
		{"teamwork-plan", RoleTeamworkPlan},
		{"architecture", RoleTeamworkPlan},
		{"teamwork_architecture", RoleTeamworkPlan},
		{"teamwork-architecture", RoleTeamworkPlan},
		{"teamwork_architect", RoleTeamworkPlan},
		{"teamwork-architect", RoleTeamworkPlan},
		{" TEAMWORK_PLAN ", RoleTeamworkPlan},

		{"teamwork_worker", RoleTeamworkWorker},
		{"teamwork-worker", RoleTeamworkWorker},
		{"teamwork_execution", RoleTeamworkWorker},
		{"teamwork-execution", RoleTeamworkWorker},
		{" TEAMWORK_WORKER ", RoleTeamworkWorker},

		{"teamwork_review", RoleTeamworkReview},
		{"teamwork-review", RoleTeamworkReview},
		{"adversarial_review", RoleTeamworkReview},
		{"adversarial-review", RoleTeamworkReview},
		{"adversarial_reviewer", RoleTeamworkReview},
		{"adversarial-reviewer", RoleTeamworkReview},
		{"challenger", RoleTeamworkReview},
		{" CHALLENGER ", RoleTeamworkReview},
	}

	for _, tc := range teamworkMappings {
		spec, ok := For(tc.input)
		if !ok {
			t.Errorf("For(%q) returned false, want true", tc.input)
			continue
		}
		if spec.Name != tc.want {
			t.Errorf("For(%q) = %q, want %q", tc.input, spec.Name, tc.want)
		}
	}

	// 2. CRITICAL BACKWARD COMPATIBILITY: Core legacy roles must NOT be hijacked
	legacyMappings := []struct {
		input string
		want  string
	}{
		{"plan", RolePlan},
		{"worker", RoleWorker},
		{"review", RoleReview},
		{"interviewer", RoleClarify},
		{"architect", RoleArchitect},
		{"clarifier", RoleClarify},
		{"reviewer", RoleReview},
		{"splitter", RoleTasks},
	}

	for _, tc := range legacyMappings {
		spec, ok := For(tc.input)
		if !ok {
			t.Errorf("Legacy For(%q) returned false", tc.input)
			continue
		}
		if spec.Name != tc.want {
			t.Errorf("REGRESSION: Legacy For(%q) hijacked to %q, must remain %q", tc.input, spec.Name, tc.want)
		}
	}
}

// TestChallengerGBNFStrictPropertyOrder tests that out-of-order keys are rejected.
func TestChallengerGBNFStrictPropertyOrder(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		valid   string
		mutated []string
	}{
		{
			name:  "teamwork_spec top-level and optional order",
			role:  RoleTeamworkSpec,
			valid: `{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"entrypoint":"c","features":[{"id":"F1","title":"t","acceptance":"a","priority":"must","description":"d"}],"language":"go","non_goals":["ng"],"questions":[{"id":"q1","header":"h","question":"q","options":[{"label":"l1","description":"d1","recommended":true},{"label":"l2","description":"d2"}],"allow_freeform":false,"facet":"architecture","recommended":"r"}]}`,
			mutated: []string{
				// Inverted top-level required
				`{"assumptions":["a"],"needs_user":false,"acceptance":["b"]}`,
				`{"acceptance":["b"],"needs_user":false,"assumptions":["a"]}`,
				`{"needs_user":false,"acceptance":["b"],"assumptions":["a"]}`,
				// Inverted top-level optional
				`{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"language":"go","entrypoint":"c"}`,
				`{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"questions":[],"non_goals":[]}`,
				// Inverted nested features: title before id
				`{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"features":[{"title":"t","id":"F1","acceptance":"a","priority":"must"}]}`,
				// Inverted nested features: priority before acceptance
				`{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"features":[{"id":"F1","title":"t","priority":"must","acceptance":"a"}]}`,
				// Inverted nested features: description before priority
				`{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"features":[{"id":"F1","title":"t","acceptance":"a","description":"d","priority":"must"}]}`,
				// Inverted nested questions: header before id
				`{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"questions":[{"header":"h","id":"q1","question":"q","options":[{"label":"l1","description":"d1"},{"label":"l2","description":"d2"}]}]}`,
				// Inverted nested questions: facet before allow_freeform
				`{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"questions":[{"id":"q1","header":"h","question":"q","options":[{"label":"l1","description":"d1"},{"label":"l2","description":"d2"}],"facet":"scope","allow_freeform":false}]}`,
				// Inverted nested options: description before label
				`{"needs_user":false,"assumptions":["a"],"acceptance":["b"],"questions":[{"id":"q1","header":"h","question":"q","options":[{"description":"d1","label":"l1"},{"label":"l2","description":"d2"}]}]}`,
			},
		},
		{
			name:  "teamwork_plan top-level and nested order",
			role:  RoleTeamworkPlan,
			valid: `{"strategy":"approach","milestones":[{"id":"M1","title":"t","acceptance":"a","depends_on":[],"description":"d"}],"file_ownership":[{"owner":"o","paths":["p"]}],"contracts":[{"interface_id":"i","provider":"p","spec":"s","consumers":["c"]}],"risks":["r"]}`,
			mutated: []string{
				// Inverted top-level required: milestones before strategy
				`{"milestones":[{"id":"M1","title":"t","acceptance":"a"}],"strategy":"approach","file_ownership":[{"owner":"o","paths":["p"]}]}`,
				// Inverted top-level required: file_ownership before milestones
				`{"strategy":"approach","file_ownership":[{"owner":"o","paths":["p"]}],"milestones":[{"id":"M1","title":"t","acceptance":"a"}]}`,
				// Inverted optional: risks before contracts
				`{"strategy":"approach","milestones":[{"id":"M1","title":"t","acceptance":"a"}],"file_ownership":[{"owner":"o","paths":["p"]}],"risks":["r"],"contracts":[]}`,
				// Inverted milestones nested: title before id
				`{"strategy":"approach","milestones":[{"title":"t","id":"M1","acceptance":"a"}],"file_ownership":[{"owner":"o","paths":["p"]}]}`,
				// Inverted milestones nested: description before depends_on
				`{"strategy":"approach","milestones":[{"id":"M1","title":"t","acceptance":"a","description":"d","depends_on":[]}],"file_ownership":[{"owner":"o","paths":["p"]}]}`,
				// Inverted file_ownership nested: paths before owner
				`{"strategy":"approach","milestones":[{"id":"M1","title":"t","acceptance":"a"}],"file_ownership":[{"paths":["p"],"owner":"o"}]}`,
				// Inverted contracts nested: provider before interface_id
				`{"strategy":"approach","milestones":[{"id":"M1","title":"t","acceptance":"a"}],"file_ownership":[{"owner":"o","paths":["p"]}],"contracts":[{"provider":"p","interface_id":"i","spec":"s"}]}`,
				// Inverted contracts nested: consumers before spec
				`{"strategy":"approach","milestones":[{"id":"M1","title":"t","acceptance":"a"}],"file_ownership":[{"owner":"o","paths":["p"]}],"contracts":[{"interface_id":"i","provider":"p","consumers":["c"],"spec":"s"}]}`,
			},
		},
		{
			name:  "teamwork_worker top-level order",
			role:  RoleTeamworkWorker,
			valid: `{"status":"done","summary":"sum","files_changed":["f.go"],"action_plan":["a"],"diff_summary":"d","notes":"n"}`,
			mutated: []string{
				// summary before status
				`{"summary":"sum","status":"done","files_changed":["f.go"]}`,
				// files_changed before summary
				`{"status":"done","files_changed":["f.go"],"summary":"sum"}`,
				// optional out of order: diff_summary before action_plan
				`{"status":"done","summary":"sum","files_changed":["f.go"],"diff_summary":"d","action_plan":["a"]}`,
				// optional out of order: notes before diff_summary
				`{"status":"done","summary":"sum","files_changed":["f.go"],"notes":"n","diff_summary":"d"}`,
			},
		},
		{
			name:  "teamwork_review top-level order",
			role:  RoleTeamworkReview,
			valid: `{"approved":true,"score":90,"verdict":"approve","summary":"good","challenge_scenarios":["cs"],"edge_cases":["ec"],"issues":["iss"]}`,
			mutated: []string{
				// score before approved
				`{"score":90,"approved":true,"verdict":"approve","summary":"good"}`,
				// verdict before score
				`{"approved":true,"verdict":"approve","score":90,"summary":"good"}`,
				// summary before verdict
				`{"approved":true,"score":90,"summary":"good","verdict":"approve"}`,
				// edge_cases before challenge_scenarios
				`{"approved":true,"score":90,"verdict":"approve","summary":"good","edge_cases":["ec"],"challenge_scenarios":["cs"]}`,
				// issues before edge_cases
				`{"approved":true,"score":90,"verdict":"approve","summary":"good","issues":["iss"],"edge_cases":["ec"]}`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			grammar, ok := GBNFForRole(tc.role)
			if !ok {
				t.Fatalf("GBNFForRole(%q) returned false", tc.role)
			}

			// 1. Verify valid input passes
			pass, err := AcceptsGBNF(grammar, tc.valid)
			if err != nil {
				t.Fatalf("AcceptsGBNF parse error on valid fixture: %v", err)
			}
			if !pass {
				t.Fatalf("AcceptsGBNF rejected valid fixture: %s", tc.valid)
			}

			// 2. Verify all mutated (out-of-order) instances are rejected
			for i, mut := range tc.mutated {
				mutPass, mutErr := AcceptsGBNF(grammar, mut)
				if mutErr != nil {
					t.Fatalf("mutation %d AcceptsGBNF returned error: %v", i, mutErr)
				}
				if mutPass {
					t.Errorf("mutation %d: GBNF SHOULD HAVE REJECTED out-of-order keys, but accepted: %s", i, mut)
				}
			}
		})
	}
}

// TestChallengerGBNFEnumAndBoundaryViolations tests that enums and boundary constraints are enforced.
func TestChallengerGBNFEnumAndBoundaryViolations(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		rejected []string
	}{
		{
			name: "teamwork_worker bad status enum",
			role: RoleTeamworkWorker,
			rejected: []string{
				`{"status":"in_progress","summary":"s","files_changed":[]}`,
				`{"status":"finished","summary":"s","files_changed":[]}`,
				`{"status":"completed","summary":"s","files_changed":[]}`,
				`{"status":"failed","summary":"s","files_changed":[]}`,
				`{"status":"","summary":"s","files_changed":[]}`,
			},
		},
		{
			name: "teamwork_review bad verdict enum",
			role: RoleTeamworkReview,
			rejected: []string{
				`{"approved":true,"score":80,"verdict":"passed","summary":"s"}`,
				`{"approved":true,"score":80,"verdict":"accepted","summary":"s"}`,
				`{"approved":false,"score":20,"verdict":"failed","summary":"s"}`,
				`{"approved":false,"score":20,"verdict":"","summary":"s"}`,
			},
		},
		{
			name: "teamwork_spec bad priority and facet enums",
			role: RoleTeamworkSpec,
			rejected: []string{
				`{"needs_user":false,"assumptions":[],"acceptance":[],"features":[{"id":"1","title":"t","acceptance":"a","priority":"critical"}]}`,
				`{"needs_user":false,"assumptions":[],"acceptance":[],"features":[{"id":"1","title":"t","acceptance":"a","priority":"low"}]}`,
				`{"needs_user":false,"assumptions":[],"acceptance":[],"questions":[{"id":"1","header":"h","question":"q","options":[{"label":"l","description":"d"}],"facet":"security"}]}`,
				`{"needs_user":false,"assumptions":[],"acceptance":[],"questions":[{"id":"1","header":"h","question":"q","options":[{"label":"l","description":"d"}],"facet":"performance"}]}`,
			},
		},
		{
			name: "teamwork_plan empty milestones",
			role: RoleTeamworkPlan,
			rejected: []string{
				`{"strategy":"s","milestones":[],"file_ownership":[{"owner":"o","paths":["p"]}]}`,
			},
		},
		{
			name: "teamwork_spec options minItems violation",
			role: RoleTeamworkSpec,
			rejected: []string{
				`{"needs_user":false,"assumptions":[],"acceptance":[],"questions":[{"id":"1","header":"h","question":"q","options":[]}]}`,
				`{"needs_user":false,"assumptions":[],"acceptance":[],"questions":[{"id":"1","header":"h","question":"q","options":[{"label":"l","description":"d"}]}]}`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			grammar, ok := GBNFForRole(tc.role)
			if !ok {
				t.Fatalf("GBNFForRole(%q) returned false", tc.role)
			}
			for i, rej := range tc.rejected {
				pass, err := AcceptsGBNF(grammar, rej)
				if err != nil {
					t.Fatalf("rejection %d error: %v", i, err)
				}
				if pass {
					t.Errorf("GBNF SHOULD HAVE REJECTED violation %d: %s", i, rej)
				}
			}
		})
	}
}

// TestChallengerCoerceRobustness stress-tests CoerceSpec on messy SLM slips without crashing.
func TestChallengerCoerceRobustness(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		raw       string
		checkFunc func(t *testing.T, out map[string]any)
	}{
		{
			name: "teamwork_review string booleans and score percent",
			role: RoleTeamworkReview,
			raw:  `{"approved":"true","score":"85%","verdict":"approve","summary":"all tests green"}`,
			checkFunc: func(t *testing.T, out map[string]any) {
				if out["approved"] != true {
					t.Errorf("expected approved: true, got %v (%T)", out["approved"], out["approved"])
				}
				switch s := out["score"].(type) {
				case float64:
					if s != 85 {
						t.Errorf("expected score 85, got %v", s)
					}
				case int64:
					if s != 85 {
						t.Errorf("expected score 85, got %v", s)
					}
				default:
					t.Errorf("unexpected score type: %T (%v)", s, s)
				}
			},
		},
		{
			name: "teamwork_review string boolean 'pass' and negative string",
			role: RoleTeamworkReview,
			raw:  `{"approved":"pass","score":"95","verdict":"approve","summary":"s"}`,
			checkFunc: func(t *testing.T, out map[string]any) {
				if out["approved"] != true {
					t.Errorf("expected approved: true, got %v", out["approved"])
				}
			},
		},
		{
			name: "teamwork_review string boolean 'rejected' and 0 score",
			role: RoleTeamworkReview,
			raw:  `{"approved":"rejected","score":"0%","verdict":"reject","summary":"broken"}`,
			checkFunc: func(t *testing.T, out map[string]any) {
				if out["approved"] != false {
					t.Errorf("expected approved: false, got %v", out["approved"])
				}
				score, _ := out["score"].(int64)
				if score != 0 {
					t.Errorf("expected score: 0, got %v", out["score"])
				}
			},
		},
		{
			name: "teamwork_worker null array and comma separated string array",
			role: RoleTeamworkWorker,
			raw:  `{"status":"done","summary":"fixed","files_changed":"pkg/schema/spec.go, pkg/schema/validate.go","action_plan":null}`,
			checkFunc: func(t *testing.T, out map[string]any) {
				fc, ok := out["files_changed"].([]any)
				if !ok || len(fc) != 2 {
					t.Errorf("expected files_changed to be coerced to 2-item array, got %v", out["files_changed"])
				}
				ap, ok := out["action_plan"].([]any)
				if !ok || len(ap) != 0 {
					t.Errorf("expected action_plan to be coerced from null to empty array, got %v", out["action_plan"])
				}
			},
		},
		{
			name: "teamwork_worker single scalar string to array",
			role: RoleTeamworkWorker,
			raw:  `{"status":"done","summary":"done","files_changed":"main.go"}`,
			checkFunc: func(t *testing.T, out map[string]any) {
				fc, ok := out["files_changed"].([]any)
				if !ok || len(fc) != 1 || fc[0] != "main.go" {
					t.Errorf("expected files_changed: ['main.go'], got %v", out["files_changed"])
				}
			},
		},
		{
			name: "teamwork_spec string boolean and null assumptions/acceptance",
			role: RoleTeamworkSpec,
			raw:  `{"needs_user":"no","assumptions":null,"acceptance":"go test ./...","non_goals":null}`,
			checkFunc: func(t *testing.T, out map[string]any) {
				if out["needs_user"] != false {
					t.Errorf("expected needs_user: false, got %v", out["needs_user"])
				}
				as, ok := out["assumptions"].([]any)
				if !ok || len(as) != 0 {
					t.Errorf("expected assumptions: [], got %v", out["assumptions"])
				}
				ac, ok := out["acceptance"].([]any)
				if !ok || len(ac) != 1 || ac[0] != "go test ./..." {
					t.Errorf("expected acceptance: ['go test ./...'], got %v", out["acceptance"])
				}
				ng, ok := out["non_goals"].([]any)
				if !ok || len(ng) != 0 {
					t.Errorf("expected non_goals: [], got %v", out["non_goals"])
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, ok := For(tc.role)
			if !ok {
				t.Fatalf("For(%q) returned false", tc.role)
			}
			coercedBytes, err := CoerceSpec(spec, []byte(tc.raw))
			if err != nil {
				t.Fatalf("CoerceSpec returned unexpected error: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(coercedBytes, &m); err != nil {
				t.Fatalf("Coerced bytes not valid JSON: %v", err)
			}
			tc.checkFunc(t, m)
		})
	}
}

// TestChallengerCoerceNeverPanics ensures chaotic inputs do not panic.
func TestChallengerCoerceNeverPanics(t *testing.T) {
	roles := []string{RoleTeamworkSpec, RoleTeamworkPlan, RoleTeamworkWorker, RoleTeamworkReview}
	chaoticInputs := []string{
		`{}`,
		`null`,
		`"random string"`,
		`12345`,
		`true`,
		`false`,
		`[]`,
		`[1, 2, 3]`,
		`{"unexpected_key": [null, true, {"deep": 42}]}`,
		`{"approved": {"not_a": "bool"}, "score": [1, 2, 3]}`,
		`{"milestones": "not an array of objects"}`,
		`{"files_changed": 12345}`,
		`{"needs_user": [1, 2], "features": "string instead of array"}`,
	}

	for _, role := range roles {
		spec, ok := For(role)
		if !ok {
			t.Fatalf("For(%q) returned false", role)
		}
		for _, raw := range chaoticInputs {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("Role %q PANICKED on input %s: %v", role, raw, r)
					}
				}()
				_, _ = CoerceSpec(spec, []byte(raw))
			}()
		}
	}
}

// TestChallengerStrictSchemaValidation verifies OpenAI strict schema requirements.
func TestChallengerStrictSchemaValidation(t *testing.T) {
	roles := []string{RoleTeamworkSpec, RoleTeamworkPlan, RoleTeamworkWorker, RoleTeamworkReview}
	for _, role := range roles {
		spec, ok := For(role)
		if !ok {
			t.Fatalf("For(%q) returned false", role)
		}
		strict := StrictSchema(spec)

		// Must serialize to JSON
		raw, err := json.Marshal(strict)
		if err != nil {
			t.Fatalf("Role %q StrictSchema marshal error: %v", role, err)
		}
		if len(raw) == 0 {
			t.Fatalf("Role %q StrictSchema produced empty bytes", role)
		}

		// Verify additionalProperties: false at top-level
		if ap, ok := strict["additionalProperties"].(bool); !ok || ap != false {
			t.Errorf("Role %q top-level additionalProperties is not false: %v", role, strict["additionalProperties"])
		}

		// Verify every property in properties is listed in required
		props, _ := strict["properties"].(map[string]any)
		reqList, _ := strict["required"].([]any)
		reqMap := make(map[string]bool)
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				reqMap[s] = true
			}
		}
		for propName := range props {
			if !reqMap[propName] {
				t.Errorf("Role %q: property %q in properties not listed in required for strict schema", role, propName)
			}
		}
	}
}
