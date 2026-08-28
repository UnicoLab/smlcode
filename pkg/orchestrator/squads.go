package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// ── Virtual dev teams ────────────────────────────────────────────────────
//
// The manager phase turns a two-domain query ("a Go backend serving a React
// frontend") into two squads that build AT THE SAME TIME against an interface
// frozen before either starts.
//
// Everything here is non-fatal. Squads are an accelerator, never a
// prerequisite: a manager that fails, times out, or returns a plan that cannot
// be trusted leaves the run exactly as it was — one stream, one board. The one
// thing this must never do is activate a plan it could not validate, because an
// invalid plan (two squads owning one path) is strictly worse than no plan.

// squadRoleID is the manager specialist that assembles the org chart.
const squadRoleID = "manager"

// assembleSquads runs the manager and activates its plan when it holds up.
//
// Returns nil for "run single-stream", which is the correct answer for most
// queries and every failure mode here.
func (o *Orchestrator) assembleSquads(ctx context.Context, query string, inventory []string, exploreOut, archOut string) *squads.Plan {
	if o == nil || o.cfg == nil || o.factory == nil {
		return nil
	}

	o.emitAgent("charter", squadRoleID, "", "assembling parallel squads", "", "")

	input := o.buildSquadPrompt(query, inventory, exploreOut, archOut)
	out, err := o.runRoleTracked(ctx, squadRoleID, "", input)
	if err != nil {
		if ctx.Err() != nil {
			o.emitWarn("charter", "manager canceled — running as a single stream", "")
			return nil
		}
		o.emitWarn("charter", "manager failed ("+err.Error()+") — running as a single stream", "")
		return nil
	}

	p, err := squads.Parse(out)
	if err != nil {
		o.emitWarn("charter", "unparsable squad plan ("+err.Error()+") — running as a single stream", "")
		return nil
	}

	// One squad is the normal pipeline wearing a hat, and paying the contract +
	// integration overhead for it is pure cost. This is a routine, expected
	// answer for a single-domain query, so it is not a warning.
	if !p.Enabled() {
		o.emit("charter", "single-domain query — no squads assembled", "")
		return nil
	}

	problems := p.Validate()
	for _, pr := range problems {
		if pr.Severity == squads.SeverityWarn {
			o.emitWarn("charter", "squad plan: "+pr.Message, "")
		}
	}
	if problems.Errors() {
		// Refuse rather than repair. The dominant error is overlapping
		// ownership, and "fixing" it means silently deciding which team loses a
		// subtree — a decision that surfaces later as missing work nobody
		// ordered. Running one stream is the honest fallback.
		for _, pr := range problems {
			if pr.Severity == squads.SeverityError {
				o.emitWarn("charter", "squad plan rejected: "+pr.Message, "")
			}
		}
		return nil
	}

	// The contract goes to disk BEFORE any worker starts. Two squads running
	// concurrently cannot ask each other what the seam looks like, so it has to
	// be a file they both read — and one a human can correct between phases.
	if err := squads.Save(o.cfg.SlmDir(), p); err != nil {
		o.emitWarn("charter", "could not save the squad plan ("+err.Error()+") — running as a single stream", "")
		return nil
	}

	o.emit("charter", p.Summarize(), "")
	for _, s := range p.Squads {
		o.emitAgent("charter", squadRoleID, "", fmt.Sprintf("squad %s owns %s",
			s.ID, strings.Join(s.Owns, ", ")), "", "")
	}
	return &p
}

// buildSquadPrompt assembles the manager's input.
func (o *Orchestrator) buildSquadPrompt(query string, inventory []string, exploreOut, archOut string) string {
	var b strings.Builder
	b.WriteString("## Query\n" + strings.TrimSpace(query) + "\n")
	if len(inventory) > 0 {
		b.WriteString("\n## Workspace inventory\n")
		for _, f := range limitList(inventory, 60) {
			b.WriteString("- " + f + "\n")
		}
	}
	if s := strings.TrimSpace(archOut); s != "" {
		b.WriteString("\n## Approach\n" + truncate(s, 1200) + "\n")
	}
	if s := strings.TrimSpace(exploreOut); s != "" {
		b.WriteString("\n## Exploration notes\n" + truncate(s, 1200) + "\n")
	}
	return b.String()
}

// routeBoardToSquads stamps each task with its owning squad and reports the
// holes in the plan.
//
// Called after split, before execute. A task that straddles both squads or
// falls outside every squad keeps no squad and runs in the normal unassigned
// lane — see squads.AssignBoard for why guessing is worse than leaving it open.
func (o *Orchestrator) routeBoardToSquads(p *squads.Plan, board *plan.Board) {
	if p == nil || board == nil {
		return
	}
	rep := squads.AssignBoard(p, board.Tasks)
	o.emit("charter", "squad assignment: "+rep.Summary(), "")

	if len(rep.Straddling) > 0 {
		o.emitWarn("charter", fmt.Sprintf(
			"%d task(s) span both squads and stay unassigned (%s) — they are the seam and belong to integration",
			len(rep.Straddling), strings.Join(limitList(rep.Straddling, 6), ", ")), "")
	}
	if len(rep.Unowned) > 0 {
		o.emitWarn("charter", fmt.Sprintf(
			"%d task(s) touch files no squad owns (%s) — a hole in the squad plan, not in the task",
			len(rep.Unowned), strings.Join(limitList(rep.Unowned, 6), ", ")), "")
	}
	for _, id := range rep.Idle {
		o.emitWarn("charter", "squad "+id+" was assembled but has no work", "")
	}

	// Put the frozen seam in front of the reviewer, not just in the prompt.
	// Without this the contract is stated and then nothing checks it: a worker
	// that drifts from the spec produces a task the reviewer approves — it did
	// what its description said — and an integration failure much later with no
	// obvious owner.
	if n := squads.AttachContract(p, board.Tasks); n > 0 {
		o.emit("charter", fmt.Sprintf("contract attached as acceptance criteria on %d task(s)", n), "")
	}
}

// reportSquadProgress emits per-squad state and any cross-team stall.
//
// With two teams running, "12 of 20 done" hides that one squad finished long
// ago and the other has been blocked since. This runs between waves, which is
// where a manager would look.
func (o *Orchestrator) reportSquadProgress(p *squads.Plan, board *plan.Board) {
	if p == nil || board == nil {
		return
	}
	if line := squads.ProgressLine(p, board.Tasks); line != "" {
		o.emit("execute", "squads: "+line, "")
	}
	// A consumer stalled on an undelivered interface is not a task-level
	// failure, and retrying its tasks forever is the wrong response. Say so.
	for _, st := range squads.WaitingOn(p, board.Tasks) {
		o.emitWarn("execute", st.String()+" — this is a contract dependency, not a task defect", "")
	}
}

// limitList clips a list for display.
func limitList(in []string, n int) []string {
	if n <= 0 || len(in) <= n {
		return in
	}
	out := append([]string{}, in[:n]...)
	return append(out, fmt.Sprintf("+%d more", len(in)-n))
}

// runSquadIntegration runs the join step once every squad's half is green.
//
// This is the gate the whole structure is built to reach. Two squads can each
// be green with the assembled application still broken — that is precisely the
// failure a frozen contract is meant to prevent and integration is meant to
// catch when the contract was not enough. A run where both halves pass and this
// is red is a FAILED run, so a red result here is reported as loudly as a red
// QA gate rather than as a note.
//
// Returns true when integration ran and FAILED.
func (o *Orchestrator) runSquadIntegration(ctx context.Context, board *plan.Board) bool {
	if o == nil || o.squadPlan == nil || board == nil {
		return false
	}
	gate := squads.ReadyForIntegration(o.squadPlan, board.Tasks)
	if !gate.Ready {
		// Not an error: the board can end with a half unfinished for reasons
		// the rest of the pipeline already reported. Say why integration was
		// skipped rather than silently passing.
		o.emitWarn("integrate", "integration skipped — "+gate.Reason, "")
		return false
	}
	if strings.TrimSpace(gate.Command) == "" {
		o.emitWarn("integrate", "every squad is green, but the plan has no integration command — "+
			"the halves were never checked together", "")
		return false
	}

	o.emit("integrate", "every squad is green — joining the halves: "+gate.Command, "")
	res := o.runSmoke(ctx, gate.Command)
	if !res.Ran {
		o.emitWarn("integrate", "integration command did not run: "+res.Summary, "")
		return false
	}
	if res.OK {
		o.emit("integrate", "integration passed — the halves fit together", "")
		o.recordGate("integration", true, "")
		return false
	}
	o.emitWarn("integrate", "INTEGRATION FAILED — every squad is green but the assembled "+
		"application is not. The seam is wrong: "+res.Summary, res.Output)
	o.recordGate("integration", false, res.Summary)
	return true
}

// routeBoardToSpecialists assigns every task the specialist its own files call
// for, after squads have been assigned.
//
// The composer picks ONE language specialist per run. That is correct for a
// single-language repository and wrong for every task on the other side of a
// mixed one: in a Go API with a React SPA, a run-level `go-worker` is wrong for
// everything under web/. Routing per task fixes that with the strongest
// evidence available — the extensions of the files the task will actually edit.
//
// Runs whether or not squads are active: a mixed-language repo does not need
// two teams to need two specialists.
func (o *Orchestrator) routeBoardToSpecialists(board *plan.Board) {
	if o == nil || board == nil || o.factory == nil {
		return
	}
	policy := plan.RoutePolicy{
		Available:       o.factory.HasRole,
		DefaultWorker:   o.Pipeline().Execute.DefaultRole,
		DefaultReviewer: o.Pipeline().Execute.Reviewer,
		DefaultTester:   o.Pipeline().Execute.Corrector,
		SquadWorker:     o.squadWorkerLookup(),
	}
	tally, byTask := plan.RouteBoard(board.Tasks, policy)

	changed := 0
	for _, r := range byTask {
		if r.Changed {
			changed++
		}
	}
	if changed == 0 {
		return
	}
	o.emit("split", fmt.Sprintf("routed %d task(s) to language specialists: %s",
		changed, plan.TallyLine(tally)), "")
	// One line per reroute, so a surprising choice is auditable rather than
	// mysterious. Bounded: a 40-task board should not produce 40 events.
	shown := 0
	for _, t := range board.Tasks {
		r := byTask[t.ID]
		if !r.Changed || shown >= 8 {
			continue
		}
		o.emitAgent("split", r.Role, t.ID, "assigned "+r.Role+" — "+r.Reason, "", "")
		shown++
	}
}

// squadWorkerLookup exposes the manager's per-squad worker choice to routing.
func (o *Orchestrator) squadWorkerLookup() func(string) string {
	if o == nil || o.squadPlan == nil {
		return nil
	}
	plan := o.squadPlan
	return func(id string) string {
		s, ok := plan.Squad(id)
		if !ok {
			return ""
		}
		return s.Worker
	}
}

// squadsAskView renders the org chart for the approval card.
func (o *Orchestrator) squadsAskView(board *plan.Board) *plan.PlanSquads {
	if o == nil || o.squadPlan == nil || len(o.squadPlan.Squads) == 0 {
		return nil
	}
	counts := map[string]int{}
	if board != nil {
		for _, t := range board.Tasks {
			if t.Squad != "" {
				counts[t.Squad]++
			}
		}
	}
	view := &plan.PlanSquads{
		Summary:     o.squadPlan.Summary,
		Integration: o.squadPlan.Integration.Acceptance,
	}
	for _, s := range o.squadPlan.Squads {
		view.Squads = append(view.Squads, plan.PlanSquad{
			ID: s.ID, Name: s.Name, Charter: s.Charter, Owns: s.Owns,
			Acceptance: s.Acceptance, Worker: s.Worker, Reviewer: s.Reviewer,
			TaskCount: counts[s.ID],
		})
	}
	for _, in := range o.squadPlan.Contract.Interfaces {
		view.Interfaces = append(view.Interfaces, plan.PlanInterface{
			ID: in.ID, Provider: in.Provider, Consumers: in.Consumers, Spec: in.Spec,
		})
	}
	return view
}

// staffableAgents lists the agent ids this run can actually dispatch.
//
// The approval UI offers these as the role choices. A UI with a hardcoded list
// would let a user pick an agent the harness cannot staff, and the only symptom
// of that is a task that never starts.
func (o *Orchestrator) staffableAgents() []string {
	if o == nil || o.factory == nil {
		return nil
	}
	var out []string
	for _, spec := range o.factory.AllSpecs() {
		out = append(out, spec.ID)
	}
	sort.Strings(out)
	return out
}

// applyPlanEdits applies a human's approval-time edits to the board and the
// org chart.
//
// Task edits and squad edits are applied independently on purpose: a squad edit
// that fails ownership validation is refused whole (see squads.ApplyEdits), and
// that must not silently discard the task edits made in the same pass, which
// are unrelated and individually valid.
func (o *Orchestrator) applyPlanEdits(board *plan.Board, edits *plan.PlanEdits) {
	if o == nil || edits == nil || edits.Empty() || board == nil {
		return
	}
	var roleExists func(string) bool
	if o.factory != nil {
		roleExists = o.factory.HasRole
	}

	if problems := plan.ApplyTaskEdits(board, *edits, roleExists); len(problems) > 0 {
		for _, p := range problems {
			o.emitWarn("plan", "plan edit: "+p, "")
		}
	}

	if len(edits.Squads) > 0 || len(edits.RemoveSquads) > 0 {
		if probs := squads.ApplyEdits(o.squadPlan, edits.Squads, edits.RemoveSquads); probs.Errors() {
			// Refused whole. Say so loudly: the user believes they fixed the
			// org chart, and running the model's version without telling them
			// is the worst of the three options.
			for _, p := range probs {
				o.emitWarn("plan", "squad edit REFUSED: "+p.Message, "")
			}
		} else {
			if err := squads.Save(o.cfg.SlmDir(), *o.squadPlan); err != nil {
				o.emitWarn("plan", "could not save the edited squad plan: "+err.Error(), "")
			}
			o.emit("plan", "squad plan edited: "+o.squadPlan.Summarize(), "")
			// Ownership moved, so who owns which task may have moved with it.
			o.routeBoardToSquads(o.squadPlan, board)
		}
	}

	// Roles may have changed by hand; re-route only what the user did not pin.
	o.routeBoardToSpecialists(board)
	o.persistBoard(board)
	o.emit("plan", fmt.Sprintf("applied plan edits — %d task(s) on the board", len(board.Tasks)), "")
}
