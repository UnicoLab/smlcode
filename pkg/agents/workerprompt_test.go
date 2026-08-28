package agents

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// ── A language-specialised tester is still a tester ──────────────────────
//
// Per-task routing puts `go-tester` / `python-tester` on a verification task
// whenever a language pack is active — which is every run with a language pack,
// i.e. most of them. Everything that recognizes a tester by exact id then stops
// recognizing it, and the finish contract is the one that hurts: a tester told
// to finish with the WORKER contract answers {"status":"done","files_changed":…}
// while the gate parses for {"passed":…,"failures":…}, so a passing tester reads
// as a malformed one and the run rewrites a plan that was fine.

func TestALanguageTesterGetsTheTesterFinishContract(t *testing.T) {
	for _, role := range []string{plan.RoleTester, "go-tester", "python-tester", "react-tester"} {
		task := plan.Task{
			ID: "T1", Title: "verify", Role: role,
			Description: "Run the tests.", Files: []string{"main.go"},
		}
		got := BuildWorkerPrompt(task, WorkerPromptOptions{LangHint: "Project language: Go."})

		// The tester contract asks for passed/failures; the worker contract asks
		// for status/files_changed. Handing a tester the latter is what turns a
		// passing verification into a malformed one.
		if !strings.Contains(got, `"passed"`) {
			t.Errorf("%s was not given the tester finish contract:\n%s", role, tail(got))
		}
		if strings.Contains(got, `"files_changed"`) {
			t.Errorf("%s was given the WORKER finish contract:\n%s", role, tail(got))
		}
	}
}

// And a worker must keep the worker contract — the fix must not swing the
// other way and hand every role the tester's.
func TestAWorkerKeepsTheWorkerFinishContract(t *testing.T) {
	for _, role := range []string{plan.RoleWorker, "go-worker", "go-corrector", plan.RoleCorrector} {
		task := plan.Task{ID: "T1", Title: "build", Role: role, Description: "Write it.", Files: []string{"main.go"}}
		got := BuildWorkerPrompt(task, WorkerPromptOptions{})
		if !strings.Contains(got, `"files_changed"`) {
			t.Errorf("%s lost the worker finish contract:\n%s", role, tail(got))
		}
	}
}

func tail(s string) string {
	if len(s) < 600 {
		return s
	}
	return "…" + s[len(s)-600:]
}

// ── The prompt must not present bookkeeping as human instruction ─────────
//
// The harness stamps a task's Notes with its own state: a dedupe key, an
// attempt count, which turn a ticket belongs to. That block used to be rendered
// under the heading "Human notes", so a 30B model was told the highest-authority
// text in its pack was `correction-key: tester|handler returns 500|…` — and the
// things those markers stand for are already stated properly, in prose, in the
// ticket body.
func TestBookkeepingNeverReachesTheWorker(t *testing.T) {
	task := plan.Task{
		ID: "C2", Title: "fix the handler", Role: "go-corrector",
		Description: "The tester gate rejected this work.",
		Files:       []string{"internal/http/todo.go"},
		Notes: "correction ticket from the tester gate; assigned to go-worker\n" +
			"correction-key: tester|handler returns 500|internal/http/todo.go\n" +
			"correction-attempt: 2\n" +
			"query scope run-178791680\n" +
			"REOPENED: tester implicated this task/file/acceptance.",
	}
	got := BuildWorkerPrompt(task, WorkerPromptOptions{})

	for _, leaked := range []string{"correction-key:", "correction-attempt:", "query scope"} {
		if strings.Contains(got, leaked) {
			t.Errorf("bookkeeping %q reached the worker prompt", leaked)
		}
	}
	// Harness prose that actually tells the agent something is kept.
	if !strings.Contains(got, "REOPENED: tester implicated this task") {
		t.Error("a reopen reason was dropped along with the bookkeeping")
	}
	// And it no longer claims a human wrote it.
	if strings.Contains(got, "Human notes") {
		t.Error("harness prose is still presented as human instruction")
	}
}

// A note a human actually left must survive intact.
func TestAHumanNoteStillReachesTheWorker(t *testing.T) {
	task := plan.Task{
		ID: "T1", Title: "add the endpoint", Role: "go-worker",
		Description: "Serve GET /api/todos.", Files: []string{"main.go"},
		Notes: "Use the existing store, do not add a new one.\ncorrection-attempt: 1",
	}
	got := BuildWorkerPrompt(task, WorkerPromptOptions{})
	if !strings.Contains(got, "Use the existing store, do not add a new one.") {
		t.Errorf("a human's note was dropped:\n%s", tail(got))
	}
	if strings.Contains(got, "correction-attempt") {
		t.Error("bookkeeping survived alongside the human note")
	}
}

// A task whose Notes are ONLY bookkeeping must not render an empty heading.
func TestAllBookkeepingRendersNoNotesSection(t *testing.T) {
	task := plan.Task{
		ID: "T1", Title: "x", Role: "go-worker", Description: "d", Files: []string{"a.go"},
		Notes: "correction-key: k\ncorrection-attempt: 3",
	}
	if got := BuildWorkerPrompt(task, WorkerPromptOptions{}); strings.Contains(got, "Notes:") {
		t.Errorf("an empty Notes heading was rendered:\n%s", tail(got))
	}
}
