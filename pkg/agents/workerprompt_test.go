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

// ── The scoped pack is the shared byte prefix ─────────────────────────────
//
// context.TaskPack.Render is written most-stable-first so that every task
// sharing a pack shares a KV-cache prefix. The prompt used to open with
// "ID: T1 / Title: … / Column: …" BEFORE the pack, so the shared prefix ended
// at "Atomic task — complete only this:\n\nID: T" and the whole pack was
// re-prefilled for every task.

func TestTasksSharingAPackShareItAsABytePrefix(t *testing.T) {
	pack := "# Scoped context for role=worker\n\n## Doc: README\n\nUse the store.\n\n" +
		"## File: internal/store.go\n\n```go\npackage store\n```"
	mk := func(id, title, body string) plan.Task {
		return plan.Task{
			ID: id, Title: title, Role: plan.RoleWorker, Column: plan.ColInProgress,
			Description: pack + "\n## Task instructions\n\n" + body,
			Files:       []string{"internal/store.go"},
			Acceptance:  "go test ./... passes",
		}
	}
	opt := WorkerPromptOptions{LangHint: "Project language: Go."}
	a := BuildWorkerPrompt(mk("T1", "add List", "Add List to the store."), opt)
	b := BuildWorkerPrompt(mk("T2", "add Delete", "Add Delete to the store."), opt)

	common := 0
	for common < len(a) && common < len(b) && a[common] == b[common] {
		common++
	}
	if common < len(pack) {
		t.Fatalf("shared byte prefix is %d bytes; the %d-byte pack must be inside it:\n%q", common, len(pack), a[:common])
	}
	if !strings.HasPrefix(a, "# Scoped context for role=worker") {
		t.Errorf("prompt does not open with the pack:\n%s", a[:120])
	}
	// The language line is project-wide, so it belongs to the shared prefix too.
	if !strings.Contains(a[:common], "## Project language\nProject language: Go.") {
		t.Error("the language hint is outside the shared prefix")
	}
	// The task header sits immediately before the task's own instructions.
	header := strings.Index(a, "Atomic task — complete only this:\n\nID: T1\nTitle: add List\nRole: worker\n\n## Task instructions\n\nAdd List to the store.")
	if header < 0 {
		t.Errorf("task header is not immediately before the instructions:\n%s", a)
	}
	if strings.Contains(a, "Column:") {
		t.Error("the Column line varies between attempts and must not be in the prompt")
	}
	// Nothing the gates enforce was lost in the reorder.
	for _, want := range []string{"## Focus files (HARD SCOPE)", "internal/store.go", "Acceptance criteria:", "## Required finish", `"files_changed"`} {
		if !strings.Contains(a, want) {
			t.Errorf("prompt lost %q", want)
		}
	}
}

// A description without a pack keeps the historical shape: header first.
func TestPromptWithoutAPackOpensWithTheTaskHeader(t *testing.T) {
	task := plan.Task{ID: "T1", Title: "x", Role: plan.RoleWorker, Description: "Do it.", Files: []string{"a.go"}}
	got := BuildWorkerPrompt(task, WorkerPromptOptions{LangHint: "Project language: Go."})
	if !strings.HasPrefix(got, "Atomic task — complete only this:\n\nID: T1\nTitle: x\nRole: worker\n\n## Project language\nProject language: Go.\n\nDo it.\n") {
		t.Errorf("unexpected shape:\n%s", got)
	}
	if strings.Contains(got, "## Task instructions") {
		t.Error("a pack-less prompt grew a Task instructions heading")
	}
}

func TestSplitScopedPack(t *testing.T) {
	pack, body := SplitScopedPack("# Scoped context for role=worker\n\nbig pack\n\n## Task instructions\n\nDo the thing\n")
	if pack != "# Scoped context for role=worker\n\nbig pack" || body != "Do the thing" {
		t.Fatalf("pack=%q body=%q", pack, body)
	}
	if pack, body := SplitScopedPack("plain description"); pack != "" || body != "plain description" {
		t.Fatalf("pack-less split: pack=%q body=%q", pack, body)
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
