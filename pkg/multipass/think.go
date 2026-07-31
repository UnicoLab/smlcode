package multipass

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// Runner performs multi-pass think → critique → refine cycles so SLMs can
// approach LLM-level quality without holding huge context in one shot.
type Runner struct {
	Passes int // total refine iterations after the first draft (default 2)
}

func New(passes int) *Runner {
	if passes <= 0 {
		passes = 2
	}
	return &Runner{Passes: passes}
}

// Execute runs agent with optional critique/refine loops. The agent should be
// a chat/react specialist; input is the scoped task pack.
//
// Early-exit: if the draft already looks like complete structured JSON, skip
// critique/refine (saves 1–2 SLM round-trips on planner/splitter/worker).
func (r *Runner) Execute(ctx context.Context, a agent.Agent, input string) (string, error) {
	draftExec, err := a.Execute(ctx, input+"\n\nPass: DRAFT. Produce your best answer now.")
	if err != nil {
		return "", err
	}
	draft := asString(draftExec.Output)
	current := draft

	if LooksCompleteJSON(draft) {
		return draft, nil
	}

	for i := 1; i <= r.Passes; i++ {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		critiquePrompt := fmt.Sprintf(`Pass: CRITIQUE (%d/%d).
You previously produced:

---
%s
---

List concrete defects only (missing acceptance, wrong APIs, scope creep, incomplete edits).
Be brief. If quality is already high, reply exactly: LOOKS_GOOD`, i, r.Passes, truncate(current, 4000))

		critExec, err := a.Execute(ctx, critiquePrompt)
		if err != nil {
			return current, nil // keep best draft
		}
		critique := asString(critExec.Output)
		if strings.Contains(strings.ToUpper(critique), "LOOKS_GOOD") {
			break
		}

		refinePrompt := fmt.Sprintf(`Pass: REFINE (%d/%d).
Original task:
%s

Previous answer:
%s

Critique to address:
%s

Produce an improved final answer. Keep the required output schema.`, i, r.Passes, truncate(input, 3000), truncate(current, 4000), truncate(critique, 1500))

		refExec, err := a.Execute(ctx, refinePrompt)
		if err != nil {
			return current, nil
		}
		current = asString(refExec.Output)
		if LooksCompleteJSON(current) && i >= r.Passes {
			break
		}
	}
	return current, nil
}

// LooksCompleteJSON reports whether s contains a parseable JSON object with
// enough structure that further critique/refine is unlikely to help latency.
func LooksCompleteJSON(s string) bool {
	raw := extractJSONObject(s)
	if raw == "" {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) == 0 {
		return false
	}
	// Common planning / task / worker / review shapes.
	if _, ok := m["tasks"]; ok {
		return true
	}
	if _, ok := m["steps"]; ok {
		return true
	}
	if _, ok := m["approved"]; ok {
		return true
	}
	if status, ok := m["status"].(string); ok {
		st := strings.ToLower(status)
		return st == "done" || st == "blocked"
	}
	if _, ok := m["passed"]; ok {
		return true
	}
	if _, ok := m["summary"]; ok {
		if _, ok2 := m["goals"]; ok2 {
			return true
		}
		if _, ok2 := m["relevant_files"]; ok2 {
			return true
		}
		if _, ok2 := m["actions"]; ok2 {
			return true
		}
	}
	return false
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return ""
	}
	return s[i : j+1]
}

func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}
