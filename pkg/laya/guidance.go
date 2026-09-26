package laya

import (
	"context"
	"strings"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
)

// Guidance returns only harness-authored hints. No generated prose, file paths,
// permissions or acceptance decisions can enter the harness from the service.
// Scores are advisory, not calibrated confidence or evidence of correctness.
func (c *Client) Guidance(ctx context.Context, role, input string) (string, error) {
	question, hint := guidanceQuestion(role)
	if question == "" {
		return "", nil
	}
	limit := 640
	if c.options.Provider == "laya" && c.options.Model == "english" {
		limit = 320
	}
	state := excerpt(input, limit)
	score, err := c.Probability(ctx, state, question)
	if err != nil || score < 0.8 {
		return "", err
	}
	return "\n\n## Optional decision-model hint\nBased on a bounded prompt excerpt; verify against the actual task and files. " + hint +
		" Existing scope, user instructions, required tests and acceptance gates still apply.\n", nil
}

func guidanceQuestion(role string) (string, string) {
	switch {
	case strings.Contains(role, "corrector"):
		return "Does this repair need investigation of the underlying cause rather than repeating the previous edit?", "Trace the failing behavior and inspect prior attempts before editing; verify the smallest supported fix."
	case strings.Contains(role, "reviewer"), strings.Contains(role, "tester"), strings.Contains(role, "critic"):
		return "Does this delivery have a substantial risk of unverified behavior or regression?", "Prioritize checking claims against disk evidence, failure paths, boundary cases and relevant regression tests."
	case strings.Contains(role, "planner"), strings.Contains(role, "architect"), strings.Contains(role, "splitter"), strings.Contains(role, "coordinator"):
		return "Does this task involve coupled changes that need explicit dependencies or specialist ownership?", "Identify dependencies and suitable existing specialists; split work into independently verifiable steps with explicit file ownership."
	case strings.Contains(role, "worker"), strings.Contains(role, "explorer"), role == "context":
		return "Does this task require locating existing definitions or callers before making a change?", "Locate definitions, callers and neighboring tests within the allowed scope before editing; reuse the existing APIs."
	default:
		return "", ""
	}
}

// excerpt preserves both the stable task prefix and recent evidence suffix.
// It is explicitly partial and must never be used to approve or reject work.
func excerpt(input string, limit int) string {
	const marker = "\n[Middle omitted; partial context]\n"
	if contextstore.DefaultTokenCounter(input) <= limit {
		return input
	}
	chars := []rune(input)
	n := min(len(chars)/2, limit)
	for n > 0 {
		state := string(chars[:n]) + marker + string(chars[len(chars)-n:])
		if contextstore.DefaultTokenCounter(state) <= limit {
			return state
		}
		n /= 2
	}
	return marker
}
