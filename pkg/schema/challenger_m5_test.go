package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEmpiricalTeamworkSchemasComprehensive stress tests all four Teamwork schemas.
func TestEmpiricalTeamworkSchemasComprehensive(t *testing.T) {
	roles := []string{
		RoleTeamworkSpec,
		RoleTeamworkPlan,
		RoleTeamworkWorker,
		RoleTeamworkReview,
	}

	// 1. All roles exist and are registered
	for _, r := range roles {
		spec, ok := For(r)
		if !ok {
			t.Fatalf("Role %q not found in schema registry", r)
		}
		if spec.Schema == nil {
			t.Fatalf("Role %q schema map is nil", r)
		}
		grammar := GBNF(spec)
		if strings.TrimSpace(grammar) == "" {
			t.Fatalf("Role %q GBNF grammar is empty", r)
		}
	}

	// 2. teamwork_review score bounds and verdict enums
	t.Run("teamwork_review score and verdict boundaries", func(t *testing.T) {
		validBase := map[string]any{
			"approved": true,
			"score":    100,
			"verdict":  "approve",
			"summary":  "All tests passing",
		}

		// Valid boundary scores: 0 and 100
		for _, score := range []int{0, 50, 100} {
			validBase["score"] = score
			raw, _ := json.Marshal(validBase)
			if err := Validate(RoleTeamworkReview, raw); err != nil {
				t.Errorf("Valid score %d rejected: %v", score, err)
			}
		}

		// Invalid scores: -1, 101, float
		for _, badScore := range []any{-1, 101, 85.5} {
			validBase["score"] = badScore
			raw, _ := json.Marshal(validBase)
			if err := Validate(RoleTeamworkReview, raw); err == nil {
				t.Errorf("Invalid score %v was accepted by Validate", badScore)
			}
		}

		// Valid verdicts: approve, reject, veto
		for _, v := range []string{"approve", "reject", "veto"} {
			validBase["score"] = 50
			validBase["verdict"] = v
			raw, _ := json.Marshal(validBase)
			if err := Validate(RoleTeamworkReview, raw); err != nil {
				t.Errorf("Valid verdict %q rejected: %v", v, err)
			}
		}

		// Invalid verdicts
		for _, v := range []string{"passed", "failed", "accepted", "APPROVED", "vetoed", ""} {
			validBase["verdict"] = v
			raw, _ := json.Marshal(validBase)
			if err := Validate(RoleTeamworkReview, raw); err == nil {
				t.Errorf("Invalid verdict %q was accepted by Validate", v)
			}
		}
	})

	// 3. teamwork_worker status enums and array bounds
	t.Run("teamwork_worker status and array limits", func(t *testing.T) {
		validWorker := map[string]any{
			"status":        "done",
			"summary":       "Task implemented",
			"files_changed": []string{"pkg/auth/token.go"},
		}

		// Valid status: done, blocked
		for _, st := range []string{"done", "blocked"} {
			validWorker["status"] = st
			raw, _ := json.Marshal(validWorker)
			if err := Validate(RoleTeamworkWorker, raw); err != nil {
				t.Errorf("Valid status %q rejected: %v", st, err)
			}
		}

		// Invalid status
		for _, st := range []string{"in_progress", "complete", "pending", "success", "failed", ""} {
			validWorker["status"] = st
			raw, _ := json.Marshal(validWorker)
			if err := Validate(RoleTeamworkWorker, raw); err == nil {
				t.Errorf("Invalid status %q was accepted by Validate", st)
			}
		}

		// Array limit: files_changed maxItems 20
		files20 := make([]string, 20)
		for i := range files20 {
			files20[i] = "file.go"
		}
		validWorker["status"] = "done"
		validWorker["files_changed"] = files20
		raw, _ := json.Marshal(validWorker)
		if err := Validate(RoleTeamworkWorker, raw); err != nil {
			t.Errorf("files_changed length 20 rejected: %v", err)
		}

		files21 := append(files20, "extra.go")
		validWorker["files_changed"] = files21
		raw, _ = json.Marshal(validWorker)
		if err := Validate(RoleTeamworkWorker, raw); err == nil {
			t.Errorf("files_changed length 21 (>20) was accepted by Validate")
		}
	})

	// 4. teamwork_plan milestones minItems 1, maxItems 6
	t.Run("teamwork_plan milestones bounds", func(t *testing.T) {
		validPlan := map[string]any{
			"strategy": "Decomposition approach",
			"milestones": []map[string]any{
				{"id": "M1", "title": "Milestone 1", "acceptance": "go test"},
			},
			"file_ownership": []map[string]any{
				{"owner": "worker", "paths": []string{"pkg/**"}},
			},
		}

		// Valid plan
		raw, _ := json.Marshal(validPlan)
		if err := Validate(RoleTeamworkPlan, raw); err != nil {
			t.Fatalf("Valid plan rejected: %v", err)
		}

		// minItems 1: empty milestones rejected
		validPlan["milestones"] = []map[string]any{}
		raw, _ = json.Marshal(validPlan)
		if err := Validate(RoleTeamworkPlan, raw); err == nil {
			t.Errorf("Empty milestones was accepted by Validate")
		}

		// maxItems 6: 7 milestones rejected
		ms7 := make([]map[string]any, 7)
		for i := range ms7 {
			ms7[i] = map[string]any{"id": "M", "title": "T", "acceptance": "A"}
		}
		validPlan["milestones"] = ms7
		raw, _ = json.Marshal(validPlan)
		if err := Validate(RoleTeamworkPlan, raw); err == nil {
			t.Errorf("7 milestones (>6) was accepted by Validate")
		}
	})

	// 5. teamwork_spec questions and features bounds
	t.Run("teamwork_spec questions and options bounds", func(t *testing.T) {
		validSpec := map[string]any{
			"needs_user":  false,
			"assumptions": []string{"assumption 1"},
			"acceptance":  []string{"acceptance 1"},
		}

		raw, _ := json.Marshal(validSpec)
		if err := Validate(RoleTeamworkSpec, raw); err != nil {
			t.Fatalf("Valid spec rejected: %v", err)
		}

		// Question options minItems 2, maxItems 4
		validSpec["questions"] = []map[string]any{
			{
				"id":       "q1",
				"header":   "Scope",
				"question": "Which db?",
				"options": []map[string]any{
					{"label": "opt1", "description": "desc1"},
				}, // only 1 option -> must reject
			},
		}
		raw, _ = json.Marshal(validSpec)
		if err := Validate(RoleTeamworkSpec, raw); err == nil {
			t.Errorf("Question with only 1 option (<2) was accepted by Validate")
		}

		// 5 options -> must reject
		opts5 := make([]map[string]any, 5)
		for i := range opts5 {
			opts5[i] = map[string]any{"label": "opt", "description": "desc"}
		}
		validSpec["questions"].([]map[string]any)[0]["options"] = opts5
		raw, _ = json.Marshal(validSpec)
		if err := Validate(RoleTeamworkSpec, raw); err == nil {
			t.Errorf("Question with 5 options (>4) was accepted by Validate")
		}
	})
}
