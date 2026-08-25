package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// These are the regression guards for the SECOND cause of the live
// `review approved=false score=0` loop (the first was clipForReview — see
// reviewclip_test.go). Every one of them fails on the code that shipped, and
// every one of them is written against the pre-existing API on purpose, so it
// can be checked out against the old tree and shown failing.
//
// The shared defect: nothing in the review path could tell "the reviewer judged
// this work and rejected it" from "the reviewer never produced a verdict". Both
// arrived as plan.ReviewResult{Approved:false, Score:0}, so an unreadable reply
// burned a retry, was reported to the operator as a score, and handed the
// corrector "reviewer returned malformed or truncated JSON" as the defect to
// fix — an instruction no worker can act on. That is how a 20-minute run spent
// its whole ceiling re-editing code whose tests were green from minute two.

// TestEmptyReviewerReplyIsNotAConsideredRejection pins the silent default-zero.
//
// parseReviewOutput short-circuited an empty reply to plan.ReviewResult{}:
// approved=false, score=0, NO issue, NO summary — i.e. the exact value a
// reviewer that had considered the work and scored it zero would produce, with
// less information attached. It did not even reach plan.ParseReviewJSON, which
// has stamped ReviewMalformedIssue on unreadable replies for exactly this
// reason.
func TestEmptyReviewerReplyIsNotAConsideredRejection(t *testing.T) {
	r := &Runner{}
	for _, raw := range []string{"", "   ", "\n\t\n"} {
		got := r.parseReviewOutput(context.Background(), plan.Task{ID: "T1"}, raw, "reviewer", "prompt")
		if got.Approved {
			t.Fatalf("an empty reviewer reply must never approve: %+v", got)
		}
		if !hasIssue(got, plan.ReviewMalformedIssue) {
			t.Errorf("reviewer reply %q produced a verdict-shaped rejection carrying no marker: %+v\n"+
				"a reply the harness could not read must stay distinguishable from a considered score-0 rejection",
				raw, got)
		}
	}
}

// TestReviewerApprovalSurvivesAnEchoedWorkerJSON pins the subtler half.
//
// formatReviewPrompt hands the reviewer the worker's own status JSON under
// "## Agent output", and a small reviewer restates it before judging. The
// repair ladder's "extract" rung then carved out the FIRST balanced document —
// the echoed worker JSON — and threw the verdict away with the rest of the
// reply. An approval was therefore parsed as a document with no verdict in it
// and reported as approved=false score=0: a rejection the reviewer never made,
// on work it had just approved.
func TestReviewerApprovalSurvivesAnEchoedWorkerJSON(t *testing.T) {
	r := &Runner{}
	raw := `Looking at the agent output {"status":"done","files_changed":["stats.go"]} ` +
		"and the disk evidence, the change is real.\n" +
		`{"approved":true,"score":90,"summary":"disk evidence confirms stats.go","issues":[]}`

	got := r.parseReviewOutput(context.Background(), plan.Task{ID: "T1"}, raw, "reviewer", "prompt")
	if !got.Approved {
		t.Fatalf("the reviewer approved and the harness read a rejection: %+v\n"+
			"the verdict was destroyed by JSON extraction, not by the reviewer", got)
	}
	if got.Score != 90 {
		t.Errorf("score=%d want 90 — the whole verdict object must survive, not just the boolean", got.Score)
	}
}

// TestNoVerdictRescuedByWorkerCompletionEvenWhenTheReplyMentionsApproved pins
// the rescue hole.
//
// slmApprovalFallback is the path that saves a task from a broken reviewer, and
// it decided "is this broken?" with looksLikeBrokenReview — a substring scan
// that returns false the moment the reply contains `"approved"`. That is
// exactly what a reviewer whose verdict the extraction just destroyed looks
// like, so the one rescue that could have caught this was switched off by the
// very string that proves a verdict was attempted.
func TestNoVerdictRescuedByWorkerCompletionEvenWhenTheReplyMentionsApproved(t *testing.T) {
	raw := `{"note":"considering whether this is approved: unclear"}`
	if looksLikeBrokenReview(raw) {
		t.Fatal("fixture no longer reproduces the defect: looksLikeBrokenReview already catches it")
	}
	review := plan.ParseReviewJSON(raw)
	if review.Approved || len(review.Issues) == 0 {
		t.Fatalf("fixture is wrong: the parser found a verdict in %q (%+v)", raw, review)
	}

	r := &Runner{}
	got := r.slmApprovalFallback(review, plan.Task{ID: "T1", Role: plan.RoleWorker},
		gateState{done: true}, raw)
	if !got.Approved {
		t.Fatalf("a completed worker was rejected on the reviewer's SILENCE rather than on "+
			"anything the harness observed: %+v", got)
	}
}

// TestNoVerdictNeverReachesTheCorrectorAsAJSONComplaint is the operator-facing
// half: what the next round-trip is actually told to do.
//
// review.Issues went straight into formatCorrectPrompt's "## Review issues"
// list, so a reviewer that could not emit JSON produced the instruction
// "reviewer returned malformed or truncated JSON — treated as a rejection" for
// the CORRECTOR. Nothing a worker does to the code can satisfy that, which is
// why every correction round re-emitted the same implementation and was
// rejected again.
func TestNoVerdictNeverReachesTheCorrectorAsAJSONComplaint(t *testing.T) {
	r := &Runner{}
	task := plan.Task{ID: "T1", Title: "Implement Median", Role: plan.RoleWorker,
		Description: "add Median to stats.go", Output: `{"status":"done"}`}

	review := r.parseReviewOutput(context.Background(), task,
		"I am not sure what to say about this one.", "reviewer", "prompt")
	review = r.applyHardGates(&task, review, gateState{}, nil)

	if review.Approved {
		t.Fatalf("fixture is wrong: an unreadable reply must not approve: %+v", review)
	}
	if len(review.Issues) == 0 {
		t.Fatal("a rejection must always carry at least one issue")
	}
	prompt := r.formatCorrectPrompt(task, review)
	if strings.Contains(prompt, plan.ReviewMalformedIssue) {
		t.Errorf("the corrector is being asked to fix the REVIEWER's JSON:\n%s",
			issuesSection(prompt))
	}

	// A hard gate that DID fire still owns the verdict text — the reviewer's
	// silence must not overwrite a real, specific reason.
	gated := r.applyHardGates(&task, r.parseReviewOutput(context.Background(), task, "",
		"reviewer", "prompt"), gateState{smokeFail: true}, nil)
	if !strings.Contains(gated.Summary, "smoke") {
		t.Errorf("a blocked task must be rejected for the gate that blocked it, got %q", gated.Summary)
	}
}

// TestConsideredRejectionsStillCarryTheReviewersOwnVerdict keeps the guard
// honest in the other direction.
func TestConsideredRejectionsStillCarryTheReviewersOwnVerdict(t *testing.T) {
	r := &Runner{}
	task := plan.Task{ID: "T1", Role: plan.RoleWorker}
	got := r.parseReviewOutput(context.Background(), task,
		`{"approved":false,"score":30,"summary":"stub in stats.go","issues":["stub code"]}`,
		"reviewer", "prompt")
	got = r.applyHardGates(&task, got, gateState{}, nil)
	if got.Summary != "stub in stats.go" || got.Score != 30 {
		t.Fatalf("a considered rejection must pass through untouched: %+v", got)
	}
	if !hasIssue(got, "stub code") {
		t.Fatalf("the reviewer's own issue was dropped: %v", got.Issues)
	}
}

func hasIssue(r plan.ReviewResult, want string) bool {
	for _, is := range r.Issues {
		if is == want {
			return true
		}
	}
	return false
}

// issuesSection isolates the corrector prompt's issue list for a failure message.
func issuesSection(prompt string) string {
	i := strings.Index(prompt, "## Review issues")
	if i < 0 {
		return prompt
	}
	return strings.TrimSpace(prompt[i:])
}
