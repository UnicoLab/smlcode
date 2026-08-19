package composer

import (
	"fmt"
	"strings"
)

// AnnotatedComposition is the Studio/API-facing shape for a composition with
// operator hints. It deliberately keeps persisted Composition files free of
// derived UI metadata.
type AnnotatedComposition struct {
	Composition
	SLMFit []string `json:"slm_fit,omitempty"`
}

// Annotate returns a normalized composition plus compact SLM-fit hints.
func Annotate(c Composition, dynamicEnabled bool, contextLimit int) AnnotatedComposition {
	c.Normalize()
	return AnnotatedComposition{
		Composition: c,
		SLMFit:      FitHints(c, dynamicEnabled, contextLimit),
	}
}

// FitHints returns compact operator-facing hints about whether a composition is
// likely to run well on tight local-model context budgets.
func FitHints(c Composition, dynamicEnabled bool, contextLimit int) []string {
	var hints []string
	enabledPhases := 0
	for _, p := range c.Phases {
		if p.Enabled && p.When != "never" {
			enabledPhases++
		}
	}
	switch {
	case !dynamicEnabled:
		hints = append(hints, "enable dynamic_pipeline to use this task-specific composition during runs")
	case enabledPhases > 10:
		hints = append(hints, fmt.Sprintf("%d enabled phases: strong coverage, but consider a narrower request for 7B-14B local models", enabledPhases))
	case enabledPhases > 0:
		hints = append(hints, fmt.Sprintf("%d enabled phases selected for the run", enabledPhases))
	}
	if len(c.Handoff) == 0 {
		hints = append(hints, "handoff is empty; planner/splitter will have less shared context")
	} else if len(c.Handoff) > 6 {
		hints = append(hints, fmt.Sprintf("%d handoff bullets: usable, but keep it short for small context windows", len(c.Handoff)))
	} else {
		hints = append(hints, fmt.Sprintf("%d handoff bullets: compact shared context", len(c.Handoff)))
	}
	if len(c.Team) == 0 {
		hints = append(hints, "team is empty; runtime will fall back to default agents")
	}
	if genericRole(c.Execute.DefaultRole) {
		hints = append(hints, "worker role is generic; apply a language pack or mention the stack/language for stronger specialists")
	}
	if c.Execute.MaxWaves > 3 {
		hints = append(hints, fmt.Sprintf("max_waves=%d may be slow/noisy on local models", c.Execute.MaxWaves))
	}
	if len(c.Slots) > 3 {
		hints = append(hints, fmt.Sprintf("%d slots add context and latency; keep only essential specialists", len(c.Slots)))
	}
	if contextLimit > 0 && contextLimit <= 8192 && enabledPhases > 8 {
		hints = append(hints, "tight context profile: prefer <=8 phases and <=5 task handoff bullets")
	}
	if len(hints) > 6 {
		hints = hints[:6]
	}
	return hints
}

func genericRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	return role == "" || role == "worker" || role == "tester" || role == "deep"
}
