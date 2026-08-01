package quality

import (
	"strings"
	"testing"
)

func TestParseTextToolCallsFenced(t *testing.T) {
	text := "I'll edit now:\n```tool\n{\"name\": \"ws_edit\", \"input\": {\"path\": \"a.py\", \"old_str\": \"x\", \"new_str\": \"y\"}}\n```\n"
	calls := ParseTextToolCalls(text)
	if len(calls) != 1 || calls[0].Name != "ws_edit" {
		t.Fatalf("%+v", calls)
	}
	if calls[0].Input["path"] != "a.py" {
		t.Fatalf("input=%v", calls[0].Input)
	}
	nudge := TextToolNudge(calls)
	if !strings.Contains(nudge, "ws_edit") || !strings.Contains(nudge, "path=") {
		t.Fatalf("nudge=%q", nudge)
	}
}

func TestParseTextToolCallsTagAndBare(t *testing.T) {
	tag := `<tool_call>{"name":"ws_shell","parameters":{"command":"pytest"}}</tool_call>`
	calls := ParseTextToolCalls(tag)
	if len(calls) != 1 || calls[0].Name != "ws_shell" {
		t.Fatalf("tag: %+v", calls)
	}
	bare := `{"name": "ws_read", "path": "main.go"}`
	calls = ParseTextToolCalls(bare)
	if len(calls) != 1 || calls[0].Input["path"] != "main.go" {
		t.Fatalf("bare: %+v", calls)
	}
}

func TestParseLiquidToolCalls(t *testing.T) {
	text := `<|tool_call_start|>[Read(path='/a.c'), Grep(pattern='x', path='.')]<|tool_call_end|>`
	calls := ParseTextToolCalls(text)
	if len(calls) < 2 {
		t.Fatalf("%+v", calls)
	}
	if calls[0].Name != "ws_read" {
		t.Fatalf("normalized name=%s", calls[0].Name)
	}
	nudge := TextToolNudge(calls)
	if !strings.Contains(nudge, "Liquid") && !strings.Contains(nudge, "native") {
		t.Fatalf("nudge=%q", nudge)
	}
}
