package quality

import "testing"

func TestAssessEmpty(t *testing.T) {
	a := AssessResponse("", nil, nil, nil)
	if a.OK || a.Reason != "empty_response" {
		t.Fatalf("%+v", a)
	}
}

func TestAssessUnknownTool(t *testing.T) {
	known := map[string]bool{"ws_edit": true}
	a := AssessResponse("hi", []ToolCall{{Name: "DeleteEverything"}}, nil, known)
	if a.OK || a.Reason != "unknown_tool:DeleteEverything" {
		t.Fatalf("%+v", a)
	}
}

func TestAssessRepeatedLoop(t *testing.T) {
	args := map[string]interface{}{"path": "a.go"}
	cur := []ToolCall{{Name: "ws_read", Input: args}}
	prev := []ToolCall{{Name: "ws_read", Input: args}}
	a := AssessResponse("x", cur, prev, nil)
	if a.OK || a.Reason != "repeated_tool_call" {
		t.Fatalf("%+v", a)
	}
}

func TestAssessRepeatAfterEditIsOK(t *testing.T) {
	// Re-running the same build after an edit is progress (issue #81).
	//
	// ORDER MATTERS, and this test used to assert the opposite: the edit has to
	// land AFTER the earlier identical build for the re-run to be able to
	// return something new. With the edit first, the sequence is
	// edit → build → build, whose second build cannot differ from the first.
	build := map[string]interface{}{"command": "go test ./..."}
	cur := []ToolCall{{Name: "ws_shell", Input: build}}
	prev := []ToolCall{
		{Name: "ws_shell", Input: build},
		{Name: "ws_edit", Input: map[string]interface{}{"path": "a.go"}},
	}
	a := AssessResponse("retry", cur, prev, nil)
	if !a.OK {
		t.Fatalf("expected ok after state change, got %+v", a)
	}
}

func TestCorrectionMessage(t *testing.T) {
	msg := CorrectionMessage("repeated_tool_call")
	if msg == "" {
		t.Fatal("empty")
	}
}

// TestAssessRepeatOnlyExemptAfterAStateChange pins the ordering rule: an edit
// that happened BEFORE the identical earlier call proves nothing, and one
// state-changing call must not buy unlimited repeats for the rest of the
// history window.
func TestAssessRepeatOnlyExemptAfterAStateChange(t *testing.T) {
	read := map[string]interface{}{"path": "a.go"}
	edit := map[string]interface{}{"path": "a.go", "old_str": "x"}
	shell := map[string]interface{}{"command": "go test ./..."}

	cases := []struct {
		name     string
		current  []ToolCall
		previous []ToolCall
		wantOK   bool
	}{
		{
			name:     "no earlier identical call",
			current:  []ToolCall{{Name: "ws_read", Input: read}},
			previous: []ToolCall{{Name: "ws_grep", Input: map[string]interface{}{"pattern": "x"}}},
			wantOK:   true,
		},
		{
			name:     "verbatim repeat with nothing in between",
			current:  []ToolCall{{Name: "ws_read", Input: read}},
			previous: []ToolCall{{Name: "ws_read", Input: read}},
			wantOK:   false,
		},
		{
			name:    "state change AFTER the identical call unlocks it",
			current: []ToolCall{{Name: "ws_shell", Input: shell}},
			previous: []ToolCall{
				{Name: "ws_shell", Input: shell},
				{Name: "ws_edit", Input: edit},
			},
			wantOK: true,
		},
		{
			name:    "state change BEFORE the identical call proves nothing",
			current: []ToolCall{{Name: "ws_shell", Input: shell}},
			previous: []ToolCall{
				{Name: "ws_edit", Input: edit},
				{Name: "ws_shell", Input: shell},
			},
			wantOK: false,
		},
		{
			name:    "one old edit does not exempt an endless read loop",
			current: []ToolCall{{Name: "ws_read", Input: read}},
			previous: []ToolCall{
				{Name: "ws_edit", Input: edit},
				{Name: "ws_read", Input: read},
				{Name: "ws_read", Input: read},
				{Name: "ws_read", Input: read},
			},
			wantOK: false,
		},
		{
			name:    "the most recent identical call is the one that counts",
			current: []ToolCall{{Name: "ws_read", Input: read}},
			previous: []ToolCall{
				{Name: "ws_read", Input: read},
				{Name: "ws_edit", Input: edit},
				{Name: "ws_read", Input: read},
			},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := AssessResponse("text", tc.current, tc.previous, nil)
			if a.OK != tc.wantOK {
				t.Fatalf("AssessResponse OK = %v (reason %q), want %v", a.OK, a.Reason, tc.wantOK)
			}
			if !tc.wantOK && a.Reason != "repeated_tool_call" {
				t.Fatalf("reason = %q, want repeated_tool_call", a.Reason)
			}
		})
	}
}
