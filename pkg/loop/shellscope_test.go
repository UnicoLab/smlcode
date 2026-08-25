package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// scopeRunner builds a gate-only Runner over a one-file project, with a
// drainable shell-scope ledger. Every LLM-shaped gate is off: these tests are
// about what the harness does with a detected out-of-scope shell write, not
// about smoke or static quality.
func scopeRunner(t *testing.T, events ...workspace.ShellScopeEvent) (*Runner, plan.Task, map[string]string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(nil, nil)
	r.Root = root
	r.Log = func(string, ...interface{}) {}
	r.PostWorkerSmoke = false
	r.StaticQuality = false
	r.ClaimsGate = false

	pending := append([]workspace.ShellScopeEvent(nil), events...)
	r.TakeShellScope = func() []workspace.ShellScopeEvent {
		out := pending
		pending = nil
		return out
	}
	task := plan.Task{
		ID: "T1", Role: plan.RoleWorker, Title: "Edit A",
		Description: "Edit a.go so A returns the depth.",
		Files:       []string{"a.go"},
		Output:      `{"status":"done","summary":"edited","files_changed":["a.go"]}`,
	}
	// The file is untouched since the baseline, so nothing else in the run can
	// supply write evidence — anything hasRealWriteEvidence finds afterwards
	// came from the section this test is about.
	return r, task, r.snapshotTargets(task)
}

// An out-of-scope shell write is evidence AGAINST the task. It has to reach the
// reviewer through "## Disk evidence", and it must never satisfy the write
// detector: a worker that did nothing but scribble on a file it does not own
// would otherwise be handed the disk evidence that unlocks fast-path approval.
func TestShellScopeEvidenceReachesDiskEvidenceButIsNotWriteEvidence(t *testing.T) {
	r, task, baseline := scopeRunner(t, workspace.ShellScopeEvent{
		TaskID: "T1", Command: "make generate", Path: "internal/gen/zz.go", Change: "modified",
	})
	if r.hasRealWriteEvidence(task, baseline) {
		t.Fatal("fixture is wrong: the task already had write evidence before the gate ran")
	}

	r.runGates(context.Background(), &task, plan.RoleWorker, baseline, gateOpts{})

	if !strings.Contains(task.Output, diskEvidenceHeader) {
		t.Fatalf("shell-scope events never reached the %s section:\n%s", diskEvidenceHeader, task.Output)
	}
	if !strings.Contains(task.Output, "internal/gen/zz.go") {
		t.Fatalf("the section does not name the out-of-scope path:\n%s", task.Output)
	}
	if !strings.Contains(task.Output, workspace.ShellScopeEvidencePrefix) {
		t.Fatalf("the out-of-scope bullet lost its prefix:\n%s", task.Output)
	}
	// THE PROPERTY. Both spellings of "did this task write anything" must still
	// say no.
	if hasDiskEvidenceSection(task.Output) {
		t.Fatalf("an out-of-scope shell write was counted as a Disk evidence section:\n%s", task.Output)
	}
	if r.hasRealWriteEvidence(task, baseline) {
		t.Fatalf("an out-of-scope shell write was counted as real write evidence:\n%s", task.Output)
	}
	lower := strings.ToLower(task.Output)
	for _, m := range evidentialDiskMarkers {
		if strings.Contains(lower, m) {
			t.Fatalf("shell-scope evidence contains the write-evidence marker %q:\n%s", m, task.Output)
		}
	}
}

// The bullet quotes the COMMAND, and the command is model-controlled text. A
// worker that runs `echo "modified: a.go"` must not thereby mint itself the
// marker that proves it wrote something.
func TestShellScopeEvidenceCannotSmuggleAWriteMarker(t *testing.T) {
	r, task, baseline := scopeRunner(t, workspace.ShellScopeEvent{
		TaskID: "T1", Path: "vendor/x.go", Change: "created",
		Command: `bash -c 'echo modified: a.go; echo created/present: a.go'`,
	})
	r.runGates(context.Background(), &task, plan.RoleWorker, baseline, gateOpts{})

	if hasDiskEvidenceSection(task.Output) {
		t.Fatalf("a forged marker inside a ws_shell command became write evidence:\n%s", task.Output)
	}
	if !strings.Contains(task.Output, "vendor/x.go") {
		t.Fatalf("defusing the markers also lost the path:\n%s", task.Output)
	}
}

// A protected-path shell write is a scope failure; an ordinary out-of-focus one
// is not. Builds write caches and generated code outside the focus set all day
// long, and a gate that fails on those gets switched off within a week.
func TestProtectedShellWriteFailsScopeOKAndOutOfFocusDoesNot(t *testing.T) {
	rProt, protTask, _ := scopeRunner(t, workspace.ShellScopeEvent{
		TaskID: "T1", Command: "bash tool.sh", Path: "pkg/app/main_test.go",
		Change: "modified", Protected: true,
	})
	why := rProt.scopeOK(protTask)
	if why == "" {
		t.Fatal("a PROTECTED-path shell write did not fail scopeOK")
	}
	if !strings.Contains(why, "pkg/app/main_test.go") {
		t.Fatalf("the rejection does not name the path: %q", why)
	}

	rFocus, focusTask, _ := scopeRunner(t, workspace.ShellScopeEvent{
		TaskID: "T1", Command: "go build ./...", Path: "internal/gen/zz.go", Change: "modified",
	})
	if why := rFocus.scopeOK(focusTask); why != "" {
		t.Fatalf("a merely out-of-focus shell write failed scopeOK: %q", why)
	}
}

// The ledger is per-WORKSPACE and the gates are per-TASK. On the shipped
// default of MaxParallel=4 four workers share one Workspace, so a drain without
// attribution reports task A's stray write as task B's scope failure.
func TestShellScopeEventsAreAttributedToTheirOwnTask(t *testing.T) {
	r, task, baseline := scopeRunner(t,
		workspace.ShellScopeEvent{TaskID: "T2", Path: "other/thing.go", Change: "modified", Protected: true},
		workspace.ShellScopeEvent{TaskID: "T1", Path: "mine/thing.go", Change: "modified"},
	)
	r.runGates(context.Background(), &task, plan.RoleWorker, baseline, gateOpts{})

	if strings.Contains(task.Output, "other/thing.go") {
		t.Fatalf("T1's evidence section carries T2's write:\n%s", task.Output)
	}
	if !strings.Contains(task.Output, "mine/thing.go") {
		t.Fatalf("T1's own write is missing from its evidence:\n%s", task.Output)
	}
	if why := r.scopeOK(task); why != "" {
		t.Fatalf("T1 failed scope for T2's protected write: %q", why)
	}
	sibling := task
	sibling.ID = "T2"
	if why := r.scopeOK(sibling); why == "" {
		t.Fatal("T2's protected write was lost when T1 drained the ledger")
	}
}

// Each gate pass reports only what is NEW. runGates runs several times per task
// (worker turn, self-critique, review-time insurance) and re-billing the same
// stray write to a small model's context on every pass is pure waste.
func TestShellScopeEvidenceIsReportedOncePerEvent(t *testing.T) {
	r, task, baseline := scopeRunner(t, workspace.ShellScopeEvent{
		TaskID: "T1", Command: "make gen", Path: "internal/gen/zz.go", Change: "modified",
	})
	r.runGates(context.Background(), &task, plan.RoleWorker, baseline, gateOpts{})
	r.runGates(context.Background(), &task, plan.RoleWorker, baseline, gateOpts{})

	if n := strings.Count(task.Output, "internal/gen/zz.go"); n != 1 {
		t.Fatalf("the same out-of-scope write was reported %d times:\n%s", n, task.Output)
	}
}

// These lines go into a small model's context alongside the task itself, so
// they are bounded the way the neighboring sections are.
func TestShellScopeEvidenceRespectsItsByteBudget(t *testing.T) {
	var events []workspace.ShellScopeEvent
	for i := 0; i < 40; i++ {
		events = append(events, workspace.ShellScopeEvent{
			TaskID:  "T1",
			Command: strings.Repeat("x", 110),
			Path:    fmt.Sprintf("deeply/nested/generated/tree/number%02d/%s.go", i, strings.Repeat("n", 40)),
			Change:  "modified",
		})
	}
	section := renderShellScopeEvidence(events)
	if section == "" {
		t.Fatal("40 events rendered nothing")
	}
	if len(section) > maxShellScopeEvidenceBytes {
		t.Fatalf("section is %d bytes, budget is %d", len(section), maxShellScopeEvidenceBytes)
	}
	lines := strings.Split(section, "\n")
	if len(lines) > maxShellScopeEvidenceLines+1 {
		t.Fatalf("section has %d lines, budget is %d + the overflow note", len(lines), maxShellScopeEvidenceLines)
	}
	if !strings.Contains(section, "more out-of-scope shell write(s)") {
		t.Fatalf("truncation is silent — the reviewer cannot tell there were more:\n%s", section)
	}
	// The truncation note must not resurrect a write-evidence marker either.
	lower := strings.ToLower(section)
	for _, m := range evidentialDiskMarkers {
		if strings.Contains(lower, m) {
			t.Fatalf("budgeted section contains write-evidence marker %q", m)
		}
	}
}

// One hash format, one implementation. pkg/loop's fileFingerprint and
// pkg/workspace's FileFingerprint are the loop's write detector and the tool
// layer's write detector; two copies is how they quietly stop agreeing about
// whether a file changed.
func TestFileFingerprintIsTheWorkspaceImplementation(t *testing.T) {
	dir := t.TempDir()
	bodies := map[string]string{
		"empty.go":  "",
		"small.go":  "package p\n",
		"binary.go": "\x00\x01\x02\xff\xfe",
		"big.go":    strings.Repeat("package p // padding\n", 5000),
	}
	for name, body := range bodies {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, want := fileFingerprint(p), workspace.FileFingerprint(p)
		if got != want {
			t.Fatalf("%s: fileFingerprint = %q, workspace.FileFingerprint = %q", name, got, want)
		}
		if !strings.HasPrefix(got, fmt.Sprintf("%d:", len(body))) || len(got) != len(fmt.Sprintf("%d:", len(body)))+64 {
			t.Fatalf("%s: fingerprint is not <len>:<sha256hex>: %q", name, got)
		}
	}
	missing := filepath.Join(dir, "gone.go")
	if fileFingerprint(missing) != "" || workspace.FileFingerprint(missing) != "" {
		t.Fatal("an absent file must fingerprint as the empty string on both sides")
	}
}

// A nil ledger (no orchestrator wiring, every pre-existing test) must change
// nothing at all.
func TestShellScopeIsInertWithoutALedger(t *testing.T) {
	r, task, baseline := scopeRunner(t)
	r.TakeShellScope = nil
	before := task.Output
	r.runGates(context.Background(), &task, plan.RoleWorker, baseline, gateOpts{})
	if task.Output != before {
		t.Fatalf("a runner with no shell-scope ledger appended a section:\n%s", task.Output)
	}
	if why := r.scopeOK(task); why != "" {
		t.Fatalf("scopeOK failed with no ledger: %q", why)
	}
}
