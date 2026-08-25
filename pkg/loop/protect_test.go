package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/permissions"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// protectFixture is a small Go project plus the REAL coding tools, registered
// against the same FocusGuard the loop installs protections on. Driving the
// actual ws_edit / ws_shell executors is the point: a protection that only
// exists in the guard's own map proves nothing about whether a worker can still
// write the file.
type protectFixture struct {
	root  string
	guard *workspace.FocusGuard
	ws    *workspace.Workspace
	reg   *tools.ToolRegistry
}

func newProtectFixture(t *testing.T, focus []string) *protectFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(abs), 0o750); mkErr != nil {
			t.Fatal(mkErr)
		}
		if wErr := os.WriteFile(abs, []byte(body), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
	}
	write("pkg/app/main.go", "package app\n\nfunc Run() int { return 0 }\n")
	write("pkg/app/main_test.go", "package app\n\n// TestRun is the contract.\nfunc TestRun() {}\n")

	guard := workspace.NewFocusGuard()
	if len(focus) > 0 {
		guard.SetWave([][]string{focus})
	}
	reg := tools.NewToolRegistry()
	ws, _, err := workspace.RegisterCodingToolsWithWorkspace(reg, root, workspace.ToolOpts{
		Permission: permissions.ModeAuto, ShellPermission: "allow",
		SlmDir: filepath.Join(root, ".slmcode"),
		Focus:  guard, DisableSyntaxCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &protectFixture{root: root, guard: guard, ws: ws, reg: reg}
}

// call runs a registered tool the way an agent would.
func (fx *protectFixture) call(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	tool, ok := fx.reg.GetTool(name)
	if !ok {
		t.Fatalf("%s is not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(context.Background(), string(raw))
	if err != nil {
		return "Error: " + err.Error()
	}
	s, _ := out.(string)
	return s
}

func (fx *protectFixture) runner(t *testing.T) *Runner {
	t.Helper()
	r := NewRunner(nil, nil)
	r.Root = fx.root
	r.Focus = fx.guard
	r.Log = func(string, ...interface{}) {}
	return r
}

// THE REGRESSION TEST FOR THE LIVE INCIDENT.
//
// A 9B run under a task whose text said "Do not edit, add or delete any
// _test.go file" changed that file's sha256 anyway. FocusGuard.Protect could
// already enforce exactly that — through ws_edit's checkFocus AND through the
// ws_shell scope detector — and nothing populated it. This asserts both halves
// from one derived protection: the tool refuses, and the opaque shell write is
// caught after the fact and flagged Protected.
func TestTaskProtectionsAreDerivedAndEnforced(t *testing.T) {
	fx := newProtectFixture(t, []string{"pkg/app/main.go"})
	task := plan.Task{
		ID:    "T1",
		Role:  plan.RoleWorker,
		Title: "Implement Run",
		Description: "Implement Run in pkg/app/main.go so it returns the queue depth. " +
			"Do not edit, add or delete any _test.go file.",
		Files:      []string{"pkg/app/main.go"},
		Acceptance: "go test ./pkg/app/...",
	}

	// The same seam runWave uses.
	r := fx.runner(t)
	undo := r.applyWaveProtections([]plan.Task{task})
	defer undo()

	if !fx.guard.HasProtections() {
		t.Fatal("no protection was derived from an explicit \"do not edit any _test.go file\"")
	}
	if !fx.guard.IsProtected("pkg/app/main_test.go") {
		t.Fatalf("_test.go is not protected; derived = %v", deriveTaskProtections(task))
	}
	// The task's own focus file must stay writable — a protection that freezes
	// the work is worse than no protection at all.
	if !fx.guard.Allow("pkg/app/main.go") {
		t.Fatal("the task's focus file became unwritable")
	}

	// (a) ws_edit refuses.
	if got := fx.call(t, "ws_read", map[string]any{"path": "pkg/app/main_test.go"}); strings.HasPrefix(got, "Error:") {
		t.Fatalf("ws_read failed: %s", got)
	}
	got := fx.call(t, "ws_edit", map[string]any{
		"path": "pkg/app/main_test.go", "old_str": "func TestRun() {}", "new_str": "func TestRun() { _ = 1 }",
	})
	if !strings.Contains(got, "protected-path write blocked") {
		t.Fatalf("ws_edit accepted a write to a protected path: %q", got)
	}
	on, err := os.ReadFile(filepath.Join(fx.root, "pkg", "app", "main_test.go")) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(on), "_ = 1") {
		t.Fatal("the protected file was modified on disk despite the refusal")
	}

	// (b) an OPAQUE shell write to the same path is flagged Protected. The
	// script is what makes it opaque — GuardShellWrites' static redirect
	// analysis cannot see inside `bash tool.sh`, exactly like `python fix.py`.
	script := filepath.Join(fx.root, "tool.sh")
	if err := os.WriteFile(script, []byte("set -e\necho '// tampered' >> pkg/app/main_test.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := fx.call(t, "ws_shell", map[string]any{"command": "bash tool.sh"})
	if !strings.Contains(out, workspace.ShellScopeMarker) {
		t.Fatalf("shell write to a protected path was silent:\n%s", out)
	}
	var flagged bool
	for _, e := range fx.ws.ShellScopeEvents() {
		if e.Path == "pkg/app/main_test.go" {
			flagged = e.Protected
		}
	}
	if !flagged {
		t.Fatalf("shell write to pkg/app/main_test.go was not flagged Protected: %+v",
			fx.ws.ShellScopeEvents())
	}
}

// THE FALSE-POSITIVE GUARD.
//
// A false protection is strictly worse than a missed one: every ws_edit to the
// path is refused with a message that says rewording cannot help, so the worker
// burns its call budget and escalates to a human. Ordinary tasks — including
// ones that talk about tests, name files, or carry an exception clause the
// deriver cannot model — must come out with an EMPTY deny list.
func TestTaskWithoutProhibitionPhrasingGetsNoProtections(t *testing.T) {
	cases := []struct {
		name string
		task plan.Task
	}{
		{"plain edit task", plan.Task{
			ID: "A", Role: plan.RoleWorker, Title: "Add Sum to pkg/calc/calc.go",
			Description: "Add a Sum(a, b int) int helper to pkg/calc/calc.go and use it in main.go.",
			Files:       []string{"pkg/calc/calc.go"}, Acceptance: "go test ./...",
		}},
		{"mentions tests without the existing-tests phrasing", plan.Task{
			ID: "B", Role: plan.RoleWorker, Title: "Fix the parser",
			Description: "Fix the off-by-one in pkg/parse/parse.go. Make sure the tests still pass.",
			Files:       []string{"pkg/parse/parse.go"}, Acceptance: "go test ./...",
		}},
		{"prohibition carrying an exception clause", plan.Task{
			ID: "C", Role: plan.RoleWorker, Title: "Harden the loader",
			Description: "Do not edit any _test.go file unless the test itself asserts the old behavior.",
			Files:       []string{"pkg/load/load.go"},
		}},
		{"task whose job IS writing tests", plan.Task{
			ID: "D", Role: plan.RoleWorker, Title: "Cover the retry path",
			Description: "Write unit tests for pkg/retry/retry.go. The existing tests must keep passing.",
			Files:       []string{"pkg/retry/retry_test.go"},
		}},
		{"tester role", plan.Task{
			ID: "E", Role: plan.RoleTester, Title: "Verify",
			Description: "The existing tests must pass after this change.",
			Files:       []string{"pkg/retry/retry.go"},
		}},
		{"prohibition naming no file at all", plan.Task{
			ID: "F", Role: plan.RoleWorker, Title: "Tidy",
			Description: "Do not change the public API. Do not touch anything you do not understand.",
			Files:       []string{"pkg/api/api.go"},
		}},
		{"a bare extension must never become a pattern", plan.Task{
			ID: "G", Role: plan.RoleWorker, Title: "Tidy",
			Description: "Do not edit any *.go file you did not read first.",
			Files:       []string{"pkg/api/api.go"},
		}},
		{"empty task", plan.Task{ID: "H"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveTaskProtections(tc.task); len(got) > 0 {
				t.Fatalf("derived %v from a task that forbids nothing enforceable", got)
			}
		})
	}

	// End to end: with nothing derived the guard stays inert and the tool layer
	// keeps letting a legitimate write through.
	fx := newProtectFixture(t, []string{"pkg/app/main_test.go"})
	r := fx.runner(t)
	undo := r.applyWaveProtections([]plan.Task{cases[0].task})
	defer undo()
	if fx.guard.HasProtections() {
		t.Fatal("a plain task installed a deny list")
	}
	if got := fx.call(t, "ws_read", map[string]any{"path": "pkg/app/main_test.go"}); strings.HasPrefix(got, "Error:") {
		t.Fatalf("ws_read failed: %s", got)
	}
	got := fx.call(t, "ws_edit", map[string]any{
		"path": "pkg/app/main_test.go", "old_str": "func TestRun() {}", "new_str": "func TestRun() { _ = 1 }",
	})
	if strings.Contains(got, "blocked") {
		t.Fatalf("a legitimate test edit was refused with no protection installed: %q", got)
	}
}

// The deny list is WAVE-global while the phrasing is per-task, and a union of
// deny lists narrows where a union of allow lists widens. Task A's "leave the
// tests alone" must not quietly freeze task B, whose declared job is writing
// exactly those tests.
func TestWaveProtectionDoesNotBindASiblingTask(t *testing.T) {
	author := plan.Task{
		ID: "A", Role: plan.RoleWorker, Title: "Implement",
		Description: "Implement Run. Do not edit any _test.go file.",
		Files:       []string{"pkg/app/main.go"},
	}
	sibling := plan.Task{
		ID: "B", Role: plan.RoleWorker, Title: "Cover Run",
		Description: "Add coverage for Run.",
		Files:       []string{"pkg/app/main_test.go"},
	}
	if got := waveProtections([]plan.Task{author}); len(got) != 1 || got[0] != "*_test.go" {
		t.Fatalf("alone, the task must protect its tests; got %v", got)
	}
	if got := waveProtections([]plan.Task{author, sibling}); len(got) != 0 {
		t.Fatalf("a sibling that OWNS the test file was frozen by %v", got)
	}
}

// runWave is where the protections have to be installed and, just as
// importantly, removed: FocusGuard.Clear rebuilds the allowlist and leaves the
// deny list untouched by design, so without an explicit undo one wave's
// protections would bind every later wave in the run.
func TestRunWaveInstallsThenClearsTaskProtections(t *testing.T) {
	fx := newProtectFixture(t, nil)
	var duringWave bool
	exec := &scriptedExec{answer: func(req ggagent.SubAgentRequest, _ int) string {
		if req.TaskID == "T1" {
			duringWave = fx.guard.IsProtected("pkg/app/main_test.go")
		}
		return `{"status":"done","summary":"ok","files_changed":["pkg/app/main.go"]}`
	}}
	r := defaultRunner(t, fx.root, exec)
	r.Focus = fx.guard
	r.WorkerCritique = false
	r.Timeout = 10 * time.Second

	task := plan.Task{
		ID: "T1", Role: plan.RoleWorker, Title: "Implement Run",
		Description: "Implement Run in pkg/app/main.go. Do not edit any _test.go file.",
		Files:       []string{"pkg/app/main.go"}, Column: plan.ColReadyToDev,
	}
	board := &plan.Board{Tasks: []plan.Task{task}}
	if err := r.runWave(context.Background(), board, []plan.Task{task}); err != nil {
		t.Fatalf("runWave: %v", err)
	}
	if !duringWave {
		t.Fatal("runWave did not install the task's derived protections before dispatching the worker")
	}
	if fx.guard.HasProtections() {
		t.Fatal("runWave left the deny list installed after the wave finished")
	}
}

// The extraction itself, on the shapes that must and must not produce patterns.
func TestDeriveTaskProtectionsPatterns(t *testing.T) {
	cases := []struct {
		name string
		task plan.Task
		want []string
	}{
		{"suffix pattern gets a star", plan.Task{
			Description: "Do not edit, add or delete any _test.go file.",
		}, []string{"*_test.go"}},
		{"explicit path", plan.Task{
			Description: "Never modify docs/frozen.md; it is generated.",
		}, []string{"docs/frozen.md"}},
		{"author-written glob survives", plan.Task{
			Description: "Do not touch **/testdata/*.json in this task.",
		}, []string{"**/testdata/*.json"}},
		{"several files in one clause", plan.Task{
			Description: "Do not change go.mod or pkg/api/schema.yaml.",
		}, []string{"go.mod", "pkg/api/schema.yaml"}},
		{"clause ends at the sentence", plan.Task{
			Description: "Do not edit config.yaml. Then update pkg/app/main.go as needed.",
		}, []string{"config.yaml"}},
		{"placeholder paths are dropped", plan.Task{
			Description: "Do not edit path/to/file.go or your_file.py.",
		}, nil},
		{"implement-against-existing-tests, Go focus", plan.Task{
			Role:        plan.RoleWorker,
			Description: "Implement Decode so the existing tests pass. The suite is already written.",
			Files:       []string{"pkg/codec/decode.go"},
		}, []string{"*_test.go"}},
		{"implement-against-existing-tests, mixed languages derives nothing", plan.Task{
			Role:        plan.RoleWorker,
			Description: "Implement Decode so the existing tests pass.",
			Files:       []string{"pkg/codec/decode.go", "scripts/gen.py"},
		}, nil},
		{"implement-against-existing-tests, no focus files derives nothing", plan.Task{
			Role:        plan.RoleWorker,
			Description: "Implement Decode so the existing tests pass.",
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveTaskProtections(tc.task)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("deriveTaskProtections = %v, want %v", got, tc.want)
			}
		})
	}
}

// Derivation must be deterministic: the same task text always yields the same
// ordered slice, because these patterns reach a prompt and a verdict.
func TestDeriveTaskProtectionsIsDeterministic(t *testing.T) {
	task := plan.Task{
		Description: "Do not change go.mod, pkg/api/schema.yaml or docs/frozen.md.",
	}
	first := strings.Join(deriveTaskProtections(task), "|")
	for i := 0; i < 25; i++ {
		if got := strings.Join(deriveTaskProtections(task), "|"); got != first {
			t.Fatalf("run %d = %q, first = %q", i, got, first)
		}
	}
	if first != "docs/frozen.md|go.mod|pkg/api/schema.yaml" {
		t.Fatalf("patterns are not sorted: %q", first)
	}
}
