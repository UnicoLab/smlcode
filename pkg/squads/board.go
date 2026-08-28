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
	// A squad is "present" when the wave contains work that may legitimately
	// write in its lane, and a present squad's paths are never denied.
	present := map[string]bool{}
	for _, t := range wave {
		if t.Squad != "" {
			present[t.Squad] = true
			continue
		}
		// An unassigned task has no declared lane. It used to disable the deny
		// list for the WHOLE wave, which dropped ownership enforcement far more
		// often than it looks: a task is unassigned whenever it straddles two
		// teams AND whenever nothing owns its files at all — a README, a
		// Makefile, a top-level config. One such task in a wave and neither
		// team was fenced from the other any more.
		//
		// Its declared files say which lanes it actually needs. A seam task
		// naming `web/src/api.ts` opens the frontend's lane and nothing else; a
		// task naming only `README.md` opens nothing, and both teams stay
		// fenced.
		//
		// A task that declared NO files is the one case where the old blanket
		// stand-down is still right: declared files are a task's scope, and one
		// with no scope could write anywhere. Fencing it would block work the
		// harness cannot show is out of bounds, so nothing is denied.
		if len(t.Files) == 0 {
			return nil
		}
		for _, f := range t.Files {
			if owner, ok := p.Owner(f); ok {
				present[owner] = true
			}
		}
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

// ── Contract enforcement ─────────────────────────────────────────────────

// maxContractCriteria bounds how many contract clauses one task carries.
//
// plan.MaxCriteria caps a task at eight, and a task's OWN acceptance conditions
// have first claim on that budget: a worker whose criteria list is entirely
// contract clauses has been told what the seam is and nothing about its job.
const maxContractCriteria = 3

// AttachContract turns the frozen interfaces into verifiable acceptance
// criteria on the tasks that owe or consume them.
//
// Without this the contract is present in the prompt and absent from the gates:
// the seam is stated, and then nothing checks it. A worker that drifts from the
// spec produces a task the reviewer approves — it did what its description
// said — and an integration step that fails much later with no obvious owner.
//
// As criteria they are judged per task, by the reviewer, at the moment the work
// is done. The wording differs by side because the obligations differ: a
// provider must MATCH the spec, a consumer must BUILD AGAINST it and may not
// have anything to call yet.
//
// Returns how many tasks were given at least one contract criterion.
func AttachContract(p *Plan, tasks []plan.Task) int {
	if p == nil || len(p.Contract.Interfaces) == 0 {
		return 0
	}
	touched := 0
	for i := range tasks {
		squad := strings.TrimSpace(tasks[i].Squad)
		if squad == "" {
			continue
		}
		// Only implementers are judged against the seam. A tester task's job is
		// to run the acceptance, not to re-assert the contract.
		if !isImplementer(tasks[i].Role) {
			continue
		}
		provides, consumes := p.interfacesFor(squad)
		added := contractCriteria(provides, consumes)
		// Idempotent: routing runs once per run, but a RESUMED run replays it
		// over a board that already carries these clauses, and duplicates would
		// eat the criteria budget a clause at a time until the task's own
		// conditions were pushed out entirely.
		added = withoutExisting(tasks[i].Criteria, added)
		if len(added) == 0 {
			continue
		}
		// The task's own conditions keep their place at the head of the list;
		// plan.NormalizeCriteria then caps the whole thing at MaxCriteria, so
		// an over-full task drops contract clauses rather than its own work.
		tasks[i].Criteria = plan.NormalizeCriteria(append(tasks[i].Criteria, added...))
		touched++
	}
	return touched
}

// contractCriteria renders the clauses for one squad, provider side first.
func contractCriteria(provides, consumes []Interface) []plan.Criterion {
	out := make([]plan.Criterion, 0, maxContractCriteria)
	for _, in := range provides {
		if len(out) >= maxContractCriteria {
			break
		}
		out = append(out, plan.Criterion{
			Text: "Matches the frozen contract for " + in.ID + specSuffix(in) +
				". Another squad is building against this right now.",
			Priority: plan.PriorityMust,
		})
	}
	for _, in := range consumes {
		if len(out) >= maxContractCriteria {
			break
		}
		out = append(out, plan.Criterion{
			// A consumer criterion must not demand a live endpoint: the
			// provider may not have written it yet, and failing the consumer
			// for that would penalize it for being on time.
			Text: "Calls " + in.ID + specSuffix(in) +
				" exactly as the frozen contract states, whether or not it exists on disk yet.",
			Priority: plan.PriorityMust,
		})
	}
	return out
}

func specSuffix(in Interface) string {
	spec := strings.TrimSpace(strings.ReplaceAll(in.Spec, "\n", " "))
	if spec == "" {
		return ""
	}
	return " (" + spec + ")"
}

// isImplementer reports whether a role writes code.
func isImplementer(role string) bool { return plan.IsImplementerRole(role) }

// withoutExisting drops clauses the task already carries, matched on text.
func withoutExisting(have, add []plan.Criterion) []plan.Criterion {
	if len(have) == 0 {
		return add
	}
	seen := make(map[string]bool, len(have))
	for _, c := range have {
		seen[strings.TrimSpace(c.Text)] = true
	}
	out := make([]plan.Criterion, 0, len(add))
	for _, c := range add {
		if !seen[strings.TrimSpace(c.Text)] {
			out = append(out, c)
		}
	}
	return out
}

// ── Editing the org chart ────────────────────────────────────────────────

// ApplyEdits applies human edits to a squad plan and re-validates it.
//
// Re-validation is not optional and not advisory. The one rule squads rest on
// is disjoint ownership, and a human editing `owns` by hand is at least as
// likely to overlap two teams as a model is. If the edited plan does not
// validate, NOTHING is applied: a half-applied org chart is worse than the one
// the model proposed, because the user believes they fixed it.
//
// Returns the problems found. An empty result means the edits were applied.
func ApplyEdits(p *Plan, edits []plan.SquadEdit, removes []string) Problems {
	if p == nil {
		return Problems{{Severity: SeverityError, Message: "no squad plan to edit"}}
	}
	if len(edits) == 0 && len(removes) == 0 {
		return nil
	}

	// Work on a copy so a rejected edit set leaves the live plan untouched.
	next := p.clone()

	gone := map[string]bool{}
	for _, id := range removes {
		gone[slug(id)] = true
	}
	if len(gone) > 0 {
		kept := make([]Squad, 0, len(next.Squads))
		for _, s := range next.Squads {
			if !gone[s.ID] {
				kept = append(kept, s)
			}
		}
		next.Squads = kept
		// An interface whose provider no longer exists is a clause nobody owes.
		ifaces := make([]Interface, 0, len(next.Contract.Interfaces))
		for _, in := range next.Contract.Interfaces {
			if gone[in.Provider] {
				continue
			}
			cons := make([]string, 0, len(in.Consumers))
			for _, c := range in.Consumers {
				if !gone[c] {
					cons = append(cons, c)
				}
			}
			in.Consumers = cons
			ifaces = append(ifaces, in)
		}
		next.Contract.Interfaces = ifaces
	}

	index := map[string]int{}
	for i, s := range next.Squads {
		index[s.ID] = i
	}
	for _, e := range edits {
		id := slug(e.ID)
		if id == "" {
			continue
		}
		i, ok := index[id]
		if !ok {
			if !e.New {
				return Problems{{Severity: SeverityError, Squad: e.ID,
					Message: "no such squad in this plan"}}
			}
			next.Squads = append(next.Squads, Squad{ID: id})
			i = len(next.Squads) - 1
			index[id] = i
		}
		s := next.Squads[i]
		if e.Name != nil {
			s.Name = strings.TrimSpace(*e.Name)
		}
		if e.Charter != nil {
			s.Charter = strings.TrimSpace(*e.Charter)
		}
		if e.Acceptance != nil {
			s.Acceptance = strings.TrimSpace(*e.Acceptance)
		}
		if e.Worker != nil {
			s.Worker = strings.TrimSpace(*e.Worker)
		}
		if e.Reviewer != nil {
			s.Reviewer = strings.TrimSpace(*e.Reviewer)
		}
		if e.Manager != nil {
			s.Manager = strings.TrimSpace(*e.Manager)
		}
		if e.OwnsSet {
			s.Owns = e.Owns
		}
		next.Squads[i] = s
	}

	next.Normalize()
	if problems := next.Validate(); problems.Errors() {
		return problems
	}
	*p = next
	return nil
}

// clone deep-copies a plan so a rejected edit set cannot leave the live one
// half-modified through a shared backing array.
func (p *Plan) clone() Plan {
	out := *p
	out.Squads = make([]Squad, len(p.Squads))
	for i, s := range p.Squads {
		s.Owns = append([]string(nil), s.Owns...)
		s.Skills = append([]string(nil), s.Skills...)
		out.Squads[i] = s
	}
	out.Contract.Interfaces = make([]Interface, len(p.Contract.Interfaces))
	for i, in := range p.Contract.Interfaces {
		in.Consumers = append([]string(nil), in.Consumers...)
		out.Contract.Interfaces[i] = in
	}
	out.Integration.Notes = append([]string(nil), p.Integration.Notes...)
	return out
}
