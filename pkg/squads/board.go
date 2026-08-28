package squads

import (
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Report is the outcome of routing a whole board to squads.
type Report struct {
	// Assigned counts tasks per squad id.
	Assigned map[string]int
	// Straddling lists task ids whose files span more than one squad. These
	// are the seam, and they are deliberately left unassigned — see Assign.
	Straddling []string
	// Unowned lists task ids whose files no squad claims.
	Unowned []string
	// Idle lists squads that ended up with no work at all.
	Idle []string
}

// Summary renders the report as one event line.
func (r Report) Summary() string {
	if len(r.Assigned) == 0 && len(r.Straddling) == 0 && len(r.Unowned) == 0 {
		return "no tasks routed"
	}
	ids := make([]string, 0, len(r.Assigned))
	for id := range r.Assigned {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%s=%d", id, r.Assigned[id]))
	}
	out := strings.Join(parts, " ")
	if len(r.Straddling) > 0 {
		out += fmt.Sprintf(" · %d cross-squad", len(r.Straddling))
	}
	if len(r.Unowned) > 0 {
		out += fmt.Sprintf(" · %d unowned", len(r.Unowned))
	}
	if len(r.Idle) > 0 {
		out += " · idle: " + strings.Join(r.Idle, ",")
	}
	return out
}

// AssignBoard stamps every task with the squad that owns its files.
//
// Mutates tasks in place and returns what happened, because the caller needs
// both: the board to execute and the holes to report. A task that straddles two
// squads or lands outside every squad keeps an EMPTY Squad rather than being
// forced into the nearest one — guessing here is how a "frontend" task acquires
// permission to rewrite the API, which is the exact failure squads exist to
// prevent. Those tasks run in the normal unassigned lane and are visible in the
// report so a human (or the integration phase) can deal with them.
func AssignBoard(p *Plan, tasks []plan.Task) Report {
	rep := Report{Assigned: map[string]int{}}
	if p == nil || len(p.Squads) == 0 {
		return rep
	}
	for i := range tasks {
		a := p.Assign(tasks[i].Files)
		switch {
		case a.Squad != "":
			tasks[i].Squad = a.Squad
			rep.Assigned[a.Squad]++
		case len(a.Straddles) > 0:
			tasks[i].Squad = ""
			rep.Straddling = append(rep.Straddling, tasks[i].ID)
		default:
			tasks[i].Squad = ""
			// A task with no files at all is not a coverage hole in the plan —
			// it is a task the splitter never scoped, which the existing
			// unscoped-task path already reports. Only count tasks that named
			// files nobody owns.
			if len(a.Unowned) > 0 {
				rep.Unowned = append(rep.Unowned, tasks[i].ID)
			}
		}
	}
	for _, s := range p.Squads {
		if rep.Assigned[s.ID] == 0 {
			rep.Idle = append(rep.Idle, s.ID)
		}
	}
	return rep
}

// ForeignPatterns returns the ownership globs of every squad EXCEPT the ones
// represented in the given tasks.
//
// This is what turns ownership from a description into an enforced boundary:
// fed to workspace.FocusGuard.Protect, it makes a write outside the wave's own
// squads impossible at the tool layer rather than merely discouraged in a
// prompt. A wave is the unit because the guard is per-workspace and one
// workspace serves the whole wave.
//
// Squads present in the wave are excluded on purpose — protecting a squad's own
// paths from its own worker would block every write it was dispatched to make.
func ForeignPatterns(p *Plan, wave []plan.Task) []string {
	if p == nil || len(p.Squads) == 0 || len(wave) == 0 {
		return nil
	}
	present := map[string]bool{}
	unassigned := false
	for _, t := range wave {
		if t.Squad == "" {
			// An unassigned task has no declared lane, so no squad's paths can
			// safely be denied on its behalf: the cross-squad seam tasks are
			// exactly the ones that legitimately touch both sides.
			unassigned = true
			break
		}
		present[t.Squad] = true
	}
	if unassigned {
		return nil
	}
	var out []string
	for _, s := range p.Squads {
		if present[s.ID] {
			continue
		}
		out = append(out, s.Owns...)
	}
	sort.Strings(out)
	return out
}

// BriefFor returns the squad brief for a task, or "" when it has no squad.
func BriefFor(p *Plan, t plan.Task) string {
	if p == nil || t.Squad == "" {
		return ""
	}
	return p.Brief(t.Squad)
}
