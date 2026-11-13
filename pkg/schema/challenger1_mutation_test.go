package schema

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestChallenger1_ComprehensiveMutationKillRate implements an empirical mutation testing
// engine against all 4 Teamwork schemas, measuring the mutant kill rate across both
// structural validation (Validate) and constrained decoding grammar (AcceptsGBNF).
func TestChallenger1_ComprehensiveMutationKillRate(t *testing.T) {
	type mutantCase struct {
		id          string
		role        string
		description string
		defense     string // "validate" or "gbnf"
		payload     any
		rawJSON     string // if non-empty, use rawJSON directly
	}

	mutants := []mutantCase{
		// ── teamwork_review mutations (17 mutants) ──────────────────────────
		{
			id: "REV-MUT-01", role: RoleTeamworkReview, description: "score negative (-1)", defense: "validate",
			payload: map[string]any{"approved": true, "score": -1, "verdict": "approve", "summary": "ok"},
		},
		{
			id: "REV-MUT-02", role: RoleTeamworkReview, description: "score above 100 (101)", defense: "validate",
			payload: map[string]any{"approved": true, "score": 101, "verdict": "approve", "summary": "ok"},
		},
		{
			id: "REV-MUT-03", role: RoleTeamworkReview, description: "score type string (\"100\")", defense: "validate",
			payload: map[string]any{"approved": true, "score": "100", "verdict": "approve", "summary": "ok"},
		},
		{
			id: "REV-MUT-04", role: RoleTeamworkReview, description: "score type float (85.5)", defense: "validate",
			payload: map[string]any{"approved": true, "score": 85.5, "verdict": "approve", "summary": "ok"},
		},
		{
			id: "REV-MUT-05", role: RoleTeamworkReview, description: "approved type string (\"true\")", defense: "validate",
			payload: map[string]any{"approved": "true", "score": 90, "verdict": "approve", "summary": "ok"},
		},
		{
			id: "REV-MUT-06", role: RoleTeamworkReview, description: "verdict invalid enum (\"passed\")", defense: "validate",
			payload: map[string]any{"approved": true, "score": 90, "verdict": "passed", "summary": "ok"},
		},
		{
			id: "REV-MUT-07", role: RoleTeamworkReview, description: "verdict invalid enum (\"maybe\")", defense: "validate",
			payload: map[string]any{"approved": true, "score": 90, "verdict": "maybe", "summary": "ok"},
		},
		{
			id: "REV-MUT-08", role: RoleTeamworkReview, description: "verdict empty string (\"\")", defense: "validate",
			payload: map[string]any{"approved": true, "score": 90, "verdict": "", "summary": "ok"},
		},
		{
			id: "REV-MUT-09", role: RoleTeamworkReview, description: "missing required approved", defense: "validate",
			payload: map[string]any{"score": 90, "verdict": "approve", "summary": "ok"},
		},
		{
			id: "REV-MUT-10", role: RoleTeamworkReview, description: "missing required score", defense: "validate",
			payload: map[string]any{"approved": true, "verdict": "approve", "summary": "ok"},
		},
		{
			id: "REV-MUT-11", role: RoleTeamworkReview, description: "missing required verdict", defense: "validate",
			payload: map[string]any{"approved": true, "score": 90, "summary": "ok"},
		},
		{
			id: "REV-MUT-12", role: RoleTeamworkReview, description: "missing required summary", defense: "validate",
			payload: map[string]any{"approved": true, "score": 90, "verdict": "approve"},
		},
		{
			id: "REV-MUT-13", role: RoleTeamworkReview, description: "challenge_scenarios > 6 items", defense: "validate",
			payload: map[string]any{
				"approved": true, "score": 90, "verdict": "approve", "summary": "ok",
				"challenge_scenarios": []string{"1", "2", "3", "4", "5", "6", "7"},
			},
		},
		{
			id: "REV-MUT-14", role: RoleTeamworkReview, description: "edge_cases > 6 items", defense: "validate",
			payload: map[string]any{
				"approved": true, "score": 90, "verdict": "approve", "summary": "ok",
				"edge_cases": []string{"1", "2", "3", "4", "5", "6", "7"},
			},
		},
		{
			id: "REV-MUT-15", role: RoleTeamworkReview, description: "issues > 12 items", defense: "validate",
			payload: map[string]any{
				"approved": true, "score": 90, "verdict": "approve", "summary": "ok",
				"issues": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13"},
			},
		},
		{
			id: "REV-MUT-16", role: RoleTeamworkReview, description: "undeclared key rejected by GBNF", defense: "gbnf",
			rawJSON: `{"approved":true,"score":90,"verdict":"approve","summary":"ok","undeclared_key":true}`,
		},
		{
			id: "REV-MUT-17", role: RoleTeamworkReview, description: "corrupted JSON syntax", defense: "validate",
			rawJSON: `{"approved": true, "score": 90, "verdict": "approve", "summary": `,
		},

		// ── teamwork_worker mutations (10 mutants) ──────────────────────────
		{
			id: "WRK-MUT-01", role: RoleTeamworkWorker, description: "status invalid enum (\"in_progress\")", defense: "validate",
			payload: map[string]any{"status": "in_progress", "summary": "done", "files_changed": []string{"a.go"}},
		},
		{
			id: "WRK-MUT-02", role: RoleTeamworkWorker, description: "status invalid enum (\"pending\")", defense: "validate",
			payload: map[string]any{"status": "pending", "summary": "done", "files_changed": []string{"a.go"}},
		},
		{
			id: "WRK-MUT-03", role: RoleTeamworkWorker, description: "status type integer (1)", defense: "validate",
			payload: map[string]any{"status": 1, "summary": "done", "files_changed": []string{"a.go"}},
		},
		{
			id: "WRK-MUT-04", role: RoleTeamworkWorker, description: "missing required status", defense: "validate",
			payload: map[string]any{"summary": "done", "files_changed": []string{"a.go"}},
		},
		{
			id: "WRK-MUT-05", role: RoleTeamworkWorker, description: "missing required summary", defense: "validate",
			payload: map[string]any{"status": "done", "files_changed": []string{"a.go"}},
		},
		{
			id: "WRK-MUT-06", role: RoleTeamworkWorker, description: "missing required files_changed", defense: "validate",
			payload: map[string]any{"status": "done", "summary": "done"},
		},
		{
			id: "WRK-MUT-07", role: RoleTeamworkWorker, description: "files_changed type string instead of array", defense: "validate",
			payload: map[string]any{"status": "done", "summary": "done", "files_changed": "a.go"},
		},
		{
			id: "WRK-MUT-08", role: RoleTeamworkWorker, description: "files_changed > 20 items (21 items)", defense: "validate",
			payload: func() map[string]any {
				arr := make([]string, 21)
				for i := range arr {
					arr[i] = fmt.Sprintf("file_%d.go", i)
				}
				return map[string]any{"status": "done", "summary": "done", "files_changed": arr}
			}(),
		},
		{
			id: "WRK-MUT-09", role: RoleTeamworkWorker, description: "action_plan > 6 items", defense: "validate",
			payload: map[string]any{
				"status": "done", "summary": "done", "files_changed": []string{"a.go"},
				"action_plan": []string{"1", "2", "3", "4", "5", "6", "7"},
			},
		},
		{
			id: "WRK-MUT-10", role: RoleTeamworkWorker, description: "undeclared key rejected by GBNF", defense: "gbnf",
			rawJSON: `{"status":"done","summary":"done","files_changed":["a.go"],"injected_field":42}`,
		},

		// ── teamwork_plan mutations (13 mutants) ────────────────────────────
		{
			id: "PLN-MUT-01", role: RoleTeamworkPlan, description: "missing required strategy", defense: "validate",
			payload: map[string]any{
				"milestones":     []map[string]any{{"id": "M1", "title": "T", "acceptance": "A"}},
				"file_ownership": []map[string]any{{"owner": "w", "paths": []string{"pkg/**"}}},
			},
		},
		{
			id: "PLN-MUT-02", role: RoleTeamworkPlan, description: "missing required milestones", defense: "validate",
			payload: map[string]any{
				"strategy":       "divide",
				"file_ownership": []map[string]any{{"owner": "w", "paths": []string{"pkg/**"}}},
			},
		},
		{
			id: "PLN-MUT-03", role: RoleTeamworkPlan, description: "missing required file_ownership", defense: "validate",
			payload: map[string]any{
				"strategy":   "divide",
				"milestones": []map[string]any{{"id": "M1", "title": "T", "acceptance": "A"}},
			},
		},
		{
			id: "PLN-MUT-04", role: RoleTeamworkPlan, description: "milestones empty array (minItems 1)", defense: "validate",
			payload: map[string]any{
				"strategy":       "divide",
				"milestones":     []map[string]any{},
				"file_ownership": []map[string]any{{"owner": "w", "paths": []string{"pkg/**"}}},
			},
		},
		{
			id: "PLN-MUT-05", role: RoleTeamworkPlan, description: "milestones > 6 items (7 items)", defense: "validate",
			payload: func() map[string]any {
				ms := make([]map[string]any, 7)
				for i := range ms {
					ms[i] = map[string]any{"id": fmt.Sprintf("M%d", i), "title": "T", "acceptance": "A"}
				}
				return map[string]any{
					"strategy":       "divide",
					"milestones":     ms,
					"file_ownership": []map[string]any{{"owner": "w", "paths": []string{"pkg/**"}}},
				}
			}(),
		},
		{
			id: "PLN-MUT-06", role: RoleTeamworkPlan, description: "milestone missing required id", defense: "validate",
			payload: map[string]any{
				"strategy":       "divide",
				"milestones":     []map[string]any{{"title": "T", "acceptance": "A"}},
				"file_ownership": []map[string]any{{"owner": "w", "paths": []string{"pkg/**"}}},
			},
		},
		{
			id: "PLN-MUT-07", role: RoleTeamworkPlan, description: "milestone missing required title", defense: "validate",
			payload: map[string]any{
				"strategy":       "divide",
				"milestones":     []map[string]any{{"id": "M1", "acceptance": "A"}},
				"file_ownership": []map[string]any{{"owner": "w", "paths": []string{"pkg/**"}}},
			},
		},
		{
			id: "PLN-MUT-08", role: RoleTeamworkPlan, description: "milestone missing required acceptance", defense: "validate",
			payload: map[string]any{
				"strategy":       "divide",
				"milestones":     []map[string]any{{"id": "M1", "title": "T"}},
				"file_ownership": []map[string]any{{"owner": "w", "paths": []string{"pkg/**"}}},
			},
		},
		{
			id: "PLN-MUT-09", role: RoleTeamworkPlan, description: "file_ownership > 8 items", defense: "validate",
			payload: func() map[string]any {
				fo := make([]map[string]any, 9)
				for i := range fo {
					fo[i] = map[string]any{"owner": "w", "paths": []string{"pkg/**"}}
				}
				return map[string]any{
					"strategy":       "divide",
					"milestones":     []map[string]any{{"id": "M1", "title": "T", "acceptance": "A"}},
					"file_ownership": fo,
				}
			}(),
		},
		{
			id: "PLN-MUT-10", role: RoleTeamworkPlan, description: "file_ownership entry missing owner", defense: "validate",
			payload: map[string]any{
				"strategy":       "divide",
				"milestones":     []map[string]any{{"id": "M1", "title": "T", "acceptance": "A"}},
				"file_ownership": []map[string]any{{"paths": []string{"pkg/**"}}},
			},
		},
		{
			id: "PLN-MUT-11", role: RoleTeamworkPlan, description: "file_ownership entry missing paths", defense: "validate",
			payload: map[string]any{
				"strategy":       "divide",
				"milestones":     []map[string]any{{"id": "M1", "title": "T", "acceptance": "A"}},
				"file_ownership": []map[string]any{{"owner": "w"}},
			},
		},
		{
			id: "PLN-MUT-12", role: RoleTeamworkPlan, description: "contracts > 8 items", defense: "validate",
			payload: func() map[string]any {
				cts := make([]map[string]any, 9)
				for i := range cts {
					cts[i] = map[string]any{"interface_id": "I1", "provider": "p", "spec": "s"}
				}
				return map[string]any{
					"strategy":       "divide",
					"milestones":     []map[string]any{{"id": "M1", "title": "T", "acceptance": "A"}},
					"file_ownership": []map[string]any{{"owner": "w", "paths": []string{"pkg/**"}}},
					"contracts":      cts,
				}
			}(),
		},
		{
			id: "PLN-MUT-13", role: RoleTeamworkPlan, description: "undeclared key rejected by GBNF", defense: "gbnf",
			rawJSON: `{"strategy":"divide","milestones":[{"id":"M1","title":"T","acceptance":"A"}],"file_ownership":[{"owner":"w","paths":["pkg/**"]}],"rogue_data":"leak"}`,
		},

		// ── teamwork_spec mutations (15 mutants) ────────────────────────────
		{
			id: "SPC-MUT-01", role: RoleTeamworkSpec, description: "missing required needs_user", defense: "validate",
			payload: map[string]any{"assumptions": []string{"a"}, "acceptance": []string{"b"}},
		},
		{
			id: "SPC-MUT-02", role: RoleTeamworkSpec, description: "missing required assumptions", defense: "validate",
			payload: map[string]any{"needs_user": false, "acceptance": []string{"b"}},
		},
		{
			id: "SPC-MUT-03", role: RoleTeamworkSpec, description: "missing required acceptance", defense: "validate",
			payload: map[string]any{"needs_user": false, "assumptions": []string{"a"}},
		},
		{
			id: "SPC-MUT-04", role: RoleTeamworkSpec, description: "needs_user type string (\"false\")", defense: "validate",
			payload: map[string]any{"needs_user": "false", "assumptions": []string{"a"}, "acceptance": []string{"b"}},
		},
		{
			id: "SPC-MUT-05", role: RoleTeamworkSpec, description: "assumptions > 8 items", defense: "validate",
			payload: map[string]any{
				"needs_user": false, "acceptance": []string{"b"},
				"assumptions": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"},
			},
		},
		{
			id: "SPC-MUT-06", role: RoleTeamworkSpec, description: "acceptance > 8 items", defense: "validate",
			payload: map[string]any{
				"needs_user": false, "assumptions": []string{"a"},
				"acceptance": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"},
			},
		},
		{
			id: "SPC-MUT-07", role: RoleTeamworkSpec, description: "non_goals > 8 items", defense: "validate",
			payload: map[string]any{
				"needs_user": false, "assumptions": []string{"a"}, "acceptance": []string{"b"},
				"non_goals": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"},
			},
		},
		{
			id: "SPC-MUT-08", role: RoleTeamworkSpec, description: "questions > 3 items", defense: "validate",
			payload: func() map[string]any {
				qs := make([]map[string]any, 4)
				for i := range qs {
					qs[i] = map[string]any{
						"id": fmt.Sprintf("q%d", i), "header": "H", "question": "Q",
						"options": []map[string]any{{"label": "1", "description": "d"}, {"label": "2", "description": "d"}},
					}
				}
				return map[string]any{"needs_user": true, "assumptions": []string{"a"}, "acceptance": []string{"b"}, "questions": qs}
			}(),
		},
		{
			id: "SPC-MUT-09", role: RoleTeamworkSpec, description: "question with only 1 option (minItems 2)", defense: "validate",
			payload: map[string]any{
				"needs_user": true, "assumptions": []string{"a"}, "acceptance": []string{"b"},
				"questions": []map[string]any{
					{
						"id": "q1", "header": "H", "question": "Q",
						"options": []map[string]any{{"label": "1", "description": "d"}},
					},
				},
			},
		},
		{
			id: "SPC-MUT-10", role: RoleTeamworkSpec, description: "question with 5 options (maxItems 4)", defense: "validate",
			payload: map[string]any{
				"needs_user": true, "assumptions": []string{"a"}, "acceptance": []string{"b"},
				"questions": []map[string]any{
					{
						"id": "q1", "header": "H", "question": "Q",
						"options": []map[string]any{
							{"label": "1", "description": "d"}, {"label": "2", "description": "d"},
							{"label": "3", "description": "d"}, {"label": "4", "description": "d"},
							{"label": "5", "description": "d"},
						},
					},
				},
			},
		},
		{
			id: "SPC-MUT-11", role: RoleTeamworkSpec, description: "question option missing label", defense: "validate",
			payload: map[string]any{
				"needs_user": true, "assumptions": []string{"a"}, "acceptance": []string{"b"},
				"questions": []map[string]any{
					{
						"id": "q1", "header": "H", "question": "Q",
						"options": []map[string]any{{"description": "d"}, {"label": "2", "description": "d"}},
					},
				},
			},
		},
		{
			id: "SPC-MUT-12", role: RoleTeamworkSpec, description: "question option missing description", defense: "validate",
			payload: map[string]any{
				"needs_user": true, "assumptions": []string{"a"}, "acceptance": []string{"b"},
				"questions": []map[string]any{
					{
						"id": "q1", "header": "H", "question": "Q",
						"options": []map[string]any{{"label": "1"}, {"label": "2", "description": "d"}},
					},
				},
			},
		},
		{
			id: "SPC-MUT-13", role: RoleTeamworkSpec, description: "question invalid facet (\"security\")", defense: "validate",
			payload: map[string]any{
				"needs_user": true, "assumptions": []string{"a"}, "acceptance": []string{"b"},
				"questions": []map[string]any{
					{
						"id": "q1", "header": "H", "question": "Q", "facet": "security",
						"options": []map[string]any{{"label": "1", "description": "d"}, {"label": "2", "description": "d"}},
					},
				},
			},
		},
		{
			id: "SPC-MUT-14", role: RoleTeamworkSpec, description: "feature invalid priority (\"urgent\")", defense: "validate",
			payload: map[string]any{
				"needs_user": false, "assumptions": []string{"a"}, "acceptance": []string{"b"},
				"features": []map[string]any{
					{"id": "F1", "title": "T", "acceptance": "A", "priority": "urgent"},
				},
			},
		},
		{
			id: "SPC-MUT-15", role: RoleTeamworkSpec, description: "features > 8 items", defense: "validate",
			payload: func() map[string]any {
				fts := make([]map[string]any, 9)
				for i := range fts {
					fts[i] = map[string]any{"id": fmt.Sprintf("F%d", i), "title": "T", "acceptance": "A", "priority": "must"}
				}
				return map[string]any{"needs_user": false, "assumptions": []string{"a"}, "acceptance": []string{"b"}, "features": fts}
			}(),
		},
	}

	totalMutants := len(mutants)
	killedMutants := 0

	for _, m := range mutants {
		t.Run(fmt.Sprintf("%s_%s", m.id, m.description), func(t *testing.T) {
			var rawStr string
			var rawBytes []byte
			if m.rawJSON != "" {
				rawStr = m.rawJSON
				rawBytes = []byte(m.rawJSON)
			} else {
				var err error
				rawBytes, err = json.Marshal(m.payload)
				if err != nil {
					t.Fatalf("Failed to marshal mutant payload: %v", err)
				}
				rawStr = string(rawBytes)
			}

			if m.defense == "gbnf" {
				// Test against GBNF grammar
				spec, ok := For(m.role)
				if !ok {
					t.Fatalf("Role %q not found", m.role)
				}
				grammar := GBNF(spec)
				accepted, err := AcceptsGBNF(grammar, rawStr)
				if err != nil {
					// Parse error = killed
					killedMutants++
					return
				}
				if accepted {
					t.Fatalf("MUTANT SURVIVED: %s (%s) was accepted by GBNF grammar for %q!", m.id, m.description, m.role)
				}
				killedMutants++
			} else {
				// Test against Validate
				err := Validate(m.role, rawBytes)
				if err == nil {
					t.Fatalf("MUTANT SURVIVED: %s (%s) was accepted by Validate(%q)!", m.id, m.description, m.role)
				}
				killedMutants++
			}
		})
	}

	killRate := (float64(killedMutants) / float64(totalMutants)) * 100.0
	t.Logf("Mutation Test Results: %d/%d mutants killed (%.2f%% kill rate)", killedMutants, totalMutants, killRate)

	if killRate < 100.0 {
		t.Fatalf("Mutation kill rate %.2f%% is below required 100.0%%", killRate)
	}
}
