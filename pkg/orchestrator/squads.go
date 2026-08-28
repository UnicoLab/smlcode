package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/stream"
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
	// A defect, on the record. Without this the run summary reads "0 failed"
	// over a broken application and the Fixes tab shows nothing — the exact
	// silence that makes a user stop trusting either.
	o.emitLoop("integrate", LoopEvent{
		Action:   "integration_failed",
		Reason:   "every squad is green but the halves do not fit together",
		Failures: trimFailures([]string{firstSentence(res.Summary)}, 3),
		From:     "integrate", To: "plan",
	})
	o.raiseIntegrationTicket(board, gate.Command, res.Summary, res.Output)
	return true
}

// raiseIntegrationTicket turns a failed join into a ticket somebody owns.
//
// This is the defect the whole squads design exists to catch — every half green
// and the assembled application broken — and it used to arrive as the least
// useful ticket the harness can produce. The QA path raised it with a synthetic
// verdict reading `qa_gate command still failing` / `qa_gate red`, so the
// command that failed, the output naming the seam, the interfaces at stake and
// the team that owes them were all discarded. What landed was a generic
// `worker` task with no files, owned by nobody, whose entire context was that a
// gate was red.
//
// The contract knows who owes each clause, so the ticket goes to that team with
// the evidence attached.
func (o *Orchestrator) raiseIntegrationTicket(board *plan.Board, cmd, summary, output string) {
	if o == nil || board == nil {
		return
	}
	squad, clauses := squads.SeamOwner(o.squadPlan, output)
	failures := []string{"the halves do not fit together: " + firstSentence(summary)}
	for _, c := range clauses {
		failures = append(failures, "contract clause at stake: "+c)
	}

	in := plan.CorrectionInput{
		Source:   plan.SourceIntegration,
		Failures: failures,
		Summary:  summary,
		Command:  cmd,
		Output:   output,
		// Files the integration output actually named, kept to the owing team's
		// lane: a bundler error listing half the tree would otherwise scope the
		// ticket to everything.
		Files: o.seamFiles(output, squad),
		Squad: squad,
	}
	key := plan.CorrectionKey(in)
	in.Attempt = board.CorrectionAttempts(key)
	if board.NoteRepeatedRejection(key) > 0 {
		o.persistBoard(board)
		return
	}
	var hasRole func(string) bool
	if o.factory != nil {
		hasRole = o.factory.HasRole
	}
	ticket := plan.NewCorrectionTicket(in, hasRole)
	plan.StampCorrectionKey(&ticket, key)
	plan.StampCorrectionAttempt(&ticket, in.Attempt+1)
	board.AddTask(ticket)
	o.persistBoard(board)

	where := "unassigned"
	if squad != "" {
		where = squad
	}
	o.emit("integrate", fmt.Sprintf("raised an integration ticket for %s → %s", where, ticket.Role), ticket.ID)
}

// seamFiles is the paths the integration output named, narrowed to one team's
// lane when a team owes the seam.
func (o *Orchestrator) seamFiles(output, squad string) []string {
	named := squads.PathsIn(output)
	if squad == "" || o.squadPlan == nil {
		return limitList(named, 8)
	}
	var mine []string
	for _, f := range named {
		if owner, ok := o.squadPlan.Owner(f); ok && owner == squad {
			mine = append(mine, f)
		}
	}
	if len(mine) == 0 {
		// The output named nothing in this team's lane. Better an unscoped
		// ticket with the evidence than one scoped to the other half's files.
		return nil
	}
	return limitList(mine, 8)
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
			Manager: s.Manager, TaskCount: counts[s.ID],
		})
	}
	for _, in := range o.squadPlan.Contract.Interfaces {
		view.Interfaces = append(view.Interfaces, plan.PlanInterface{
			ID: in.ID, Provider: in.Provider, Consumers: in.Consumers, Spec: in.Spec,
		})
	}
	return view
}

// manageableAgents lists the agents a team may be given as its project manager.
//
// Narrower than staffableAgents, and for a reason the UI cannot infer: the
// decoding grammar for a request is derived from the agent's own system prompt,
// so an agent that does not answer the triage contract replies with something
// the reassignment step cannot read — after a full model call has been spent.
func (o *Orchestrator) manageableAgents() []string {
	if o == nil || o.factory == nil {
		return nil
	}
	var out []string
	for _, spec := range o.factory.AllSpecs() {
		if strings.EqualFold(spec.SchemaRole, schema.RoleTriage) {
			out = append(out, spec.ID)
		}
	}
	sort.Strings(out)
	return out
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

// triageRejectedDelivery asks the project manager who should take a rejected
// task next, and what they need to know that the last attempt did not.
//
// Non-fatal in every direction. A manager that fails, times out or answers with
// something unusable leaves the deterministic ladder in charge — the loop
// validates the answer before applying it, so a bad verdict costs one call, not
// a stalled task.
func (o *Orchestrator) triageRejectedDelivery(ctx context.Context, req loop.TriageRequest) (plan.TriageDecision, bool) {
	// No executor means nothing can be dispatched at all. Triage is an
	// accelerator over the deterministic ladder, never a prerequisite, so a
	// half-built orchestrator declines the question instead of crashing on it.
	if o == nil || o.factory == nil || o.executor == nil || len(req.Roster) == 0 {
		return plan.TriageDecision{}, false
	}
	t := req.Task

	var b strings.Builder
	// Who is being asked. A run-wide manager answering for a specific team
	// picks from a roster it has no reason to understand; saying which team
	// this is, and who staffs it, is what makes "prefer your own people" a
	// decision the model can actually make.
	if st := req.Staffing; st.Squad != "" {
		fmt.Fprintf(&b, "## You are the project manager for the %s team\n", st.Squad)
		if len(st.Members) > 0 {
			fmt.Fprintf(&b, "Your team is staffed with: %s\n", strings.Join(st.Members, ", "))
		}
		b.WriteString("Prefer your own people. Reach outside the team only when the fix needs a skill it does not have.\n\n")
	}

	b.WriteString("## Rejected task\n")
	fmt.Fprintf(&b, "%s — %s\n", t.ID, t.Title)
	if t.Squad != "" {
		fmt.Fprintf(&b, "squad: %s\n", t.Squad)
	}
	fmt.Fprintf(&b, "held by: %s\n", t.Role)
	if req.Language != "" {
		fmt.Fprintf(&b, "language: %s\n", req.Language)
	}
	if len(t.Files) > 0 {
		fmt.Fprintf(&b, "files: %s\n", strings.Join(t.Files, ", "))
	}
	if s := strings.TrimSpace(t.Acceptance); s != "" {
		fmt.Fprintf(&b, "acceptance: %s\n", truncate(s, 300))
	}

	b.WriteString("\n## What the reviewer found\n")
	if s := strings.TrimSpace(req.Review.Summary); s != "" {
		b.WriteString(s + "\n")
	}
	for _, issue := range limitList(req.Review.Issues, 6) {
		b.WriteString("- " + issue + "\n")
	}

	// The attempt ledger is the whole reason a manager can do better than a
	// ladder: it is what says "this was already tried".
	if len(t.AttemptLog) > 0 {
		b.WriteString("\n## Attempts already made — do not repeat these\n")
		for _, a := range limitList(t.AttemptLog, 6) {
			b.WriteString("- " + truncate(a, 240) + "\n")
		}
	}

	// Ranked and labeled, not alphabetical. Sorted by name the generics sit at
	// the top of the list the model reads first, and it takes one — even though
	// the prompt told it to prefer a specialist for the language of the files.
	b.WriteString("\n## ROSTER — pick exactly one of these, best fit first\n")
	for _, a := range plan.RankRoster(req.Roster, t.Files) {
		b.WriteString("- " + a.ID + " (" + a.Note + ")\n")
	}

	// A team may name its own manager; the run's `triage` agent answers for
	// the teams that did not. The nominee has to actually answer the triage
	// contract — see Factory.EmitsSchema for why existing is not enough.
	manager := agents.RoleTriage
	if m := strings.TrimSpace(req.Staffing.Manager); m != "" && o.factory.EmitsSchema(m, schema.RoleTriage) {
		manager = m
	}

	out, err := o.runRoleTracked(ctx, manager, "", b.String())
	if err != nil {
		if ctx.Err() == nil {
			o.emitWarn("coord", manager+" failed ("+err.Error()+") — using the deterministic handoff", "")
		}
		return plan.TriageDecision{}, false
	}
	d, err := plan.ParseTriage(out)
	if err != nil {
		o.emitWarn("coord", "unreadable triage verdict — using the deterministic handoff", "")
		return plan.TriageDecision{}, false
	}
	// A verdict is a PROPOSAL. Announcing it as a reassignment here put
	// "T1 reassigned to go-corrector" on the stream immediately before
	// "triage ignored — not a registered agent", so a watching human saw a
	// handoff that never happened. Both callers validate the verdict and
	// announce the one they actually applied.
	o.emitFull("coord", stream.KindDebug, manager, t.ID,
		fmt.Sprintf("%s proposes %s — %s", manager, d.Assignee, d.Reason), "", "")
	return d, true
}

// reassignedMarker records a manager handoff in a ticket's notes, so the
// one-handoff budget survives persistence and a resumed run does not restart
// it. Same marker the review ladder writes — see pkg/loop/handoff.go.
const reassignedMarker = "reassigned-to: "

// triageRepeatTickets sends a SECOND attempt at the same defect past the
// project manager before it goes back to work.
//
// The tester path routes tickets by language: a failing .go file goes to
// go-worker, a failing .tsx to react-worker. That is the right first answer and
// the wrong second one. When the same defect comes back, the deterministic
// route hands it to the agent that just failed at it, with a ticket whose only
// new content is that it failed again — which is the loop that made tester
// failures feel like noise rather than progress.
//
// A manager breaks it, and can do the two things the router cannot: pick
// somebody else, and say what to do differently. Everything here is best
// effort — no manager, an unreadable verdict, or a verdict naming an agent that
// cannot be dispatched all leave the ticket exactly as the router left it,
// which still works.
func (o *Orchestrator) triageRepeatTickets(ctx context.Context, board *plan.Board) int {
	if o == nil || board == nil || o.factory == nil {
		return 0
	}
	roster := o.triageRoster()
	if len(roster) == 0 {
		return 0
	}

	moved := 0
	for i := range board.Tasks {
		t := board.Tasks[i]
		t.Normalize()
		// The stamped attempt, not a count of tickets: a defect re-reported
		// while its ticket is still open produces no second task — dedupe is
		// doing its job — so counting tickets would call the third rejection of
		// one defect a first attempt.
		if plan.CorrectionKeyOf(t) == "" || t.Column != plan.ColReadyToDev ||
			plan.CorrectionAttemptOf(t) < 2 {
			continue
		}
		// One handoff per ticket, the same budget the review ladder uses. A
		// second manager verdict would be a third agent guessing at work two
		// others could not do, which is not a staffing problem any more — it is
		// a scoping problem, and that is what a human is being asked to see.
		if strings.Contains(t.Notes, reassignedMarker) {
			continue
		}
		staff := squads.StaffingFor(o.squadPlan, t.Squad)
		d, ok := o.triageRejectedDelivery(ctx, loop.TriageRequest{
			Task: t,
			// The ticket body IS the review here: the tester's findings were
			// written into it when it was raised, and re-deriving a summary
			// from the board would only lose detail.
			Review:   plan.ReviewResult{Summary: ticketHeadline(t), Issues: ticketFindings(t)},
			Roster:   squads.Colleagues(o.squadPlan, t.Squad, roster),
			Language: plan.LanguageOf(t.Files),
			Staffing: staff,
		})
		if !ok {
			continue
		}
		if usable, why := d.Usable(t.Role, o.factory.HasRole); !usable {
			o.emitWarn("plan", t.ID+" triage ignored — the manager "+why, "")
			continue
		}
		assignee := d.Assignee
		if upgraded, changed := plan.PreferSpecialist(assignee, t.Role, t.Files, roster); changed {
			o.emitFull("plan", stream.KindDebug, upgraded, t.ID,
				fmt.Sprintf("%s: %s over the generic %s for %s", t.ID, upgraded, assignee,
					plan.LanguageOf(t.Files)), "", "")
			assignee = upgraded
		}
		from := t.Role
		t.Role = assignee
		if g := strings.TrimSpace(d.Guidance); g != "" {
			// Above the evidence, not below it: the guidance is the only thing
			// here the last attempt did not already have.
			t.Description = "## From the project manager\n\n" + g + "\n\n---\n\n" +
				strings.TrimLeft(t.Description, "\n")
		}
		t.Notes = strings.TrimSpace(t.Notes + "\n" + reassignedMarker + assignee +
			" (repeat ticket, routed by the project manager)")
		board.Tasks[i] = t
		moved++
		o.emitFull("plan", stream.KindOutput, assignee, t.ID,
			fmt.Sprintf("%s reassigned from %s to %s — %s", t.ID, from, assignee, d.Reason),
			strings.Join(t.Files, ", "), "")
	}
	return moved
}

// ticketHeadline is a correction ticket's one-line verdict, for a manager that
// needs to know what is failing without being handed the whole body.
func ticketHeadline(t plan.Task) string {
	if s := strings.TrimSpace(t.Review); s != "" {
		return s
	}
	return strings.TrimSpace(t.Title)
}

// ticketFindings pulls the bullet list out of a ticket's "What failed" section.
func ticketFindings(t plan.Task) []string {
	body := t.Description
	i := strings.Index(body, "## What failed")
	if i < 0 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(body[i:], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") && !strings.HasPrefix(line, "## What failed") {
			break
		}
		if after, found := strings.CutPrefix(line, "- "); found {
			out = append(out, strings.TrimSpace(after))
		}
	}
	return limitList(out, 6)
}

// triageRoster is the set of agents the project manager may choose from.
//
// Implementers only: triage decides who WRITES the fix, and offering a reviewer
// or a planner invites an answer the loop would then refuse.
func (o *Orchestrator) triageRoster() []string {
	if o == nil || o.factory == nil {
		return nil
	}
	var out []string
	for _, spec := range o.factory.AllSpecs() {
		id := strings.ToLower(spec.ID)
		if strings.HasSuffix(id, "-worker") || strings.HasSuffix(id, "-corrector") ||
			id == plan.RoleWorker || id == plan.RoleCorrector {
			out = append(out, spec.ID)
		}
	}
	sort.Strings(out)
	return out
}
