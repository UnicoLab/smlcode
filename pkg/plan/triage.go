package plan

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ── Manager triage of a rejected delivery ────────────────────────────────
//
// The deterministic ladder (language corrector → generic corrector → worker)
// answers "who else could hold this". A manager answers a different and more
// useful question: who SHOULD hold it, and what do they need to know that the
// last attempt did not.
//
// The second half is the part a ladder cannot produce. An agent handed the same
// task with the same context makes the same attempt; what changes the outcome
// is being told what to do differently, and that requires reading the failure.

// TriageDecision is the manager's answer for one rejected task.
type TriageDecision struct {
	// Assignee is the agent id that should take it next.
	Assignee string `json:"assignee"`
	// Reason is one line for the human reading the board.
	Reason string `json:"reason"`
	// Guidance is what the next agent needs that the last one did not have.
	Guidance string `json:"guidance,omitempty"`
	// Priority is normal | high.
	Priority string `json:"priority,omitempty"`
}

// ParseTriage reads a manager verdict, tolerating the wrappers small models add.
func ParseTriage(raw string) (TriageDecision, error) {
	body := extractFirstJSONObject(raw)
	if body == "" {
		return TriageDecision{}, fmt.Errorf("triage: no JSON object in %d bytes", len(raw))
	}
	var d TriageDecision
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return TriageDecision{}, fmt.Errorf("triage: %w", err)
	}
	d.Assignee = strings.ToLower(strings.TrimSpace(d.Assignee))
	d.Reason = strings.TrimSpace(d.Reason)
	d.Guidance = strings.TrimSpace(d.Guidance)
	d.Priority = strings.ToLower(strings.TrimSpace(d.Priority))
	if d.Priority != "high" {
		d.Priority = "normal"
	}
	return d, nil
}

// Usable reports whether a decision can actually be acted on.
//
// Two ways a manager's answer is worse than no answer, and both have to be
// caught here rather than at dispatch:
//
//   - an agent that is not registered cannot be dispatched, so the task would
//     sit in ready_to_dev with nobody able to move it — strictly worse than the
//     deterministic fallback;
//   - the agent that just failed has already spent every retry the ladder
//     allows. Re-picking it is the loop triage exists to end.
func (d TriageDecision) Usable(failedRole string, roleExists func(string) bool) (bool, string) {
	if d.Assignee == "" {
		return false, "named no assignee"
	}
	if strings.EqualFold(d.Assignee, strings.TrimSpace(failedRole)) {
		return false, fmt.Sprintf("re-picked %q, the agent that just exhausted its retries", d.Assignee)
	}
	if roleExists == nil || !roleExists(d.Assignee) {
		return false, fmt.Sprintf("named %q, which is not a registered agent", d.Assignee)
	}
	return true, ""
}

// extractFirstJSONObject finds the outermost {...}, ignoring braces in strings.
func extractFirstJSONObject(raw string) string {
	start := strings.Index(raw, "{")
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}
