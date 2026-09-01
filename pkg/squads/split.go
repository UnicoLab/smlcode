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
//     two testers that each verify nothing.
//
// A task other tasks DEPEND ON is cut like any other, and every dependent is
// rewritten onto ALL of its pieces. That is not a guess about which piece they
// meant: the parent's work is exactly the union of its pieces, so waiting for
// all of them is precisely as strong as waiting for the parent was, and can
// never permit anything the original ordering forbade. Refusing here was
// measured to be the costly choice — the shape it protected is a first task
// that straddles and that everything else waits on, which then runs alone with
// no team while both lanes sit idle.
func SplitStraddlers(p *Plan, tasks []plan.Task) ([]plan.Task, string) {
	if p == nil || len(p.Squads) < 2 || len(tasks) == 0 {
		return tasks, ""
	}

	taken := map[string]bool{}
	for _, t := range tasks {
		taken[strings.ToUpper(strings.TrimSpace(t.ID))] = true
	}

	out := make([]plan.Task, 0, len(tasks))
	replaced := map[string][]string{}
	var cut []string
	for _, t := range tasks {
		byTeam, _, why := splitPlan(p, t)
		if byTeam == nil || why != "" {
			out = append(out, t)
			continue
		}
		// Plan order, so the backend's piece is always the backend's piece.
		var pieces []string
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
			pieces = append(pieces, piece.ID)
			out = append(out, piece)
		}
		replaced[key(t.ID)] = pieces
		cut = append(cut, t.ID)
	}
	if len(cut) == 0 {
		return tasks, ""
	}
	rewriteDependents(out, replaced)
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
func splitPlan(p *Plan, t plan.Task) (map[string][]string, bool, string) {
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
	want := key(id)
	for _, t := range tasks {
		if key(t.ID) != want {
			continue
		}
		_, straddles, why := splitPlan(p, t)
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

// rewriteDependents points every dependency at the pieces that replaced it.
//
// Waiting for ALL of a parent's pieces is exactly as strong as waiting for the
// parent was — the parent's work is the union of its pieces — so this preserves
// the ordering the planner wrote rather than reinterpreting it.
func rewriteDependents(tasks []plan.Task, replaced map[string][]string) {
	if len(replaced) == 0 {
		return
	}
	for i := range tasks {
		if len(tasks[i].DependsOn) == 0 {
			continue
		}
		changed := false
		out := make([]string, 0, len(tasks[i].DependsOn))
		seen := map[string]bool{}
		for _, d := range tasks[i].DependsOn {
			pieces, split := replaced[key(d)]
			if !split {
				if !seen[key(d)] {
					seen[key(d)] = true
					out = append(out, d)
				}
				continue
			}
			changed = true
			for _, piece := range pieces {
				// A piece never waits on itself: a cut task whose sibling
				// replaced a shared dependency would otherwise deadlock its own
				// wave.
				if piece == tasks[i].ID || seen[key(piece)] {
					continue
				}
				seen[key(piece)] = true
				out = append(out, piece)
			}
		}
		if changed {
			tasks[i].DependsOn = out
		}
	}
}
