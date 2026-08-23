package loop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/UnicoLab/slmcode/pkg/session"
)

// bigToolTranscript builds a ReAct transcript of n assistant/tool pairs whose
// tool results are large, i.e. what a worker with MaxIter=16 actually produces
// when every observation is a whole file.
func bigToolTranscript(n, resultBytes int) []session.ReactMessage {
	msgs := []session.ReactMessage{
		{Role: "system", Content: "you are a worker"},
		{Role: "user", Content: "edit pkg/x/y.go"},
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs, session.ReactMessage{
			Role: "assistant", Content: "reading",
			ToolCalls: []session.ReactToolCall{{
				ID: id, Type: "function", Name: "ws_read",
				Arguments: fmt.Sprintf(`{"path":"pkg/x/file%d.go"}`, i),
			}},
		})
		msgs = append(msgs, session.ReactMessage{
			Role: "tool", Name: "ws_read", ToolCallID: id,
			Content: strings.Repeat("x", resultBytes),
		})
	}
	return msgs
}

// TestReactCompactUsesModelContextWindow pins the deprecated-window bug:
// WindowTokensFromKB(16) reports 4096 tokens, so a 32K model looked like it was
// at 80% capacity with ~3K tokens in hand and compacted on almost every resume.
func TestReactCompactUsesModelContextWindow(t *testing.T) {
	msgs := bigToolTranscript(6, 400) // ~5K chars ≈ 1.2K tokens

	cases := []struct {
		name         string
		contextLimit int
		maxKB        int
		wantCompact  bool
	}{
		{"32k model leaves plenty of headroom", 32768, 16, false},
		{"128k model", 131072, 16, false},
		{"unknown limit falls back to the KB budget", 0, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{
				ReactCompact: true, ReactCompactAtPercent: 80,
				ContextLimitTokens: tc.contextLimit, MaxContextKB: tc.maxKB,
			}
			got := r.maybeCompactReact(msgs, "worker", 3)
			compacted := len(got) != len(msgs) || got[0].Content != msgs[0].Content
			if compacted != tc.wantCompact {
				t.Fatalf("compacted=%v want=%v (window=%d tokens)",
					compacted, tc.wantCompact, compact.WindowTokensFor(tc.contextLimit, tc.maxKB))
			}
		})
	}
}

// TestReactCompactNeverOrphansAToolResult is the HTTP 400 regression: the kept
// tail used to be msgs[len-8:], which routinely began on a role:"tool" message.
// Every OpenAI-compatible server rejects that.
func TestReactCompactNeverOrphansAToolResult(t *testing.T) {
	// Enough volume to force compaction even against a real model window.
	msgs := bigToolTranscript(20, 4000)
	r := &Runner{ReactCompact: true, ReactCompactAtPercent: 50, ContextLimitTokens: 8192}
	got := r.maybeCompactReact(msgs, "worker", 12)
	if len(got) >= len(msgs) {
		t.Fatalf("transcript was not compacted: %d → %d", len(msgs), len(got))
	}
	assertToolPairsIntact(t, got)
}

// TestReactCompactRoundTripsToolFields asserts the tool linkage survives.
// sessionToChat used to flatten ToolCalls into a "[tools:ws_read]" text suffix,
// so ToolCallID / Name / ToolCalls could not be restored at all.
func TestReactCompactRoundTripsToolFields(t *testing.T) {
	in := bigToolTranscript(3, 32)
	out := chatToSession(sessionToChat(in))
	if len(out) != len(in) {
		t.Fatalf("round trip changed length: %d → %d", len(in), len(out))
	}
	for i := range in {
		a, b := in[i], out[i]
		if a.Role != b.Role || a.Content != b.Content || a.Name != b.Name || a.ToolCallID != b.ToolCallID {
			t.Fatalf("message %d lost fields:\n in=%+v\nout=%+v", i, a, b)
		}
		if len(a.ToolCalls) != len(b.ToolCalls) {
			t.Fatalf("message %d lost tool calls: %d → %d", i, len(a.ToolCalls), len(b.ToolCalls))
		}
		for k := range a.ToolCalls {
			if a.ToolCalls[k] != b.ToolCalls[k] {
				t.Fatalf("message %d tool call %d changed:\n in=%+v\nout=%+v",
					i, k, a.ToolCalls[k], b.ToolCalls[k])
			}
		}
	}
	if strings.Contains(fmt.Sprint(out), "[tools:") {
		t.Fatal("tool calls were flattened into text")
	}
}

// TestReactCompactPrefersDeterministicElision asserts old tool RESULTS are
// collapsed before any summarization is attempted: elision costs no inference,
// keeps every tool CALL visible, and is measured to beat summarization.
func TestReactCompactPrefersDeterministicElision(t *testing.T) {
	msgs := bigToolTranscript(12, 1200)
	r := &Runner{ReactCompact: true, ReactCompactAtPercent: 40, ContextLimitTokens: 8192}
	got := r.maybeCompactReact(msgs, "worker", 6)

	if len(got) != len(msgs) {
		t.Fatalf("elision must not drop messages: %d → %d", len(msgs), len(got))
	}
	elided := 0
	calls := 0
	for _, m := range got {
		if m.Content == compact.DefaultElidedPlaceholder {
			elided++
		}
		calls += len(m.ToolCalls)
	}
	if elided == 0 {
		t.Fatal("no tool result was elided")
	}
	if calls != 12 {
		t.Fatalf("elision dropped tool calls: %d of 12 left", calls)
	}
	assertToolPairsIntact(t, got)
}

// TestCompactLiveMessagesIsAvailable covers the live entry point: the loop
// package now exposes the compaction policy so the ReAct loop can apply it per
// iteration instead of only on resume.
func TestCompactLiveMessagesIsAvailable(t *testing.T) {
	live := fromSessionMessages(bigToolTranscript(20, 4000))
	r := &Runner{ReactCompact: true, ReactCompactAtPercent: 50, ContextLimitTokens: 8192}
	got := r.CompactLiveMessages("worker", 12, live)
	if len(got) >= len(live) {
		t.Fatalf("live transcript was not compacted: %d → %d", len(live), len(got))
	}
	// Disabled compaction is a pass-through.
	off := &Runner{ReactCompact: false}
	if n := len(off.CompactLiveMessages("worker", 12, live)); n != len(live) {
		t.Fatalf("compaction off must pass through: %d → %d", len(live), n)
	}
}

// assertToolPairsIntact fails when any tool result is unanswered by a preceding
// assistant tool call, or when the transcript begins on a tool message.
func assertToolPairsIntact(t *testing.T, msgs []session.ReactMessage) {
	t.Helper()
	if len(msgs) == 0 {
		return
	}
	if strings.EqualFold(msgs[0].Role, "tool") {
		t.Fatal("transcript begins on a tool result — every OpenAI-compatible server returns HTTP 400")
	}
	requested := map[string]bool{}
	for i, m := range msgs {
		for _, tc := range m.ToolCalls {
			requested[tc.ID] = true
		}
		if !strings.EqualFold(m.Role, "tool") {
			continue
		}
		if m.ToolCallID != "" && !requested[m.ToolCallID] {
			t.Fatalf("message %d is an orphaned tool result (tool_call_id=%s)", i, m.ToolCallID)
		}
	}
}
