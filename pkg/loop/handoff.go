package loop

import (
	"context"
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// ── Reassignment before escalation ───────────────────────────────────────
//
// When the review ladder runs out of retries, the task used to go straight to
// `to_scope` with "needs human input or smaller scope" and a red intervention
// event. On a long run that is the notification the user sees over and over,
// and it asks them to do the one thing they cannot easily do: work out, from a
// parked task, what the agent should have done differently.
//
// The failing agent has already had every retry the ladder allows, so handing
// it the SAME work again is the definition of the loop that produced those
// notifications. Handing it to a DIFFERENT specialist, with the failure ledger
// as context, is what a team lead does — and it is what the reviewer's evidence
// was collected for.
//
// A human is still the last resort. This buys exactly one attempt by another
// pair of hands before asking for one.

// maxHandoffs bounds reassignment per task.
//
// One. A second handoff would be a third agent guessing at work two others
// could not do, which is not a staffing problem any more — it is a scoping
// problem, and that is precisely what a human is being asked to look at.
const maxHandoffs = 1

// handoffMarker records a reassignment in the task notes, so the count survives
// persistence and a resumed run does not restart the budget.
const handoffMarker = "reassigned-to: "

// handoffCount is how many times this task has already changed hands.
func handoffCount(t plan.Task) int {
	return strings.Count(t.Notes, handoffMarker)
}

// TriageRequest is what the project manager is asked to decide about.
type TriageRequest struct {
	Task   plan.Task
	Review plan.ReviewResult
	// Roster is who may be picked, the task's own team first.
	Roster   []string
	Language string
	// Staffing names the team this task belongs to and who runs it. Empty
	// Squad means an unassigned task, which is most of them on a single-stream
	// run — the manager then answers for the run rather than for a team.
	Staffing squads.Staffing
}

// reassignFailedTask hands an exhausted task to a different specialist.
//
// The manager decides when one is wired; the deterministic ladder is the
// fallback, not the default. A ladder answers "who else COULD hold this"; a
// manager answers "who should, and what do they need to know that the last
// attempt did not" — and the second half is what changes the outcome, because
// an agent handed identical context makes an identical attempt.
//
// Returns the rewritten task and true when a handoff happened. False means the
// caller should escalate to a human — either the budget is spent or there is no
// second specialist to try, and pretending otherwise would park the task in
// ready_to_dev forever with nobody able to move it.
func (r *Runner) reassignFailedTask(ctx context.Context, t plan.Task, review plan.ReviewResult) (plan.Task, bool) {
	if r == nil || handoffCount(t) >= maxHandoffs {
		return t, false
	}
	next, guidance := r.decideNextHolder(ctx, t, review)
	if next == "" {
		return t, false
	}

	failures := review.Issues
	if len(failures) == 0 && strings.TrimSpace(review.Summary) != "" {
		failures = []string{review.Summary}
	}
	ticket := plan.NewCorrectionTicket(plan.CorrectionInput{
		Source:   plan.SourceReviewer,
		Failures: failures,
		Summary:  review.Summary,
		Files:    t.Files,
		Squad:    t.Squad,
		Origin:   t.ID,
		// Every retry the ladder allowed has already been spent, so the next
		// agent must be told not to repeat them.
		Attempt: t.Retries,
	}, r.roleExists)

	// The task keeps its identity, its files and its acceptance — this is a
	// change of hands, not a new piece of work. Only who holds it and what they
	// have been told changes.
	t.Role = next
	body := ticket.Description
	if guidance != "" {
		// The manager's direction goes ABOVE the failure dump: it is the one
		// thing here the previous attempt did not already have, and burying it
		// under the evidence is how it gets skimmed past.
		body = "## From the project manager\n\n" + guidance + "\n\n" + body
	}
	t.Description = strings.TrimRight(t.Description, "\n") + "\n\n---\n\n" + body
	t.MoveTo(plan.ColReadyToDev)
	t.Status = plan.StatusReady
	t.Retries = 0
	t.Error = ""
	t.Notes = strings.TrimSpace(t.Notes + "\n" + handoffMarker + next +
		" (the previous specialist exhausted its retries)")
	return t, true
}

// alternateSpecialist picks a different agent to hand the work to.
//
// Prefers the corrector for the task's language — the role whose entire prompt
// is "somebody else's code is failing, fix it" — then the generic corrector,
// then the generic worker. Never returns the role that just failed.
func (r *Runner) alternateSpecialist(t plan.Task) string {
	current := strings.ToLower(strings.TrimSpace(t.Role))
	candidates := make([]string, 0, 4)
	if lang := plan.LanguageOf(t.Files); lang != "" {
		candidates = append(candidates, lang+"-corrector", lang+"-worker")
	}
	candidates = append(candidates, r.CorrectorRole, plan.RoleCorrector, plan.RoleWorker)
	for _, c := range candidates {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" || c == current || !r.roleExists(c) {
			continue
		}
		return c
	}
	return ""
}

// roleExists reports whether an agent id is registered.
//
// Nil RoleExists means the caller did not wire a registry, and inventing an
// agent id would fail to dispatch — worse than escalating to a human, which at
// least tells somebody. So the answer is no.
func (r *Runner) roleExists(id string) bool {
	if r == nil || r.RoleExists == nil {
		return false
	}
	return r.RoleExists(id)
}

// announceHandoff reports a reassignment as staffing, not as a failure.
func (r *Runner) announceHandoff(t plan.Task, from string) {
	r.fireLevel(stream.KindOutput, t.Role, t.ID,
		fmt.Sprintf("%s reassigned from %s to %s after its retries were spent", t.ID, from, t.Role),
		strings.Join(t.Files, ", "), "", stream.LevelInfo)
}

// decideNextHolder asks the manager, falling back to the deterministic ladder.
//
// The manager's answer is VALIDATED before it is used. Two failure modes are
// worse than no manager at all: naming an agent that cannot be dispatched
// (the task then sits unassigned forever) and re-picking the agent that just
// failed (the loop triage exists to end). Either one falls through to the
// ladder rather than being applied.
func (r *Runner) decideNextHolder(ctx context.Context, t plan.Task, review plan.ReviewResult) (role, guidance string) {
	if r.Triage != nil {
		staff := squads.StaffingFor(r.Squads, t.Squad)
		req := TriageRequest{
			Task:     t,
			Review:   review,
			Roster:   squads.Colleagues(r.Squads, t.Squad, r.roster()),
			Language: plan.LanguageOf(t.Files),
			Staffing: staff,
		}
		if d, ok := r.Triage(ctx, req); ok {
			if usable, why := d.Usable(t.Role, r.RoleExists); usable {
				// The prompt asks for a specialist over a generic; asking is
				// not enough. A generic corrector handed a failing Go handler
				// brings nothing the generic worker that already failed did.
				assignee := d.Assignee
				if upgraded, changed := plan.PreferSpecialist(assignee, t.Role, t.Files, req.Roster); changed {
					r.logf("%s: %s over the generic %s", t.ID, upgraded, assignee)
					assignee = upgraded
				}
				r.logf("%s triaged to %s: %s", t.ID, assignee, d.Reason)
				return assignee, d.Guidance
			} else {
				r.logf("%s triage ignored — the manager %s; using the deterministic ladder", t.ID, why)
			}
		}
	}
	return r.alternateSpecialist(t), ""
}

// roster is the agent ids the manager may choose from.
//
// Built from the same predicate that gates the answer, so the manager cannot be
// offered a choice that would then be refused.
func (r *Runner) roster() []string {
	if r == nil || r.RoleExists == nil {
		return nil
	}
	var out []string
	for _, id := range r.RosterIDs {
		if r.RoleExists(id) {
			out = append(out, id)
		}
	}
	return out
}
