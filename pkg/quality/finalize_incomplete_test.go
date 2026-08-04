package quality

import (
	"strings"
	"testing"
)

func TestIncompleteFinalizeReason(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "empty_response"},
		{"   ", "empty_response"},
		{`{"status":"blocked","summary":"model ended on a tool call","notes":"retry with clearer finish instruction"}`, "ended_on_tool_call"},
		{`{"status":"blocked","summary":"empty finalize","notes":"retry with clearer finish instruction"}`, "ended_on_tool_call"},
		{"<tool_call>ws_edit</tool_call>", "ended_on_tool_call"},
		{`{"status":"done","summary":"ok","files_changed":["a.go"]}`, ""},
		{`{"status":"blocked","summary":"missing API key","notes":"need human"}`, ""},
		{"hello\n\n## Disk evidence\n- modified: a.go", ""},
	}
	for _, tc := range cases {
		got := IncompleteFinalizeReason(tc.in)
		if got != tc.want {
			t.Fatalf("in=%q got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestAssessResponseCatchesToolEndBlock(t *testing.T) {
	a := AssessResponse(`{"status":"blocked","summary":"model ended on a tool call","notes":"retry with clearer finish instruction"}`, nil, nil, nil)
	if a.OK || a.Reason != "ended_on_tool_call" {
		t.Fatalf("%+v", a)
	}
}

func TestFinishSteerMessageEvidence(t *testing.T) {
	msg := FinishSteerMessage("ended_on_tool_call", true)
	if !strings.Contains(msg, "STRICT JSON") || !strings.Contains(msg, "do NOT start new tool") {
		t.Fatalf("unexpected steer: %s", msg)
	}
}

func TestProvisionalDoneFromEvidence(t *testing.T) {
	out := ProvisionalDoneFromEvidence([]string{"main.py"}, "ended_on_tool_call")
	if IncompleteFinalizeReason(out) != "" {
		t.Fatalf("provisional should be complete: %s", out)
	}
	if !strings.Contains(out, `"status":"done"`) || !strings.Contains(out, "main.py") {
		t.Fatalf("unexpected provisional: %s", out)
	}
}

func TestPhraseForUserEndedOnTool(t *testing.T) {
	if got := PhraseForUser("ended_on_tool_call"); !strings.Contains(got, "tool call") {
		t.Fatalf("%q", got)
	}
}
