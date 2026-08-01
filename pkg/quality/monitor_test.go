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
	build := map[string]interface{}{"command": "go test ./..."}
	cur := []ToolCall{{Name: "ws_shell", Input: build}}
	prev := []ToolCall{
		{Name: "ws_edit", Input: map[string]interface{}{"path": "a.go"}},
		{Name: "ws_shell", Input: build},
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
