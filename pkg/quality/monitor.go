package quality

import (
	"encoding/json"
	"strings"
)

// ToolCall is a name+args snapshot for loop detection.
type ToolCall struct {
	Name  string
	Input interface{}
}

// Assessment is the quality-monitor verdict (little-coder quality.py port).
type Assessment struct {
	OK     bool
	Reason string
}

var stateChangingTools = map[string]bool{
	"ws_edit": true, "ws_write": true, "ws_patch": true, "ws_mv": true,
	"ws_delete": true, "ws_shell": true, "bash": true, "edit": true, "write": true,
}

// AssessResponse detects empty replies, hallucinated tools, text-embedded tool
// calls, and verbatim loops.
func AssessResponse(text string, current, previous []ToolCall, knownTools map[string]bool) Assessment {
	if strings.TrimSpace(text) == "" && len(current) == 0 {
		return Assessment{OK: false, Reason: "empty_response"}
	}
	// Native tool channel empty but prose contains fenced/XML tool calls.
	if len(current) == 0 {
		if names := DetectTextToolCalls(text); len(names) > 0 {
			return Assessment{OK: false, Reason: "text_tool_calls:" + strings.Join(names, ",")}
		}
	}
	for _, tc := range current {
		if strings.TrimSpace(tc.Name) == "" {
			return Assessment{OK: false, Reason: "empty_tool_name"}
		}
		if len(knownTools) > 0 && !knownTools[tc.Name] {
			return Assessment{OK: false, Reason: "unknown_tool:" + tc.Name}
		}
		if hasMalformedArgs(tc.Input) {
			return Assessment{OK: false, Reason: "malformed_args:" + tc.Name}
		}
	}
	if len(current) > 0 && len(previous) > 0 {
		for _, tc := range current {
			tcIn := mustJSON(tc.Input)
			for _, prev := range previous {
				if tc.Name == prev.Name && mustJSON(prev.Input) == tcIn {
					envChanged := false
					for _, r := range previous {
						same := r.Name == tc.Name && mustJSON(r.Input) == tcIn
						if !same && stateChangingTools[strings.ToLower(r.Name)] {
							envChanged = true
							break
						}
					}
					if envChanged {
						continue
					}
					return Assessment{OK: false, Reason: "repeated_tool_call"}
				}
			}
		}
	}
	return Assessment{OK: true}
}

// CorrectionMessage is steered back to the model on a quality failure.
func CorrectionMessage(reason string) string {
	switch {
	case reason == "empty_response":
		return "Your previous response was empty. Respond with text or a tool call " +
			"(ws_read/ws_edit/ws_write/ws_shell) to make progress."
	case reason == "empty_tool_name":
		return "Your tool call had an empty name. Use a real tool: ws_read, ws_write, " +
			"ws_edit, ws_patch, ws_shell, ws_glob, ws_grep."
	case reason == "repeated_tool_call":
		return "You just made the exact same tool call as your previous turn — you may be " +
			"stuck. Try a different approach (re-read the file, adjust old_str, or run a smoke test)."
	case strings.HasPrefix(reason, "unknown_tool:"):
		name := strings.TrimPrefix(reason, "unknown_tool:")
		return "Tool '" + name + "' does not exist. Available: ws_read, ws_write, ws_edit, " +
			"ws_patch, ws_shell, ws_glob, ws_grep, ws_list, ws_mv, git_status, git_diff."
	case strings.HasPrefix(reason, "text_tool_calls:"):
		names := strings.TrimPrefix(reason, "text_tool_calls:")
		return "You embedded tool calls in text (" + names + "). Re-issue them as NATIVE " +
			"tool calls (not fenced ```tool / <tool_call> prose), then finish with status JSON."
	case strings.HasPrefix(reason, "malformed_args:"):
		name := strings.TrimPrefix(reason, "malformed_args:")
		return "The arguments for tool '" + name + "' were malformed (not valid JSON). " +
			"Re-issue the tool call with a proper JSON object for arguments."
	case reason == "thinking_budget_exceeded":
		return ThinkingBudgetBreachMessage()
	default:
		return "Issue detected: " + reason + ". Please try a different approach."
	}
}

// PhraseForUser is a short harness-intervention line.
func PhraseForUser(reason string) string {
	switch {
	case strings.HasPrefix(reason, "unknown_tool:"):
		return "the model called a tool that doesn't exist (" +
			strings.TrimPrefix(reason, "unknown_tool:") + ")"
	case strings.HasPrefix(reason, "text_tool_calls:"):
		return "the model wrote tool calls as text instead of native calls"
	case reason == "empty_response":
		return "the model returned an empty response"
	case reason == "empty_tool_name":
		return "the model emitted a tool call with no name"
	case reason == "repeated_tool_call":
		return "the model repeated its previous tool call verbatim"
	case strings.HasPrefix(reason, "malformed_args:"):
		return "the model's tool arguments were malformed (" +
			strings.TrimPrefix(reason, "malformed_args:") + ")"
	case reason == "thinking_budget_exceeded":
		return "the model exceeded the thinking budget"
	default:
		return "quality issue (" + reason + ")"
	}
}

func hasMalformedArgs(input interface{}) bool {
	m, ok := input.(map[string]interface{})
	if !ok || m == nil {
		return false
	}
	_, has := m["_raw"]
	return has
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
