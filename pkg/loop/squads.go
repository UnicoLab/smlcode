package loop

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// ── Squad wiring ─────────────────────────────────────────────────────────
//
// Two virtual teams building at once need exactly two things from the loop
// that a single stream does not:
//
//   1. every worker must know which half is ITS half, and what the other half
//      is building against — the brief;
//   2. it must be unable to write the other half's files even when its own
//      build fails against them — the boundary.
//
// The second is the one that matters. A prompt saying "do not edit web/" is a
// suggestion a stuck model talks itself out of; a deny list in the tool layer
// is not.

// squadBriefSection renders the per-task squad brief injected into the worker
// prompt.
//
// Only the task's OWN squad, never the whole contract: a worker handed both
// halves spends its attention reading the team it is not on, and on a 30B-class
// model that attention is the scarce resource the whole design is built around.
func (r *Runner) squadBriefSection(t plan.Task) string {
	if r == nil || r.Squads == nil {
		return ""
	}
	brief := squads.BriefFor(r.Squads, t)
	if strings.TrimSpace(brief) == "" {
		return ""
	}
	return "\n" + brief + "\n"
}

// squadProtections returns the ownership deny list for a wave, or nil when the
// wave has no squad boundary to enforce.
//
// Merged with — not instead of — the protections derived from task text: a
// squad boundary and a "do not touch the tests" instruction are different
// promises and both have to hold.
func (r *Runner) squadProtections(wave []plan.Task) []string {
	if r == nil || r.Squads == nil {
		return nil
	}
	return squads.ForeignPatterns(r.Squads, wave)
}

// squadOf reports the squad a task belongs to, "" when unassigned.
func squadOf(t plan.Task) string { return strings.TrimSpace(t.Squad) }

// waveSquads lists the distinct squads represented in a wave, in first-seen
// order, for the event line that tells a watching human which teams are live.
func waveSquads(wave []plan.Task) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range wave {
		id := squadOf(t)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
