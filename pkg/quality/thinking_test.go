package quality

import (
	"strings"
	"testing"
)

func TestThinkingBudgetExceeded(t *testing.T) {
	long := strings.Repeat("I should consider another approach. ", 400)
	if !ThinkingBudgetExceeded(long, 200) {
		t.Fatal("expected breach on long prose")
	}
	done := long + "\n" + `{"status":"done","summary":"ok","files_changed":["a.go"]}`
	if ThinkingBudgetExceeded(done, 200) {
		t.Fatal("done status should not breach")
	}
	if ThinkingBudgetExceeded("short", 4096) {
		t.Fatal("short ok")
	}
}

func TestMalformedArgsAssessment(t *testing.T) {
	a := AssessResponse("", []ToolCall{{
		Name: "ws_read", Input: map[string]interface{}{"_raw": "garbage"},
	}}, nil, map[string]bool{"ws_read": true})
	if a.OK || !strings.HasPrefix(a.Reason, "malformed_args:") {
		t.Fatalf("%+v", a)
	}
	msg := CorrectionMessage(a.Reason)
	if !strings.Contains(msg, "malformed") {
		t.Fatal(msg)
	}
}
