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
	// Synthetic / incomplete finalize (incl. GoLangGraph "ended on a tool call").
	if reason := IncompleteFinalizeReason(text); reason != "" && len(current) == 0 {
		return Assessment{OK: false, Reason: reason}
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
			if repeatsWithoutProgress(tc, previous) {
				return Assessment{OK: false, Reason: "repeated_tool_call"}
			}
		}
	}
	return Assessment{OK: true}
}

// repeatsWithoutProgress reports whether tc repeats an earlier call verbatim
// with NOTHING having changed the workspace since that earlier call.
//
// `previous` is an ordered history, oldest first. The old implementation
// scanned the WHOLE history for any state-changing call and unlocked unlimited
// repeats when it found one — so a model that ran one ws_edit and then read the
// same file forever kept its exemption for the rest of the window, i.e. the
// guard switched itself off exactly when a model started looping. Only a state
// change AFTER the last identical call can make the same call return something
// new; that is the same rule pkg/workspace's CallTracker enforces per task.
func repeatsWithoutProgress(tc ToolCall, previous []ToolCall) bool {
	in := mustJSON(tc.Input)
	last := -1
	for i := len(previous) - 1; i >= 0; i-- {
		if previous[i].Name == tc.Name && mustJSON(previous[i].Input) == in {
			last = i
			break
		}
	}
	if last < 0 {
		return false
	}
	for _, later := range previous[last+1:] {
		if stateChangingTools[strings.ToLower(later.Name)] {
			return false
		}
	}
	return true
}

// CorrectionMessage is steered back to the model on a quality failure.
func CorrectionMessage(reason string) string {
	switch {
	case reason == "empty_response":
		return "Your previous response was empty. STOP exploring. Emit STRICT status JSON now: " +
			`{"status":"done|blocked","summary":"...","files_changed":["real/paths"],"notes":""}. ` +
			"Do not reply with only a tool call."
	case reason == "ended_on_tool_call":
		return "You ended on a tool call (or the harness blocked a tool-junk finalize). " +
			"STOP calling tools. Emit STRICT status JSON summarizing completed work " +
			`(or status=blocked with a precise gap): {"status":"done|blocked","summary":"...","files_changed":[],"notes":""}.`
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
	case reason == "ended_on_tool_call":
		return "the model ended on a tool call without status JSON"
	case reason == "empty_tool_name":
		return "the model emitted a tool call with no name"
	case reason == "repeated_tool_call":
		return "the model repeated its previous tool call verbatim"
	case strings.HasPrefix(reason, "malformed_args:"):
		return "the model's tool arguments were malformed (" +
			strings.TrimPrefix(reason, "malformed_args:") + ")"
	case reason == "thinking_budget_exceeded":
		return "the model exceeded the thinking budget"
	case reason == "escalate" || strings.Contains(reason, "ESCALATED"):
		return "task escalated — needs human review or precise fix in Studio"
	case reason == "review":
		return "auto-approve blocked — stub/placeholder or weak evidence needs a real fix"
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
