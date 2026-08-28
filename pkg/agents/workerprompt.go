package agents

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// The worker contract has ONE source of truth, and it lives here.
//
// There used to be two builders: pkg/loop's Runner.formatWorkerPrompt and the
// orchestrator's formatWorkerPromptFor. Production used the orchestrator's,
// which DROPPED the checklist, the "no extra helper files" rule, the ws_patch
// retry rule, the ws_shell smoke step and the no-stubs rule — every one of
// which the review gates then rejected on. A worker was being graded against a
// contract it was never shown.
//
// It matters more than a shared-constant tidy-up would suggest. A 7B–32B model
// weights the task-adjacent restatement far above the same words in a system
// prompt written thousands of tokens earlier; recency is most of what it has.
// So the rules the gates enforce belong NEXT TO the task, every time.

// WorkerScopeRules are the hard-scope rules that follow the focus-file list.
// They are only meaningful when the task actually names focus files.
func WorkerScopeRules() string {
	return "Do NOT create main.go / index.js / other entrypoints unless listed above.\n" +
		"Do NOT add extra helper files or unrelated functions — only what acceptance requires.\n" +
		"If ws_patch fails, re-read the file and retry a minimal SEARCH/REPLACE; never invent new root files.\n"
}

// WorkerTaskRules is the canonical "## Required finish" block for an
// implementation role, with a language-appropriate smoke command.
//
// lang accepts either a short id ("go", "python", "js") or a full project
// language hint sentence, so callers can pass whatever they already have.
func WorkerTaskRules(lang string) string {
	return fmt.Sprintf(`
## Required finish
1. ws_read focus files first, then ws_edit / ws_patch (prefer over rewrites).
2. ws_write is NEW files only — refused on existing paths. No cat> overwrites.
3. After edits: ws_shell smoke (%s). Fix failures before done.
4. No stubs (pass / … / NotImplemented / TODO panic). Never add argparse --help.
5. End with STRICT JSON only:
{"status":"done","summary":"...","files_changed":["real/path.go"],"notes":"..."}
Never claim done without tool edits. Never end on a tool call.
`, smokeHintFor(lang))
}

// TesterTaskRules is the canonical finish block for the tester role, which ends
// on a pass/fail verdict rather than a status object.
func TesterTaskRules(lang string) string {
	return fmt.Sprintf(`
## Required finish (tester)
1. Use ws_shell to install deps if needed, then run real tests or smoke commands (%s).
2. Reading files alone is NOT verification — commands must exit 0.
3. End with STRICT JSON only:
{"passed":true|false,"commands":["exact shell…"],"summary":"...","failures":["T1: path — reason"]}
Never end on a tool call. Never soft-pass broken code.
`, smokeHintFor(lang))
}

// smokeHintFor maps a language id or hint sentence to concrete smoke commands.
// The generic fallback is the pre-existing wording, so an unknown project is no
// worse off than before.
func smokeHintFor(lang string) string {
	l := strings.ToLower(lang)
	switch {
	case strings.Contains(l, "go") && !strings.Contains(l, "django") && !strings.Contains(l, "mongo"):
		return "go build ./... / go test ./pkg/... -short"
	case strings.Contains(l, "python"), strings.Contains(l, "pytest"):
		return "python -m py_compile PATH / python -m pytest -q"
	case strings.Contains(l, "rust"), strings.Contains(l, "cargo"):
		return "cargo build --quiet / cargo test --quiet"
	case strings.Contains(l, "java"), strings.Contains(l, "gradle"), strings.Contains(l, "maven"):
		return "mvn -q test / ./gradlew test"
	case strings.Contains(l, "js"), strings.Contains(l, "ts"), strings.Contains(l, "node"),
		strings.Contains(l, "npm"):
		return "node --check PATH / npx tsc --noEmit / npm test"
	case strings.Contains(l, "c++"), strings.Contains(l, "cpp"), strings.Contains(l, "cmake"):
		return "cmake --build build / ctest"
	}
	return "python -m py_compile PATH / go test ./pkg -short / node --check PATH"
}

// WorkerPromptOptions tunes BuildWorkerPrompt.
type WorkerPromptOptions struct {
	// LangHint is the project language line ("Project language: Go. …").
	LangHint string
	// Description overrides the rendered task body. Callers that inject a
	// scoped context pack (or strip one) pass the prepared text here; empty
	// falls back to Task.Description.
	Description string
}

// BuildWorkerPrompt renders the full task-adjacent worker prompt: task
// identity, language, body, hard-scoped focus files, acceptance, checklist,
// human notes and the required-finish rules for the task's role.
//
// Both the inner loop's fallback and the orchestrator's production builder
// should go through this, so a rule can never again exist in the gate but not
// in the prompt.
func BuildWorkerPrompt(t plan.Task, opt WorkerPromptOptions) string {
	desc := opt.Description
	if strings.TrimSpace(desc) == "" {
		desc = t.Description
	}

	var b strings.Builder
	b.WriteString("Atomic task — complete only this:\n\n")
	fmt.Fprintf(&b, "ID: %s\nTitle: %s\nColumn: %s\nRole: %s\n\n", t.ID, t.Title, t.Column, t.Role)
	if h := strings.TrimSpace(opt.LangHint); h != "" {
		b.WriteString("## Project language\n" + h + "\n\n")
	}
	b.WriteString(desc)
	b.WriteString("\n")

	if len(t.Files) > 0 {
		b.WriteString("\n## Focus files (HARD SCOPE)\nOnly edit these paths or files in the same package directory:\n- ")
		b.WriteString(strings.Join(t.Files, "\n- "))
		b.WriteString("\n")
		b.WriteString(WorkerScopeRules())
	}
	if strings.TrimSpace(t.Acceptance) != "" {
		b.WriteString("\nAcceptance criteria:\n")
		b.WriteString(t.Acceptance)
		b.WriteString("\n")
	}
	// The checklist is the model's own decomposition of the acceptance
	// criteria. Dropping it (as the orchestrator's builder did) removes the
	// only per-step structure a small model gets.
	if len(t.Checklist) > 0 {
		b.WriteString("\nChecklist:\n")
		for _, c := range t.Checklist {
			mark := "[ ]"
			if c.Done {
				mark = "[x]"
			}
			fmt.Fprintf(&b, "- %s %s\n", mark, c.Text)
		}
	}
	// "Notes", not "Human notes": most of what lands here is written by the
	// harness — a reopen reason, a placeholder gap — and claiming a human wrote
	// it hands harness prose an authority it should not have. The bookkeeping
	// lines are dropped entirely; see plan.PromptNotes.
	if notes := plan.PromptNotes(t.Notes); notes != "" {
		b.WriteString("\nNotes:\n")
		b.WriteString(notes)
		b.WriteString("\n")
	}

	if plan.IsTesterRole(t.Role) {
		b.WriteString(TesterTaskRules(opt.LangHint))
		return b.String()
	}
	b.WriteString(WorkerTaskRules(opt.LangHint))
	return b.String()
}
