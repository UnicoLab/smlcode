package plan

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

// EscalateDecide is the SLM arbitrator JSON on HITL timeout.
type EscalateDecide struct {
	Action     string  `json:"action"`
	Reason     string  `json:"reason,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

var escalateJSONRe = regexp.MustCompile(`\{[^{}]*"action"\s*:\s*"[^"]+"[^{}]*\}`)

// ParseEscalateDecide extracts an action decision from model output.
func ParseEscalateDecide(raw string) (EscalateDecide, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return EscalateDecide{}, false
	}
	var d EscalateDecide
	if err := json.Unmarshal([]byte(raw), &d); err == nil && strings.TrimSpace(d.Action) != "" {
		d.Action = NormalizeEscalateAction(d.Action)
		return d, true
	}
	// Schema-aware repair ladder: coerces the escalate contract (and records
	// per-rung telemetry) before the hand-rolled fence/substring fallbacks.
	if fixed := repairRole(extractJSON(raw), schema.RoleEscalate); fixed != "" {
		if err := json.Unmarshal([]byte(fixed), &d); err == nil && strings.TrimSpace(d.Action) != "" {
			d.Action = NormalizeEscalateAction(d.Action)
			return d, true
		}
	}
	// Strip markdown fences, then try substring from first { to last }.
	cleaned := raw
	cleaned = strings.ReplaceAll(cleaned, "```json", "")
	cleaned = strings.ReplaceAll(cleaned, "```", "")
	if i := strings.Index(cleaned, "{"); i >= 0 {
		if j := strings.LastIndex(cleaned, "}"); j > i {
			chunk := cleaned[i : j+1]
			if err := json.Unmarshal([]byte(chunk), &d); err == nil && strings.TrimSpace(d.Action) != "" {
				d.Action = NormalizeEscalateAction(d.Action)
				return d, true
			}
		}
	}
	if m := escalateJSONRe.FindString(raw); m != "" {
		if err := json.Unmarshal([]byte(m), &d); err == nil && strings.TrimSpace(d.Action) != "" {
			d.Action = NormalizeEscalateAction(d.Action)
			return d, true
		}
	}
	lower := strings.ToLower(raw)
	for _, a := range []string{EscalateActionRetry, EscalateActionReScope, EscalateActionAbort, EscalateActionMarkDone} {
		if strings.Contains(lower, `"action":"`+a+`"`) || strings.Contains(lower, `"action": "`+a+`"`) {
			return EscalateDecide{Action: a, Reason: "parsed from prose"}, true
		}
	}
	return EscalateDecide{}, false
}

// HeuristicEscalateDecide picks a safe timeout action without an LLM.
// Prefer retry on fixable quality failures; re_scope when acceptance is vague.
func HeuristicEscalateDecide(t Task, detail string) EscalateAnswer {
	blob := strings.ToLower(t.Error + " " + t.Review + " " + t.Notes + " " + detail + " " + t.Acceptance)
	switch {
	case strings.Contains(blob, "placeholder") || strings.Contains(blob, "stub") ||
		strings.Contains(blob, "static quality") || strings.Contains(blob, "smoke failed") ||
		strings.Contains(blob, "acceptance smoke") || strings.Contains(blob, "py_compile") ||
		strings.Contains(blob, "syntax"):
		return EscalateAnswer{
			Action: EscalateActionRetry,
			Notes:  "timeout heuristic: fixable quality failure → retry",
		}
	case strings.Contains(blob, "vague") || strings.Contains(blob, "clarify") ||
		strings.Contains(blob, "secret") || strings.Contains(blob, "api key") ||
		strings.Contains(blob, "needs human") && strings.Contains(blob, "scope"):
		return EscalateAnswer{
			Action: EscalateActionReScope,
			Notes:  "timeout heuristic: needs human scope → re_scope",
		}
	default:
		return EscalateAnswer{
			Action: EscalateActionRetry,
			Notes:  "timeout heuristic: default retry (SLM unavailable)",
		}
	}
}
