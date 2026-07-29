package multipass

import (
	"context"
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
func (r *Runner) Execute(ctx context.Context, a agent.Agent, input string) (string, error) {
	draftExec, err := a.Execute(ctx, input+"\n\nPass: DRAFT. Produce your best answer now.")
	if err != nil {
		return "", err
	}
	draft := asString(draftExec.Output)
	current := draft

	for i := 1; i <= r.Passes; i++ {
		critiquePrompt := fmt.Sprintf(`Pass: CRITIQUE (%d/%d).
You previously produced:

---
%s
---

List concrete defects only (missing acceptance, wrong APIs, scope creep, incomplete edits).
Be brief. If quality is already high, reply exactly: LOOKS_GOOD`, i, r.Passes, truncate(current, 6000))

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

Produce an improved final answer. Keep the required output schema.`, i, r.Passes, truncate(input, 4000), truncate(current, 6000), critique)

		refExec, err := a.Execute(ctx, refinePrompt)
		if err != nil {
			return current, nil
		}
		current = asString(refExec.Output)
	}
	return current, nil
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
