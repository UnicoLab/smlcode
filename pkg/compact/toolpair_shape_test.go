package compact

import (
	"fmt"
	"testing"
)

// openAIShapeErr models the two hard constraints an OpenAI-compatible chat
// completions endpoint enforces on a message list: a tool result must directly
// follow the assistant tool_calls message that requested it, and an assistant
// tool_calls message must be followed by the tool results answering it.
func openAIShapeErr(msgs []ChatMsg) error {
	for i, m := range msgs {
		switch m.Role {
		case RoleTool:
			j := i - 1
			for j >= 0 && msgs[j].Role == RoleTool {
				j--
			}
			if j < 0 || msgs[j].Role != RoleAssistant || len(msgs[j].ToolCalls) == 0 {
				return fmt.Errorf("msg[%d] tool result is not preceded by an assistant tool_calls message", i)
			}
		case RoleAssistant:
			if len(m.ToolCalls) == 0 {
				continue
			}
			need := map[string]bool{}
			for _, tc := range m.ToolCalls {
				need[tc.ID] = true
			}
			j := i + 1
			for j < len(msgs) && msgs[j].Role == RoleTool {
				delete(need, msgs[j].ToolCallID)
				j++
			}
			if len(need) > 0 {
				return fmt.Errorf("msg[%d] assistant tool_calls unanswered: %v", i, need)
			}
		}
	}
	return nil
}

func mkMsg(role, content string) ChatMsg { return ChatMsg{Role: role, Content: content} }

func padTurns(n int) []ChatMsg {
	var out []ChatMsg
	for i := 0; i < n; i++ {
		out = append(out, mkMsg(RoleUser, fmt.Sprintf("u%d", i)), mkMsg(RoleAssistant, fmt.Sprintf("a%d", i)))
	}
	return out
}

// Compaction must never turn a valid transcript into one an OpenAI-compatible
// server rejects.
func TestCompactionKeepsOpenAIShape(t *testing.T) {
	cases := map[string][]ChatMsg{
		"empty":            {},
		"two-assistants":   append(padTurns(8), mkMsg(RoleAssistant, "a"), mkMsg(RoleAssistant, "b")),
		"tool-pair-at-end": append(padTurns(8), ChatMsg{Role: RoleAssistant, ToolCalls: []ToolCallRef{{ID: "c1"}}}, ChatMsg{Role: RoleTool, ToolCallID: "c1", Content: "r"}),
		"three-results-one-call": append(padTurns(8),
			ChatMsg{Role: RoleAssistant, ToolCalls: []ToolCallRef{{ID: "c1"}}},
			ChatMsg{Role: RoleTool, ToolCallID: "c1", Content: "r1"},
			ChatMsg{Role: RoleTool, ToolCallID: "c1", Content: "r2"},
			ChatMsg{Role: RoleTool, ToolCallID: "c1", Content: "r3"},
		),
	}
	for name, in := range cases {
		out, _ := CompactChatMessages(in, 8)
		if err := openAIShapeErr(out); err != nil {
			t.Errorf("%s: compacted history rejected by OpenAI shape: %v", name, err)
		}
	}
}

// An interrupted ReAct checkpoint is saved with tool calls still pending — that
// is what ReactCheckpoint.PendingToolCalls means. Compaction must not insert a
// user message between that assistant message and the tool results the executor
// is about to append, or resuming the run is a permanent HTTP 400.
func TestCompactionDoesNotOrphanPendingToolCalls(t *testing.T) {
	in := append(padTurns(10), ChatMsg{
		Role: RoleAssistant, ToolCalls: []ToolCallRef{{ID: "c1", Name: "ws_edit"}},
	})
	out, ok := CompactChatMessages(in, 8)
	if !ok {
		t.Fatal("expected compaction to run")
	}
	last := out[len(out)-1]
	if last.Role != RoleAssistant || len(last.ToolCalls) == 0 {
		t.Fatalf("compaction appended %q after an unanswered assistant tool_calls message; "+
			"the executor's tool results can no longer follow it", last.Role)
	}
	// The resume notice must survive somewhere — folded into the digest.
	if out[0].Role != RoleSystem {
		t.Fatalf("expected a leading system digest, got %q", out[0].Role)
	}
	// And the transcript must still be valid once the pending results land.
	replayed := append(append([]ChatMsg{}, out...), ChatMsg{Role: RoleTool, ToolCallID: "c1", Content: "done"})
	if err := openAIShapeErr(replayed); err != nil {
		t.Fatalf("resumed transcript rejected by OpenAI shape: %v", err)
	}
}

func TestTrailingUnansweredToolCalls(t *testing.T) {
	tests := []struct {
		name string
		msgs []ChatMsg
		want bool
	}{
		{"empty", nil, false},
		{"plain assistant", []ChatMsg{mkMsg(RoleAssistant, "hi")}, false},
		{"answered pair", []ChatMsg{
			{Role: RoleAssistant, ToolCalls: []ToolCallRef{{ID: "a"}}},
			{Role: RoleTool, ToolCallID: "a"},
		}, false},
		{"unanswered", []ChatMsg{
			{Role: RoleAssistant, ToolCalls: []ToolCallRef{{ID: "a"}}},
		}, true},
		{"partially answered", []ChatMsg{
			{Role: RoleAssistant, ToolCalls: []ToolCallRef{{ID: "a"}, {ID: "b"}}},
			{Role: RoleTool, ToolCallID: "a"},
		}, true},
		{"user last", []ChatMsg{
			{Role: RoleAssistant, ToolCalls: []ToolCallRef{{ID: "a"}}},
			{Role: RoleTool, ToolCallID: "a"},
			mkMsg(RoleUser, "next"),
		}, false},
	}
	for _, tc := range tests {
		if got := TrailingUnansweredToolCalls(tc.msgs); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
