package plan

import "testing"

// A reviewer whose valid JSON puts the verdict inside a nested object, or wraps
// it, must still be readable as an approval.
func TestReviewJSONNestedAndWrappedApprovals(t *testing.T) {
	cases := map[string]bool{
		`{"approved": true, "score": 90, "issues": []}`:               true,
		`{"review": {"approved": true, "score": 90}}`:                 true,
		"```json\n{\"approved\": true}\n```":                          true,
		`{"result":{"verdict":{"approved":true}},"score":9}`:          true,
		`Looks fine to me. {"approved":true,"summary":"ok"}`:          true,
		`{"path":"main.go"}` + "\n" + `{"approved":true}`:             true,
		`{"approved":"yes"}`:                                          true,
		`{"approved": false, "issues": ["stub code in parser.go`:      false,
		`I would approve this once the tests pass.`:                   false,
		`{"approved":true,"issues":["also: approved: false is bad"]}`: true,
		`{"approved": true,`:                                          true,
	}
	for raw, want := range cases {
		got := ParseReviewJSON(raw).Approved
		if got != want {
			t.Errorf("ParseReviewJSON(%q).Approved = %v, want %v", raw, got, want)
		}
	}
}

// A tester that ran its command through a hook / an executor frame rather than
// literally calling ws_shell must still count as having evidence.
func TestTesterEvidenceShapes(t *testing.T) {
	cases := map[string]bool{
		// A bare Observation frame proves a tool call happened, not that a
		// SHELL ran — and a tester with no tool calls can type it verbatim.
		"Observation: all tests pass":                        false,
		"Observation: ws_shell `pytest -q` exit status 0":    true,
		"ws_shell -> ok":                                     true,
		"## Deterministic smoke\nPASSED":                     true,
		"exit status 0":                                      true,
		"exit code: 1":                                       true,
		"exit_code=0":                                        true,
		"Running the test suite... OK, all tests pass":       false,
		"I ran pytest and everything passed":                 false,
		"hook: pretest ran `go test ./...` -> exit status 0": true,
	}
	for raw, want := range cases {
		if got := TesterHasShellEvidence(raw); got != want {
			t.Errorf("TesterHasShellEvidence(%q) = %v, want %v", raw, got, want)
		}
	}
}
