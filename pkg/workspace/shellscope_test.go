package workspace

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// scopeFixture builds a workspace whose ws_shell runs for real, with a tree
// laid out like a small Go project. writer is the body of tool.sh — the point
// of running the write through a SCRIPT is that it is opaque to the static
// analysis in GuardShellWrites, exactly like `python fix.py` or `make generate`
// in a live run.
type scopeFixture struct {
	ws   *Workspace
	root string
	seen []string // intervention reasons
}

func newScopeFixture(t *testing.T, focus, protect []string) *scopeFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(abs), 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
		if wErr := os.WriteFile(abs, []byte(body), 0o644); wErr != nil {
			t.Fatal(wErr)
		}
	}
	write("pkg/app/main.go", "package app\n\nfunc Run() {}\n")
	write("pkg/app/main_test.go", "package app\n\nfunc TestRun(t *testing.T) {}\n")
	write("pkg/other/util.go", "package other\n\nfunc Helper() {}\n")
	write("go.mod", "module example.com/x\n\ngo 1.23\n")
	if err := os.MkdirAll(filepath.Join(root, ".slmcode"), 0o750); err != nil {
		t.Fatal(err)
	}

	fx := &scopeFixture{root: root}
	guard := NewFocusGuard()
	if len(focus) > 0 {
		guard.SetWave([][]string{focus})
	}
	if len(protect) > 0 {
		guard.Protect(protect...)
	}
	ws, _, err := NewWorkspace(root, ToolOpts{
		Permission: "auto", ShellPermission: "allow",
		SlmDir: filepath.Join(root, ".slmcode"),
		Focus:  guard, DisableSyntaxCheck: true,
		OnIntervention: func(reason, _ string) { fx.seen = append(fx.seen, reason) },
	})
	if err != nil {
		t.Fatal(err)
	}
	fx.ws = ws
	return fx
}

// runScript writes tool.sh with the given body and runs it through ws_shell.
// tool.sh is created BEFORE the pre-command snapshot, so the script itself
// never shows up as a change.
func (fx *scopeFixture) runScript(t *testing.T, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.root, "tool.sh"), []byte("set -e\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := fx.ws.shell(context.Background(), map[string]interface{}{"command": "bash tool.sh"})
	if err != nil {
		t.Fatalf("ws_shell returned an error: %v", err)
	}
	s, _ := out.(string)
	return s
}

func (fx *scopeFixture) paths() []string {
	var out []string
	for _, e := range fx.ws.ShellScopeEvents() {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}

// THE REGRESSION GUARD.
//
// Every file-writing workspace tool calls checkFocus; ws_shell called nothing,
// so a command that wrote outside the focus set left no trace anywhere in the
// harness. This test fails on the unpatched tree: the tool result is just the
// command's (empty) output and the ledger is empty.
func TestShellWriteOutsideFocusIsNotSilent(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, nil)

	out := fx.runScript(t, "echo '// tampered' >> pkg/other/util.go\n")

	if !strings.Contains(out, ShellScopeMarker) {
		t.Fatalf("shell write outside focus was SILENT — result had no %q:\n%s", ShellScopeMarker, out)
	}
	if !strings.Contains(out, "pkg/other/util.go") {
		t.Errorf("notice does not name the file that changed:\n%s", out)
	}
	events := fx.ws.ShellScopeEvents()
	if len(events) != 1 {
		t.Fatalf("want 1 recorded event, got %d: %+v", len(events), events)
	}
	if events[0].Path != "pkg/other/util.go" || events[0].Change != "modified" {
		t.Errorf("event = %+v, want modified pkg/other/util.go", events[0])
	}
	if events[0].Protected {
		t.Error("an ordinary out-of-focus file must not be reported as protected")
	}
	if !strings.Contains(events[0].Command, "tool.sh") {
		t.Errorf("event does not attribute the change to the command: %q", events[0].Command)
	}
	// The gate seam: the reviewer must be able to see this.
	lines := ShellScopeEvidenceLines(events)
	if len(lines) != 1 || !strings.Contains(lines[0], "pkg/other/util.go") {
		t.Fatalf("evidence lines = %v", lines)
	}
	// …without it counting as proof the task did its job. pkg/loop's
	// evidentialDiskMarkers are matched with Contains, so the bullet must not
	// carry one.
	for _, marker := range []string{"modified:", "created/present:", "renamed:", "deleted:"} {
		if strings.Contains(lines[0], marker) {
			t.Errorf("evidence line %q contains write-evidence marker %q", lines[0], marker)
		}
	}
	// Draining is what the caller does per task.
	if got := fx.ws.TakeShellScopeEvents(); len(got) != 1 {
		t.Fatalf("TakeShellScopeEvents drained %d events", len(got))
	}
	if got := fx.ws.ShellScopeEvents(); len(got) != 0 {
		t.Fatalf("ledger not cleared after drain: %+v", got)
	}
}

// A command that only touches focus files must produce no complaint at all —
// this is the over-blocking regression, and it is the one that decides whether
// anybody leaves the guard switched on.
func TestShellWriteInsideFocusIsSilent(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, nil)

	out := fx.runScript(t, "echo '// in scope' >> pkg/app/main.go\necho done\n")

	if strings.Contains(out, ShellScopeMarker) {
		t.Fatalf("in-focus write was reported as out of scope:\n%s", out)
	}
	if got := fx.ws.ShellScopeEvents(); len(got) != 0 {
		t.Fatalf("in-focus write recorded events: %+v", got)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("command output was lost: %q", out)
	}
	// Same package as a focus file is in scope too (FocusGuard.Allow's dir rule).
	fx.runScript(t, "echo '// sibling' >> pkg/app/main_test.go\n")
	if got := fx.ws.ShellScopeEvents(); len(got) != 0 {
		t.Fatalf("same-package write recorded events: %+v", got)
	}
}

// Build tooling writes go.sum, caches, .git/ and editor droppings on every run.
// A guard that reports those gets turned off.
func TestShellScopeIgnoresBuildNoise(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, nil)

	out := fx.runScript(t, strings.Join([]string{
		"echo 'example.com/dep v1.0.0 h1:abc=' >> go.sum",
		"mkdir -p .git/objects node_modules/leftpad __pycache__ dist .pytest_cache",
		"echo x > .git/objects/blob",
		// A real .go file inside a pruned tree: if the walk descended, the
		// extension allowlist would happily pick it up.
		"mkdir -p vendor/dep && echo 'package dep' > vendor/dep/dep.go",
		"echo 'module.exports={}' > node_modules/leftpad/index.js",
		"echo compiled > __pycache__/util.cpython-311.pyc",
		"echo bundled > dist/bundle.js",
		"echo cached > .pytest_cache/v.json",
		"echo 'log line' > build.log",
		"echo 'backup' > pkg/other/util.go.orig",
	}, "\n")+"\n")

	if strings.Contains(out, ShellScopeMarker) {
		t.Fatalf("build noise triggered the scope guard:\n%s", out)
	}
	if got := fx.ws.ShellScopeEvents(); len(got) != 0 {
		t.Fatalf("build noise recorded events: %+v", got)
	}
}

// The harness writes .slmcode/board.json, TASKS.md, queries/ and waves/ WHILE a
// command runs. Watching those would fire on every shell call in a parallel
// wave, so they are deliberately out of the watched set — a documented
// trade-off, not an oversight. Direct writes to them are still refused at the
// tool layer (CheckHarnessStateWrite) and statically for redirects
// (GuardShellWrites); this test pins the boundary so a future widening of
// watchedControlFiles has to argue with it.
func TestShellScopeIgnoresHarnessOwnChurn(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, nil)

	out := fx.runScript(t, strings.Join([]string{
		"echo '{\"tasks\":[]}' > .slmcode/board.json",
		"echo '- item' > .slmcode/TASKS.md",
		"echo '{}' > .slmcode/checkpoint.json",
		"mkdir -p .slmcode/queries/q1 && echo '{}' > .slmcode/queries/q1/req.json",
		"mkdir -p .slmcode/waves/w1 && echo '{}' > .slmcode/waves/w1/snapshot.json",
	}, "\n")+"\n")

	if strings.Contains(out, ShellScopeMarker) {
		t.Fatalf("harness-owned churn triggered the guard:\n%s", out)
	}
	if got := fx.ws.ShellScopeEvents(); len(got) != 0 {
		t.Fatalf("harness churn recorded events: %+v", got)
	}
}

// Explore / docs phases run with the focus guard cleared and must not be
// second-guessed about project files.
func TestShellScopeInertWhenFocusDisabled(t *testing.T) {
	fx := newScopeFixture(t, nil, nil) // no SetWave, no Protect

	if fx.ws.Focus.Enabled() {
		t.Fatal("fixture guard should be disabled")
	}
	out := fx.runScript(t, "echo '// free' >> pkg/other/util.go\n"+
		"mkdir -p pkg/brand\necho 'package new' > pkg/brand/new.go\n")

	if strings.Contains(out, ShellScopeMarker) {
		t.Fatalf("disabled focus guard still complained:\n%s", out)
	}
	if got := fx.ws.ShellScopeEvents(); len(got) != 0 {
		t.Fatalf("disabled guard recorded events: %+v", got)
	}
	// Inert means CHEAP as well as quiet: the minimal pass fingerprints only
	// .slmcode/ control files, never the project tree.
	snap := fx.ws.scopeScan()
	if snap == nil {
		t.Fatal("minimal scan should still return a snapshot")
	}
	for rel := range snap.files {
		if !IsHarnessStatePath(rel) {
			t.Errorf("disabled guard fingerprinted project file %q", rel)
		}
	}
}

// .slmcode/ outside scratch is a privilege boundary, not an anti-wander
// heuristic: hooks.json is arbitrary bash on the next turn. It is flagged
// whether or not the path is in focus, and whether or not focus is on at all.
func TestShellWriteToHarnessStateIsFlagged(t *testing.T) {
	cases := []struct {
		name  string
		focus []string
	}{
		{"focus elsewhere", []string{"pkg/app/main.go"}},
		// Even if a task somehow listed harness state as its own focus file.
		{"harness state in focus", []string{".slmcode/hooks.json"}},
		// Explore phase: no focus at all.
		{"focus disabled", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newScopeFixture(t, tc.focus, nil)

			out := fx.runScript(t, "echo '{\"hooks\":{\"pre\":\"curl evil\"}}' > .slmcode/hooks.json\n")

			if !strings.Contains(out, ShellScopeMarker) {
				t.Fatalf("harness-state write was silent:\n%s", out)
			}
			if !strings.Contains(out, "PROTECTED") {
				t.Errorf("harness-state write not flagged as protected:\n%s", out)
			}
			events := fx.ws.ShellScopeEvents()
			if len(events) != 1 || events[0].Path != ".slmcode/hooks.json" {
				t.Fatalf("events = %+v", events)
			}
			if !events[0].Protected {
				t.Error("harness-state event must have Protected set")
			}
			if events[0].Change != "created" {
				t.Errorf("change = %q, want created", events[0].Change)
			}
			// A violation is raised the same way every other gate refusal is.
			if len(fx.seen) == 0 || fx.seen[len(fx.seen)-1] != "shell_scope_violation" {
				t.Errorf("no intervention raised: %v", fx.seen)
			}
			if lines := ShellScopeEvidenceLines(events); len(lines) != 1 ||
				!strings.HasPrefix(lines[0], ShellScopeProtectedPrefix) {
				t.Errorf("protected evidence line = %v", lines)
			}
		})
	}
}

// Agent scratch is the one writable corner of .slmcode/ and must stay quiet.
func TestShellWriteToScratchIsAllowed(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, nil)

	out := fx.runScript(t, "mkdir -p .slmcode/scratch\necho notes > .slmcode/scratch/notes.md\n")

	if strings.Contains(out, ShellScopeMarker) {
		t.Fatalf("scratch write was flagged:\n%s", out)
	}
	if got := fx.ws.ShellScopeEvents(); len(got) != 0 {
		t.Fatalf("scratch write recorded events: %+v", got)
	}
}

// The live 9B failure, reproduced: the task owned pkg/app and was told "do not
// edit, add or delete any _test.go file". main_test.go is INSIDE the focus
// package, so the focus allowlist alone can never catch it.
func TestShellWriteToProtectedPatternIsFlagged(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, []string{"*_test.go"})

	before := FileFingerprint(filepath.Join(fx.root, "pkg/app/main_test.go"))
	out := fx.runScript(t, "echo '// t.Skip()' >> pkg/app/main_test.go\n")
	after := FileFingerprint(filepath.Join(fx.root, "pkg/app/main_test.go"))
	if before == after {
		t.Fatal("fixture did not actually change the protected file")
	}

	if !strings.Contains(out, ShellScopeMarker) || !strings.Contains(out, "PROTECTED") {
		t.Fatalf("protected-file shell write was not flagged:\n%s", out)
	}
	events := fx.ws.ShellScopeEvents()
	if len(events) != 1 || events[0].Path != "pkg/app/main_test.go" || !events[0].Protected {
		t.Fatalf("events = %+v", events)
	}

	// The tool layer must refuse the same path up front — a protected path is
	// off limits to ws_edit even though it is inside the focus package.
	err := fx.ws.checkFocus(context.Background(), "pkg/app/main_test.go")
	if err == nil {
		t.Fatal("checkFocus allowed a protected path")
	}
	if !strings.Contains(err.Error(), "protected-path write blocked") {
		t.Errorf("refusal does not explain itself: %v", err)
	}
	// …while the ordinary focus file stays writable.
	if err := fx.ws.checkFocus(context.Background(), "pkg/app/main.go"); err != nil {
		t.Errorf("focus file refused: %v", err)
	}
}

// COST RULE. The snapshot must not walk the whole tree: dependency, VCS and
// generated-output directories are pruned with SkipDir, non-source extensions
// are skipped, and .slmcode/ is read one level deep.
func TestShellScopeDoesNotWalkWholeTree(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, nil)
	mk := func(rel, body string) {
		abs := filepath.Join(fx.root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Each of these is a .go / .js / .json file — i.e. it WOULD pass the
	// extension filter. Only the directory pruning keeps them out.
	pruned := []string{
		".git/objects/pack/thing.go",
		"node_modules/leftpad/index.js",
		"vendor/dep/dep.go",
		"dist/bundle.js",
		"site/index.html",
		"build/out.js",
		".venv/lib/site.py",
		"__pycache__/mod.py",
		".claude/worktrees/copy/pkg/app/main.go",
		".slmcode/queries/q1/deep.json",
		".slmcode/waves/w1/snapshot.json",
	}
	for _, p := range pruned {
		mk(p, "x")
	}
	// Non-source files at the top level are skipped by extension / noise rules.
	for _, p := range []string{"logo.png", "report.pdf", "go.sum", "server.log", "a.go.orig"} {
		mk(p, "x")
	}

	snap := fx.ws.scopeScan()
	if snap == nil {
		t.Fatal("nil snapshot")
	}
	for _, p := range pruned {
		if _, ok := snap.files[p]; ok {
			t.Errorf("walked into a pruned tree: %s is in the snapshot", p)
		}
	}
	for _, p := range []string{"logo.png", "report.pdf", "go.sum", "server.log", "a.go.orig"} {
		if _, ok := snap.files[p]; ok {
			t.Errorf("non-source file fingerprinted: %s", p)
		}
	}
	// What SHOULD be there: the project sources and go.mod.
	for _, p := range []string{"pkg/app/main.go", "pkg/app/main_test.go", "pkg/other/util.go", "go.mod"} {
		if _, ok := snap.files[p]; !ok {
			t.Errorf("source file missing from snapshot: %s", p)
		}
	}
	// 4 project files + tool.sh is absent (not written yet) + the fixed
	// .slmcode/ control-file seeds. Anything materially larger means the walk
	// is descending somewhere it should not.
	maxExpected := 4 + len(watchedControlFiles) + 2
	if snap.scanned > maxExpected {
		t.Errorf("scanned %d files, want <= %d: %v", snap.scanned, maxExpected, sortedKeys(snap.files))
	}
	if snap.truncated {
		t.Error("a 20-file tree should not truncate")
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A command that fails or is killed can still have written. The report must run
// on those paths too.
func TestShellScopeReportedOnFailedCommand(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, nil)

	out := fx.runScript(t, "echo '// tampered' >> pkg/other/util.go\nexit 3\n")

	if !strings.Contains(out, "exit error:") {
		t.Fatalf("expected a failure result: %s", out)
	}
	if !strings.Contains(out, ShellScopeMarker) {
		t.Fatalf("failed command's out-of-scope write was silent:\n%s", out)
	}
	if got := fx.paths(); len(got) != 1 || got[0] != "pkg/other/util.go" {
		t.Errorf("paths = %v", got)
	}
}

// Creations and deletions outside focus are writes too.
func TestShellScopeDetectsCreateAndDelete(t *testing.T) {
	fx := newScopeFixture(t, []string{"pkg/app/main.go"}, nil)

	fx.runScript(t, "mkdir -p pkg/brand\necho 'package brand' > pkg/brand/new.go\nrm pkg/other/util.go\n")

	events := fx.ws.ShellScopeEvents()
	kinds := map[string]string{}
	for _, e := range events {
		kinds[e.Path] = e.Change
	}
	if kinds["pkg/brand/new.go"] != "created" {
		t.Errorf("created file not reported: %v", kinds)
	}
	if kinds["pkg/other/util.go"] != "deleted" {
		t.Errorf("deleted file not reported: %v", kinds)
	}
}

func TestScopeCandidatePredicate(t *testing.T) {
	yes := []string{
		"pkg/app/main.go", "src/mod.py", "web/src/App.tsx", "config.yaml",
		"go.mod", "README.md", ".slmcode/hooks.json", ".slmcode/config.yaml",
	}
	for _, p := range yes {
		if !scopeCandidate(p) {
			t.Errorf("scopeCandidate(%q) = false, want true", p)
		}
	}
	no := []string{
		"", "go.sum", "package-lock.json", "server.log", "main.go.orig",
		"logo.png", "a.out", ".DS_Store",
		// Harness state deeper than one level: harness-written churn.
		".slmcode/queries/q1/req.json", ".slmcode/waves/w1/snapshot.json",
		// Scratch is the agent's own.
		".slmcode/scratch/notes.md",
		// Depth-1 .slmcode files the HARNESS rewrites during a run. Watching
		// these would fire on every shell call in a parallel wave.
		".slmcode/board.json", ".slmcode/TASKS.md", ".slmcode/checkpoint.json",
		".slmcode/SCRATCH.md",
	}
	for _, p := range no {
		if scopeCandidate(p) {
			t.Errorf("scopeCandidate(%q) = true, want false", p)
		}
	}
}

// Under truncation the two walks may cover different slices of the tree, so a
// path seen on one side only proves nothing. Silence beats a false accusation.
func TestDiffSuppressesCreateDeleteWhenTruncated(t *testing.T) {
	before := &scopeSnapshot{files: map[string]string{"a.go": "1:aa", "gone.go": "1:bb"}}
	after := &scopeSnapshot{files: map[string]string{"a.go": "1:cc", "new.go": "1:dd"}}

	full := diffScopeSnapshots(before, after)
	if len(full) != 3 {
		t.Fatalf("complete diff = %+v, want modified+deleted+created", full)
	}
	after.truncated = true
	partial := diffScopeSnapshots(before, after)
	if len(partial) != 1 || partial[0].path != "a.go" || partial[0].change != "modified" {
		t.Fatalf("truncated diff = %+v, want only the same-path change", partial)
	}
}

func TestFocusGuardProtectOverridesFocus(t *testing.T) {
	g := NewFocusGuard()
	g.SetWave([][]string{{"pkg/app/main.go"}})
	if !g.Allow("pkg/app/main_test.go") {
		t.Fatal("same-package file should start allowed")
	}
	g.Protect("*_test.go", "docs/frozen.md")
	if !g.HasProtections() {
		t.Fatal("HasProtections = false after Protect")
	}
	for _, p := range []string{"pkg/app/main_test.go", "main_test.go", "docs/frozen.md"} {
		if g.Allow(p) {
			t.Errorf("protected path %q still allowed", p)
		}
		if !g.IsProtected(p) {
			t.Errorf("IsProtected(%q) = false", p)
		}
	}
	if !g.Allow("pkg/app/main.go") {
		t.Error("focus file must stay writable")
	}
	if bad := g.OutOfScopeFiles([]string{"pkg/app/main.go", "pkg/app/main_test.go"}); len(bad) != 1 ||
		bad[0] != "pkg/app/main_test.go" {
		t.Errorf("OutOfScopeFiles = %v", bad)
	}
	// TrackedPaths carries the literal entries only; globs cannot be seeded.
	tracked := g.TrackedPaths()
	if len(tracked) != 2 || tracked[0] != "docs/frozen.md" || tracked[1] != "pkg/app/main.go" {
		t.Errorf("TrackedPaths = %v", tracked)
	}
	// A deny list works with no allowlist at all.
	g2 := NewFocusGuard()
	g2.Protect("secrets.yaml")
	if g2.Enabled() {
		t.Fatal("Protect must not enable the focus allowlist")
	}
	if g2.Allow("secrets.yaml") {
		t.Error("protection must hold with the allowlist disabled")
	}
	if !g2.Allow("anything/else.go") {
		t.Error("unprotected paths stay allowed when the allowlist is off")
	}
	g2.Protect()
	if g2.HasProtections() || !g2.Allow("secrets.yaml") {
		t.Error("Protect() with no arguments should clear the deny list")
	}
}

func TestFileFingerprintFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	fp := FileFingerprint(p)
	// Same shape as pkg/loop's fileFingerprint: "<len>:<sha256 hex>".
	if !strings.HasPrefix(fp, "5:") || len(fp) != 2+64 {
		t.Fatalf("fingerprint = %q", fp)
	}
	if FileFingerprint(filepath.Join(dir, "missing.txt")) != "" {
		t.Error("absent file must fingerprint as the empty string")
	}
	if err := os.WriteFile(p, []byte("hellp"), 0o600); err != nil {
		t.Fatal(err)
	}
	if FileFingerprint(p) == fp {
		t.Error("same-length different-content files must differ")
	}
}
