package squads

import (
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// ── Cutting a straddling task along the seam ─────────────────────────────
//
// # WHY THIS EXISTS
//
// A task whose files span two teams is assigned to neither, and that is the
// right answer to the question Assign asks: handing the seam to one half is how
// a frontend task acquires permission to rewrite the API.
//
// It is the wrong answer to the question the RUN needs answered, and measured
// live it is the difference between teams working and teams existing. Twice,
// against two different local models, a run finished with EVERY task straddling:
//
//	task T1  squad=  files=[cmd/server/main.go web/src/App.tsx]
//	task T2  squad=  files=[cmd/server/main.go web/src/App.tsx]
//	routing: 0 task(s) in a lane, 2 straddling both halves
//
// The org chart was right. The contract was frozen and correct. Both files were
// edited. The run reported SUCCESS. And the teams did nothing at all: no
// parallel waves, no ownership fence, no per-team acceptance — because not one
// task belonged to anybody. Nothing in the output distinguishes that from a run
// where the teams worked, which is what makes it worth code rather than a
// warning.
//
// Telling the splitter about the boundaries helps and does not settle it: a
// model is free to ignore the instruction, and one of the two did, repeatedly.
//
// So the harness cuts the task itself. A task naming `cmd/server/main.go` and
// `web/src/App.tsx` IS two tasks — one per team, each with only that team's
// files — and deriving them needs no model call, only the ownership map that
// already exists. What the split cannot recover is intent, so it is deliberately
// conservative: see SplitStraddlers for the four cases it refuses.

// SplitStraddlers cuts tasks that span several teams into one task per team.
//
// Returns the new task list and a line naming what was cut, empty when nothing
// was. Deterministic: teams are visited in plan order, so the same board always
// produces the same tasks in the same order.
//
// It refuses four cases, each because the split would destroy something:
//
//   - a task with FEWER THAN TWO owned files — there is nothing to cut;
//   - a task whose files land in one team or in none — Assign already handles
//     both, and a split would be a no-op or a guess;
//   - a NON-IMPLEMENTER task — a tester verifying that the halves meet is doing
//     the one job that is genuinely about both, and cutting it in half produces
//     two testers that each verify nothing;
//   - a task other tasks DEPEND ON — the dependents named one id, and rewriting
//     that into "all of the pieces" changes the shape of the wave graph on a
//     guess about which piece they meant.
func SplitStraddlers(p *Plan, tasks []plan.Task) ([]plan.Task, string) {
	if p == nil || len(p.Squads) < 2 || len(tasks) == 0 {
		return tasks, ""
	}

	dependedOn := map[string]bool{}
	for _, t := range tasks {
		for _, d := range t.DependsOn {
			dependedOn[strings.ToUpper(strings.TrimSpace(d))] = true
		}
	}
	taken := map[string]bool{}
	for _, t := range tasks {
		taken[strings.ToUpper(strings.TrimSpace(t.ID))] = true
	}

	out := make([]plan.Task, 0, len(tasks))
	var cut []string
	for _, t := range tasks {
		byTeam, _, why := splitPlan(p, t, dependedOn)
		if byTeam == nil || why != "" {
			out = append(out, t)
			continue
		}
		// Plan order, so the backend's piece is always the backend's piece.
		for _, s := range p.Squads {
			files := byTeam[s.ID]
			if len(files) == 0 {
				continue
			}
			piece := t
			piece.ID = nextPieceID(t.ID, s.ID, taken)
			taken[strings.ToUpper(piece.ID)] = true
			piece.Squad = s.ID
			piece.Files = files
			piece.Title = t.Title + " — " + s.ID
			// The description keeps the WHOLE job and names the boundary. A
			// piece told only about its own half writes against a seam it
			// cannot see; a piece told nothing about the boundary edits across
			// it and is refused at the tool layer.
			piece.Description = strings.TrimSpace(t.Description) +
				fmt.Sprintf("\n\nThis is the %s half of that work. Change ONLY %s; "+
					"the rest is another team's, being built at the same time, "+
					"and the frozen contract is what you build against.",
					s.ID, strings.Join(files, ", "))
			piece.Normalize()
			out = append(out, piece)
		}
		cut = append(cut, t.ID)
	}
	if len(cut) == 0 {
		return tasks, ""
	}
	sort.Strings(cut)
	return out, fmt.Sprintf("cut %s along the team boundary — a task spanning two teams "+
		"belongs to neither, so it would have run alone outside both lanes",
		strings.Join(cut, ", "))
}

// splitPlan groups a task's files by owning team and reports whether cutting it
// is safe.
//
// Three results, and they are distinct on purpose. straddles=false means the
// task is not on a seam at all and there is nothing to decide. straddles=true
// with an empty reason means cut it. straddles=true with a reason means leave it
// whole, and the reason is what a reader is owed — a task sitting outside every
// lane looks identical whether the harness chose that or failed to notice.
func splitPlan(p *Plan, t plan.Task, dependedOn map[string]bool) (map[string][]string, bool, string) {
	if len(t.Files) < 2 {
		return nil, false, ""
	}
	byTeam := map[string][]string{}
	unowned := ""
	for _, f := range t.Files {
		owner, owned := p.Owner(f)
		if !owned {
			if unowned == "" {
				unowned = f
			}
			continue
		}
		byTeam[owner] = append(byTeam[owner], f)
	}
	if len(byTeam) < 2 {
		return nil, false, ""
	}
	switch {
	case unowned != "":
		// A file nobody owns cannot be given to a piece, and dropping it would
		// silently narrow the work. Leave the whole task alone.
		return nil, true, "it also touches " + unowned + ", which no team owns — " +
			"dropping that file to make the cut would silently narrow the work"
	case !plan.IsImplementerRole(t.Role):
		return nil, true, "it is a " + roleLabel(t.Role) + " on the seam — verifying that " +
			"the halves meet is the one job genuinely about both, and two half-testers " +
			"each verify nothing"
	case dependedOn[strings.ToUpper(strings.TrimSpace(t.ID))]:
		return nil, true, "other tasks depend on it — they named one id, and rewriting that " +
			"into all of the pieces would change the wave graph on a guess about which " +
			"piece they meant"
	}
	return byTeam, true, ""
}

// roleLabel names a role for a human, without inventing one when it is blank.
func roleLabel(role string) string {
	if r := strings.TrimSpace(role); r != "" {
		return r
	}
	return "non-implementer task"
}

// CutRefusal explains why the task with the given id was left spanning several
// teams rather than cut along the boundary.
//
// Empty when the task is not on a seam, was cut, or is not on the board. The
// caller reporting a straddler should say WHY it stayed whole: the previous
// message asserted every straddler "belongs to integration", which is true of a
// seam tester and false of a task that merely had a dependent.
func CutRefusal(p *Plan, tasks []plan.Task, id string) string {
	if p == nil || len(p.Squads) < 2 {
		return ""
	}
	want := strings.ToUpper(strings.TrimSpace(id))
	dependedOn := map[string]bool{}
	for _, t := range tasks {
		for _, d := range t.DependsOn {
			dependedOn[strings.ToUpper(strings.TrimSpace(d))] = true
		}
	}
	for _, t := range tasks {
		if strings.ToUpper(strings.TrimSpace(t.ID)) != want {
			continue
		}
		_, straddles, why := splitPlan(p, t, dependedOn)
		if !straddles {
			return ""
		}
		return why
	}
	return ""
}

// nextPieceID names a piece after its parent and its team, avoiding collisions.
//
// Derived from the parent id rather than sequential, so a piece is traceable
// back to the task it came from in a log that shows only ids.
func nextPieceID(parent, team string, taken map[string]bool) string {
	base := strings.ToUpper(strings.TrimSpace(parent)) + "-" + strings.ToUpper(slug(team))
	if !taken[base] {
		return base
	}
	for n := 2; n < 100; n++ {
		candidate := fmt.Sprintf("%s%d", base, n)
		if !taken[candidate] {
			return candidate
		}
	}
	return base
}
