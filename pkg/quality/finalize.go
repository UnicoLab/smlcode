package quality

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FinalizeWarnRemaining is how many tool turns before MaxIter we warn.
const FinalizeWarnRemaining = 4

// IncompleteFinalizeReason returns a quality reason when the model failed to
// produce a usable finish (empty, tool-junk XML, or GoLangGraph's synthetic
// "model ended on a tool call" blocked JSON). Empty string means OK to review.
func IncompleteFinalizeReason(output string) string {
	core := stripHarnessSections(output)
	if strings.TrimSpace(core) == "" {
		return "empty_response"
	}
	lower := strings.ToLower(core)
	if LooksLikeToolJunk(core) {
		return "ended_on_tool_call"
	}
	if strings.Contains(lower, "model ended on a tool call") {
		return "ended_on_tool_call"
	}
	// GoLangGraph / harness synthetic blocks that ask for a clearer finish.
	if (strings.Contains(lower, `"status":"blocked"`) || strings.Contains(lower, `"status": "blocked"`)) &&
		(strings.Contains(lower, "clearer finish") ||
			strings.Contains(lower, "empty finalize") ||
			strings.Contains(lower, "retry with clearer")) {
		return "ended_on_tool_call"
	}
	return ""
}

// LooksLikeToolJunk detects raw tool-call XML / Action: lines mistaken for a final answer.
func LooksLikeToolJunk(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "<function") ||
		strings.Contains(lower, "<tool_call>") ||
		strings.Contains(lower, "</tool_call>") ||
		(strings.HasPrefix(lower, "action:") && !strings.Contains(lower, "{"))
}

// FinishSteerMessage is the corrective prompt for incomplete finalize recovery.
// When hasWriteEvidence is true, forbid new tool chains and demand status JSON.
func FinishSteerMessage(reason string, hasWriteEvidence bool) string {
	base := CorrectionMessage(reason)
	if hasWriteEvidence {
		return base + " Disk/tool write evidence already exists — do NOT start new tool chains. " +
			`Emit STRICT JSON only: {"status":"done","summary":"what changed","files_changed":["real/paths"],"notes":""}.`
	}
	return base + " If you still need one edit, use ws_edit/ws_patch (ws_read first), then IMMEDIATELY " +
		`emit STRICT JSON: {"status":"done|blocked","summary":"...","files_changed":[],"notes":""}. Never end on a tool call.`
}

// ProvisionalDoneFromEvidence builds a reviewable done JSON when the model
// failed to finalize but disk/tool evidence already proves work landed.
func ProvisionalDoneFromEvidence(files []string, priorReason string) string {
	summary := "recovered incomplete finalize from disk/tool evidence"
	if priorReason != "" {
		summary += " (" + priorReason + ")"
	}
	if files == nil {
		files = []string{}
	}
	payload := map[string]interface{}{
		"status":        "done",
		"summary":       summary,
		"files_changed": files,
		"notes":         "provisional finalize — verify via disk evidence / smoke",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return `{"status":"done","summary":"recovered incomplete finalize","files_changed":[],"notes":"provisional finalize"}`
	}
	return string(raw)
}

// stripHarnessSections delegates to the single exported strip list. It used to
// carry its own literals, two of which ("## Static quality", "## Claims gate")
// never matched anything the harness emits — so a "finalize" consisting of
// nothing but a FAILED gate section read as a real answer.
func stripHarnessSections(s string) string {
	return StripHarnessSections(s)
}

// FinalizeWarnMessage reminds the model to emit status JSON before turn-cap abort.
// Port of little-coder finalize-warn — prevents "ran out of turns, no final JSON".
func FinalizeWarnMessage(maxIter int) string {
	if maxIter <= FinalizeWarnRemaining {
		return ""
	}
	return fmt.Sprintf(
		"\n## Turn budget\nYou have at most %d tool/thinking turns for this task. "+
			"When you have ~%d turns left, STOP starting new tool chains: finish edits, "+
			"run a quick smoke if needed, then emit STRICT status JSON. "+
			"Never end on a bare tool call.\n",
		maxIter, FinalizeWarnRemaining,
	)
}

// FinalizeSteerMessage is the mid-run follow-up when turns remaining hit the
// warn band (little-coder finalize-warn deliverAs:"followUp").
func FinalizeSteerMessage(remaining int) string {
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf(
		"TURN BUDGET: ~%d tool/thinking turns left. STOP starting new tool chains. "+
			"Finish pending edits, run a quick smoke if needed, then emit STRICT status JSON "+
			`({"status":"done"|"blocked",...}). Never end on a bare tool call.`,
		remaining,
	)
}

// ShouldFinalizeSteer reports whether iteration is inside the warn band.
func ShouldFinalizeSteer(iteration, maxIter int) bool {
	if maxIter <= 0 || maxIter <= FinalizeWarnRemaining {
		return false
	}
	remaining := maxIter - iteration
	return remaining > 0 && remaining <= FinalizeWarnRemaining
}

// DefaultThinkingBudgetTokens matches little-coder's default thinking budget.
const DefaultThinkingBudgetTokens = 4096

// ThinkingBudgetNudge forces SLMs out of endless deliberation (little-coder thinking-budget).
func ThinkingBudgetNudge(enabled bool) string {
	if !enabled {
		return ""
	}
	return "\n## Thinking budget\n" +
		"Deliberate briefly, then COMMIT to an implementation. Do not keep exploring " +
		"alternatives once you have a viable approach. Prefer a correct tiny ws_edit over " +
		"another round of planning. If uncertain, pick the simplest approach that meets " +
		"acceptance and verify with ws_shell.\n"
}

// ThinkingBudgetBreachMessage is the hard-abort recovery nudge after over-thinking.
func ThinkingBudgetBreachMessage() string {
	return "THINKING BUDGET EXCEEDED. Stop deliberating. Commit to the simplest viable " +
		"implementation now: use ws_edit/ws_patch (ws_read first), smoke with ws_shell, " +
		"then emit status JSON. Do not plan further alternatives."
}

// EstimateTokensApprox approximates tokens from rune/byte length (~4 chars/token).
func EstimateTokensApprox(s string) int {
	n := len(s)
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}

// ThinkingBudgetExceeded reports whether prose looks like over-thinking without a
// finished deliverable (hard-abort approximation without mid-stream cancel).
func ThinkingBudgetExceeded(output string, budgetTokens int) bool {
	if budgetTokens <= 0 {
		budgetTokens = DefaultThinkingBudgetTokens
	}
	core := strings.TrimSpace(output)
	if core == "" {
		return false
	}
	// Strip harness appendices — gate markdown is not the model's deliberation,
	// and counting it toward the thinking budget both inflated the estimate and
	// (with the two headers that never matched) left FAILED text in `core`.
	core = StripHarnessSections(core)
	if EstimateTokensApprox(core) < budgetTokens {
		return false
	}
	lower := strings.ToLower(core)
	// Finished work is fine even if verbose.
	if strings.Contains(lower, `"status":"done"`) || strings.Contains(lower, `"status": "done"`) ||
		strings.Contains(lower, `"status":"blocked"`) || strings.Contains(lower, `"approved"`) {
		return false
	}
	if strings.Contains(lower, "observation:") || strings.Contains(lower, "edited ") ||
		strings.Contains(lower, "wrote ") || strings.Contains(lower, "patched ") {
		return false
	}
	// Long prose / deliberation without tool evidence → breach.
	return true
}

// DetectTextToolCalls finds fenced/XML-ish tool calls embedded in prose
// (little-coder output-parser signal). Used to nudge corrector / quality monitor.
func DetectTextToolCalls(text string) []string {
	lower := strings.ToLower(text)
	var names []string
	add := func(n string) {
		for _, x := range names {
			if x == n {
				return
			}
		}
		names = append(names, n)
	}
	markers := []string{
		"```tool", "<tool_call>", "<function=", "<|tool_call",
		`"name": "ws_`, `"name":"ws_`,
	}
	hit := false
	for _, m := range markers {
		if strings.Contains(lower, m) || strings.Contains(text, m) {
			hit = true
			break
		}
	}
	if !hit {
		return nil
	}
	for _, tool := range []string{
		"ws_read", "ws_write", "ws_edit", "ws_patch", "ws_shell",
		"ws_glob", "ws_grep", "ws_list", "ws_mv",
	} {
		if strings.Contains(lower, tool) {
			add(tool)
		}
	}
	if len(names) == 0 {
		add("unknown")
	}
	return names
}
