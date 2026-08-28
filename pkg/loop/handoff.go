package loop

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
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

// reassignFailedTask hands an exhausted task to a different specialist.
//
// Returns the rewritten task and true when a handoff happened. False means the
// caller should escalate to a human — either the budget is spent or there is no
// second specialist to try, and pretending otherwise would park the task in
// ready_to_dev forever with nobody able to move it.
func (r *Runner) reassignFailedTask(t plan.Task, review plan.ReviewResult) (plan.Task, bool) {
	if r == nil || handoffCount(t) >= maxHandoffs {
		return t, false
	}
	next := r.alternateSpecialist(t)
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
	t.Description = strings.TrimRight(t.Description, "\n") + "\n\n---\n\n" + ticket.Description
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
