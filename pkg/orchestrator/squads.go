package orchestrator

import (
	"context"
	"fmt"
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
