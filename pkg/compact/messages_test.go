package compact

import (
	"strings"
	"testing"
)

func asst(content string, calls ...ToolCallRef) ChatMsg {
	return ChatMsg{Role: RoleAssistant, Content: content, ToolCalls: calls}
}

func tool(id, name, content string) ChatMsg {
	return ChatMsg{Role: RoleTool, Content: content, ToolCallID: id, Name: name}
}

func TestSafeKeepStartNeverOrphansToolResults(t *testing.T) {
	// user, assistant(call a), tool(a), assistant(call b), tool(b), assistant, user
	msgs := []ChatMsg{
		{Role: RoleUser, Content: "do it"},
		asst("", ToolCallRef{ID: "a", Name: "ws_read"}),
		tool("a", "ws_read", "file body"),
		asst("", ToolCallRef{ID: "b", Name: "ws_edit"}),
		tool("b", "ws_edit", "ok"),
		asst("done"),
		{Role: RoleUser, Content: "next"},
	}
	tests := []struct {
		name     string
		keepLast int
	}{
		{"keep 1", 1},
		{"keep 2", 2},
		{"keep 3", 3},
		{"keep 4", 4},
		{"keep 5", 5},
		{"keep 6", 6},
		{"keep all", 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start := SafeKeepStart(msgs, tc.keepLast)
			if start < 0 || start > len(msgs) {
				t.Fatalf("start out of range: %d", start)
			}
			if start < len(msgs) && msgs[start].Role == RoleTool {
				t.Fatalf("window starts on a tool message at %d", start)
			}
			// Every tool message in the window must have its call inside too.
			seen := map[string]bool{}
			for _, m := range msgs[start:] {
				for _, tc2 := range m.ToolCalls {
					seen[tc2.ID] = true
				}
				if m.Role == RoleTool && !seen[m.ToolCallID] {
					t.Fatalf("orphan tool result %q from start=%d", m.ToolCallID, start)
				}
			}
		})
	}
}

func TestSafeKeepStartExtendsBackwards(t *testing.T) {
	msgs := []ChatMsg{
		{Role: RoleUser, Content: "u"},
		asst("", ToolCallRef{ID: "a", Name: "ws_read"}),
		tool("a", "ws_read", "body"),
	}
	// keepLast=1 would start on the tool result; must extend back to index 1.
	if got := SafeKeepStart(msgs, 1); got != 1 {
		t.Fatalf("start=%d want 1", got)
	}
}

func TestSafeKeepStartFuncWithForeignType(t *testing.T) {
	type mine struct {
		role   string
		callID string
		calls  []string
	}
	msgs := []mine{
		{role: "user"},
		{role: "assistant", calls: []string{"x"}},
		{role: "tool", callID: "x"},
		{role: "assistant"},
	}
	start := SafeKeepStartFunc(msgs, 2, func(m mine) MsgKind {
		return MsgKind{Role: m.role, ToolCallID: m.callID, ToolCallIDs: m.calls, HasToolCall: len(m.calls) > 0}
	})
	if start != 1 {
		t.Fatalf("start=%d want 1 (must not begin on the tool result)", start)
	}
}

func TestCompactChatMessagesKeepsToolPairs(t *testing.T) {
	var msgs []ChatMsg
	msgs = append(msgs, ChatMsg{Role: RoleUser, Content: "start"})
	for i := 0; i < 8; i++ {
		id := string(rune('a' + i))
		msgs = append(msgs, asst("", ToolCallRef{ID: id, Name: "ws_read", Arguments: `{"path":"f` + id + `.go"}`}))
		msgs = append(msgs, tool(id, "ws_read", "contents"))
	}
	out, ok := CompactChatMessages(msgs, 4)
	if !ok {
		t.Fatal("expected compaction")
	}
	if out[0].Role != RoleSystem {
		t.Fatalf("first message role=%s", out[0].Role)
	}
	// The message after the digest must not be a tool result.
	if out[1].Role == RoleTool {
		t.Fatalf("compaction produced an orphan tool result: %+v", out[1])
	}
	if out[len(out)-1].Content != ResumeMessage {
		t.Fatal("missing resume message")
	}
	if !strings.Contains(out[0].Content, "Files read:") {
		t.Fatalf("digest lost structure:\n%s", out[0].Content)
	}
}

func TestCompactChatMessagesNoOpWhenShort(t *testing.T) {
	msgs := []ChatMsg{{Role: RoleUser, Content: "a"}, {Role: RoleAssistant, Content: "b"}}
	if _, ok := CompactChatMessages(msgs, 8); ok {
		t.Fatal("should not compact")
	}
}

func TestElideOldToolResults(t *testing.T) {
	var msgs []ChatMsg
	for i := 0; i < 8; i++ {
		id := string(rune('a' + i))
		msgs = append(msgs, asst("", ToolCallRef{ID: id, Name: "ws_read"}))
		msgs = append(msgs, tool(id, "ws_read", "big observation "+id))
	}
	tests := []struct {
		name      string
		keepLast  int
		wantElide int
	}{
		{"keep 5", 5, 3},
		{"keep 0", 0, 8},
		{"keep all", 8, 0},
		{"keep more than present", 20, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, n := ElideOldToolResults(msgs, tc.keepLast, "")
			if n != tc.wantElide {
				t.Fatalf("elided %d want %d", n, tc.wantElide)
			}
			if len(out) != len(msgs) {
				t.Fatalf("length changed: %d vs %d", len(out), len(msgs))
			}
			calls := 0
			for _, m := range out {
				if len(m.ToolCalls) > 0 {
					calls++
				}
			}
			if calls != 8 {
				t.Fatalf("tool calls must stay visible, got %d", calls)
			}
			elided := 0
			for _, m := range out {
				if m.Content == DefaultElidedPlaceholder {
					elided++
				}
			}
			if elided != tc.wantElide {
				t.Fatalf("placeholder count %d want %d", elided, tc.wantElide)
			}
		})
	}
	// Input must not be mutated.
	if msgs[1].Content != "big observation a" {
		t.Fatalf("input mutated: %q", msgs[1].Content)
	}
}

func TestElideOldToolResultsFuncForeignType(t *testing.T) {
	type mine struct {
		role, content string
	}
	msgs := []mine{
		{"tool", "one"}, {"assistant", "x"}, {"tool", "two"}, {"tool", "three"},
	}
	out, n := ElideOldToolResultsFunc(msgs, 1, "[gone]",
		func(m mine) bool { return m.role == "tool" },
		func(m mine, p string) mine { m.content = p; return m })
	if n != 2 || out[0].content != "[gone]" || out[3].content != "three" {
		t.Fatalf("n=%d out=%+v", n, out)
	}
}
