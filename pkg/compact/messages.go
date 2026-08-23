package compact

import (
	"sort"
	"strings"
)

// ToolCallRef mirrors an assistant tool call across compaction. Without these
// fields a compacted transcript loses the link between an assistant's
// tool_calls and the tool results that answer them, and every
// OpenAI-compatible server rejects the next request with HTTP 400
// ("messages with role 'tool' must be a response to a preceding message with
// tool_calls").
type ToolCallRef struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// MsgKind is the minimal shape compaction needs from a message in order to
// slice a transcript without orphaning a tool result. Callers that keep their
// own message type (pkg/loop's session.ReactMessage, llm.Message, …) describe
// it with this struct and use the *Func helpers, so they never have to convert
// to ChatMsg and lose fields.
type MsgKind struct {
	Role        string
	ToolCallIDs []string // ids this (assistant) message requests
	ToolCallID  string   // id this (tool) message answers
	HasToolCall bool
}

// RoleTool / RoleAssistant are the roles the tool-pair invariant cares about.
const (
	RoleTool      = "tool"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleUser      = "user"
)

// KindOf describes one ChatMsg.
func KindOf(m ChatMsg) MsgKind {
	k := MsgKind{Role: strings.ToLower(strings.TrimSpace(m.Role)), ToolCallID: m.ToolCallID}
	for _, tc := range m.ToolCalls {
		k.ToolCallIDs = append(k.ToolCallIDs, tc.ID)
	}
	k.HasToolCall = len(m.ToolCalls) > 0
	return k
}

// SafeKeepStartFunc returns the index a kept window may start at so that the
// window never begins on (or contains) an orphaned tool result.
//
// It starts from len(msgs)-keepLast and walks BACKWARDS, extending the window
// until the first message is not a tool result and every tool result inside the
// window is preceded by the assistant message that requested it. The returned
// index is always in [0, len(msgs)].
func SafeKeepStartFunc[T any](msgs []T, keepLast int, kind func(T) MsgKind) int {
	n := len(msgs)
	if n == 0 {
		return 0
	}
	if keepLast <= 0 {
		keepLast = 8
	}
	start := n - keepLast
	if start <= 0 {
		return 0
	}
	kinds := make([]MsgKind, n)
	for i, m := range msgs {
		k := kind(m)
		k.Role = strings.ToLower(strings.TrimSpace(k.Role))
		kinds[i] = k
	}
	for start > 0 {
		if orphanFree(kinds, start) {
			return start
		}
		start--
	}
	return 0
}

// orphanFree reports whether the window kinds[start:] is self-consistent:
// the window does not begin on a tool result, and every tool result inside it
// is answered by an assistant tool call that is also inside it.
func orphanFree(kinds []MsgKind, start int) bool {
	if start >= len(kinds) {
		return true
	}
	if kinds[start].Role == RoleTool {
		return false
	}
	requested := map[string]bool{}
	anyCallSeen := false
	for i := start; i < len(kinds); i++ {
		k := kinds[i]
		if k.HasToolCall || len(k.ToolCallIDs) > 0 {
			anyCallSeen = true
			for _, id := range k.ToolCallIDs {
				if id != "" {
					requested[id] = true
				}
			}
		}
		if k.Role != RoleTool {
			continue
		}
		if k.ToolCallID != "" {
			if !requested[k.ToolCallID] {
				return false
			}
			continue
		}
		// No id available (older transcripts): require that some assistant
		// tool call has been seen earlier inside the window.
		if !anyCallSeen {
			return false
		}
	}
	return true
}

// SafeKeepStart is SafeKeepStartFunc for []ChatMsg.
func SafeKeepStart(msgs []ChatMsg, keepLast int) int {
	return SafeKeepStartFunc(msgs, keepLast, KindOf)
}

// ElideOldToolResultsFunc keeps the last keepLast tool RESULTS verbatim and
// replaces the content of older ones with placeholder, leaving every tool CALL
// (and therefore every tool pair) intact.
//
// This is deterministic observation elision — deepagents' ClearToolUsesEdit and
// SWE-agent's last-5 observation collapse. It costs no inference and should
// always be attempted BEFORE any LLM summarization. It returns the rewritten
// slice and the number of results elided.
func ElideOldToolResultsFunc[T any](msgs []T, keepLast int, placeholder string,
	isToolResult func(T) bool, replace func(T, string) T) ([]T, int) {
	if len(msgs) == 0 || replace == nil || isToolResult == nil {
		return msgs, 0
	}
	if keepLast < 0 {
		keepLast = 0
	}
	if strings.TrimSpace(placeholder) == "" {
		placeholder = DefaultElidedPlaceholder
	}
	var idx []int
	for i, m := range msgs {
		if isToolResult(m) {
			idx = append(idx, i)
		}
	}
	if len(idx) <= keepLast {
		return msgs, 0
	}
	elide := map[int]bool{}
	for _, i := range idx[:len(idx)-keepLast] {
		elide[i] = true
	}
	out := make([]T, len(msgs))
	copy(out, msgs)
	n := 0
	for i := range out {
		if elide[i] {
			out[i] = replace(out[i], placeholder)
			n++
		}
	}
	return out, n
}

// DefaultElidedPlaceholder replaces an old tool observation.
const DefaultElidedPlaceholder = "[tool result elided]"

// DefaultElideKeepLast is the number of full tool results to retain.
const DefaultElideKeepLast = 5

// ElideOldToolResults is ElideOldToolResultsFunc for []ChatMsg.
func ElideOldToolResults(msgs []ChatMsg, keepLast int, placeholder string) ([]ChatMsg, int) {
	return ElideOldToolResultsFunc(msgs, keepLast, placeholder,
		func(m ChatMsg) bool { return strings.EqualFold(m.Role, RoleTool) },
		func(m ChatMsg, p string) ChatMsg { m.Content = p; return m })
}

// sortedUnique is a small helper shared by digest rendering.
func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
