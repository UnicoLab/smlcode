package plan

import (
	"testing"
)

func TestParseScopeInterviewWithFeatures(t *testing.T) {
	raw := `{
		"needs_user": false,
		"assumptions": ["Assumption 1"],
		"acceptance": ["Acceptance 1"],
		"language": "go",
		"entrypoint": "main.go",
		"features": [
			{
				"id": "F1",
				"title": "Core Auth",
				"description": "JWT authentication",
				"acceptance": "go test ./pkg/auth/...",
				"priority": "must"
			},
			{
				"id": "F2",
				"title": "OAuth2 Support",
				"description": "Google login",
				"acceptance": "go test ./pkg/oauth/...",
				"priority": "should"
			}
		],
		"non_goals": ["No SAML"],
		"questions": []
	}`

	interview := ParseScopeInterview(raw)
	if len(interview.Features) != 2 {
		t.Fatalf("expected 2 features, got %d", len(interview.Features))
	}
	if interview.Features[0].ID != "F1" || interview.Features[0].Priority != "must" {
		t.Errorf("unexpected feature 0: %+v", interview.Features[0])
	}
	if len(interview.PRD.Features) != 2 {
		t.Fatalf("expected 2 PRD features, got %d", len(interview.PRD.Features))
	}

	md := FormatPRDMarkdown(interview.PRD, interview.Assumptions)
	if !stringsContains(md, "### Features") {
		t.Errorf("expected '### Features' in markdown, got:\n%s", md)
	}
	if !stringsContains(md, "**[F1] Core Auth** (must)") {
		t.Errorf("expected feature F1 formatted in markdown, got:\n%s", md)
	}

	// Test EnsureTaskPRDs seeds criteria from features
	tasks := []Task{
		{ID: "T1", Title: "Build auth", Role: RoleWorker, Files: []string{"pkg/auth/auth.go"}},
	}
	tasks = EnsureTaskPRDs(tasks, interview.PRD, "implement auth")
	if len(tasks[0].Criteria) == 0 {
		t.Fatalf("expected criteria seeded into task, got 0")
	}
	if tasks[0].Criteria[0].ID != "F1" {
		t.Errorf("expected criterion ID F1, got %q", tasks[0].Criteria[0].ID)
	}
}

func TestTeamworkPlanParseAndContracts(t *testing.T) {
	raw := `{
		"strategy": "Modular architecture",
		"milestones": [
			{
				"id": "M1",
				"title": "Data Schema",
				"description": "Define database models",
				"acceptance": "go test ./pkg/models/...",
				"depends_on": []
			}
		],
		"file_ownership": [
			{
				"owner": "worker-1",
				"paths": ["pkg/models/models.go"]
			}
		],
		"contracts": [
			{
				"interface_id": "IModelStore",
				"provider": "pkg/models",
				"consumers": ["pkg/service"],
				"spec": "type ModelStore interface { Get(id string) (*Model, error) }"
			}
		],
		"risks": ["Data migration complexity"]
	}`

	tp, ok := ParseTeamworkPlanJSON(raw)
	if !ok {
		t.Fatal("expected ParseTeamworkPlanJSON to succeed")
	}
	if tp.Strategy != "Modular architecture" {
		t.Errorf("expected strategy 'Modular architecture', got %q", tp.Strategy)
	}
	if len(tp.Milestones) != 1 || tp.Milestones[0].ID != "M1" {
		t.Errorf("unexpected milestones: %+v", tp.Milestones)
	}
	if len(tp.Contracts) != 1 || tp.Contracts[0].InterfaceID != "IModelStore" {
		t.Errorf("unexpected contracts: %+v", tp.Contracts)
	}

	contractMD := RenderContractsMarkdown(tp.Contracts)
	if !stringsContains(contractMD, "# Interface Contracts") {
		t.Errorf("expected header in contract markdown, got:\n%s", contractMD)
	}
	if !stringsContains(contractMD, "## IModelStore") {
		t.Errorf("expected IModelStore in contract markdown, got:\n%s", contractMD)
	}
	if !stringsContains(contractMD, "type ModelStore interface") {
		t.Errorf("expected spec in contract markdown, got:\n%s", contractMD)
	}

	// Test ParsePlanJSON parses TeamworkPlan seamlessly
	plan, err := ParsePlanJSON(raw)
	if err != nil {
		t.Fatalf("ParsePlanJSON failed: %v", err)
	}
	if plan.Summary != "Modular architecture" {
		t.Errorf("expected plan summary 'Modular architecture', got %q", plan.Summary)
	}
	if len(plan.Steps) != 1 {
		t.Errorf("expected 1 step in plan, got %d", len(plan.Steps))
	}
}

func TestParseReviewJSONWithVerdict(t *testing.T) {
	// 1. Explicit veto overrides approved:true
	rawVeto := `{"approved": true, "score": 90, "verdict": "veto", "summary": "critical flaw", "issues": ["security vuln"]}`
	res := ParseReviewJSON(rawVeto)
	if res.Approved {
		t.Errorf("expected Approved=false on veto, got true")
	}
	if res.Verdict != "veto" {
		t.Errorf("expected verdict 'veto', got %q", res.Verdict)
	}

	// 2. Explicit reject overrides approved:true
	rawReject := `{"approved": true, "score": 85, "verdict": "reject", "summary": "incomplete", "issues": ["missing tests"]}`
	res2 := ParseReviewJSON(rawReject)
	if res2.Approved {
		t.Errorf("expected Approved=false on reject, got true")
	}

	// 3. Normal approve
	rawApprove := `{"approved": true, "score": 95, "verdict": "approve", "summary": "looks great", "issues": []}`
	res3 := ParseReviewJSON(rawApprove)
	if !res3.Approved {
		t.Errorf("expected Approved=true, got false")
	}
	if res3.Verdict != "approve" {
		t.Errorf("expected verdict 'approve', got %q", res3.Verdict)
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && containsSubstr(s, substr)))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
