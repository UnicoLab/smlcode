package quality

import "strings"

// Intervention reasons surfaced in TUI/Studio banners (not debug-hidden).
const (
	InterventionLoop      = "loop"
	InterventionWhitelist = "shell_whitelist"
	InterventionThinking  = "thinking_budget"
	InterventionTextTools = "text_tools"
	InterventionFinalize  = "finalize"
	InterventionMalformed = "malformed_args"
	InterventionQuality   = "quality"
)

// Intervention is a harness action the user should see.
type Intervention struct {
	Reason  string // short machine code
	Message string // human-facing line
	Detail  string // optional recovery / args
}

// ClassifyIntervention maps quality-monitor / gate reasons to banner codes.
func ClassifyIntervention(reason string) string {
	switch {
	case reason == "repeated_tool_call" || strings.Contains(reason, "QUALITY MONITOR"):
		return InterventionLoop
	case strings.HasPrefix(reason, "malformed_args:"):
		return InterventionMalformed
	case reason == "thinking_budget_exceeded" || strings.Contains(reason, "THINKING BUDGET"):
		return InterventionThinking
	case strings.HasPrefix(reason, "text_tool_calls:") || strings.Contains(reason, "embedded tool"):
		return InterventionTextTools
	case strings.Contains(reason, "shell whitelist") || strings.Contains(reason, "SAFE_PREFIXES"):
		return InterventionWhitelist
	case strings.Contains(reason, "TURN BUDGET") || strings.Contains(reason, "finalize"):
		return InterventionFinalize
	default:
		return InterventionQuality
	}
}
