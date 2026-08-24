package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFocusGuardAllowAndBlock(t *testing.T) {
	g := NewFocusGuard()
	if !g.Allow("main.go") {
		t.Fatal("inactive guard should allow all")
	}
	g.SetWave([][]string{{"pkg/loop/runner.go", "pkg/loop/error_handler.go"}})
	if !g.Enabled() {
		t.Fatal("expected enabled")
	}
	if !g.Allow("pkg/loop/runner.go") {
		t.Fatal("exact focus should allow")
	}
	if !g.Allow("pkg/loop/runner_test.go") {
		t.Fatal("same package should allow")
	}
	if g.Allow("main.go") {
		t.Fatal("root main.go must be blocked")
	}
	if err := g.Check(context.Background(), "main.go"); err == nil {
		t.Fatal("expected check error for main.go")
	}
	bad := g.OutOfScopeFiles([]string{"pkg/loop/runner.go", "main.go", ".slmcode/TASKS.md"})
	if len(bad) != 1 || bad[0] != "main.go" {
		t.Fatalf("out of scope=%v", bad)
	}
	g.Clear()
	if g.Enabled() {
		t.Fatal("cleared guard should be disabled")
	}
}

func TestFocusGuardScaffoldGreenfield(t *testing.T) {
	g := NewFocusGuard()
	g.SetWave([][]string{{"pyproject.toml"}})
	if !g.Allow("pyproject.toml") {
		t.Fatal("manifest should allow")
	}
	for _, p := range []string{
		"src/lg_agent/__init__.py",
		"src/lg_agent/graph.py",
		"tests/test_graph.py",
		"README.md",
		"main.py",
	} {
		if !g.Allow(p) {
			t.Fatalf("scaffold should allow %s", p)
		}
	}
	// Unrelated wander still blocked.
	if g.Allow("vendor/secret.bin") {
		t.Fatal("unrelated path must stay blocked")
	}
}

func TestWorkspaceFocusBlocksWrite(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg/hello"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg/hello/a.go"), []byte("package hello\n"), 0o644)
	g := NewFocusGuard()
	g.SetWave([][]string{{"pkg/hello/a.go"}})
	w := &Workspace{Root: root, Focus: g}
	_, err := w.writeFile(context.Background(), map[string]interface{}{
		"path": "main.go", "content": "package main\n",
	})
	if err == nil {
		t.Fatal("expected focus block for main.go")
	}
	_, err = w.writeFile(context.Background(), map[string]interface{}{
		"path": "pkg/hello/b.go", "content": "package hello\n",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIsHarnessStatePathAndScratch(t *testing.T) {
	cases := []struct {
		path             string
		harness, scratch bool
	}{
		{".slmcode", true, false},
		{".slmcode/hooks.json", true, false},
		{".slmcode/blocks/agents/x.yaml", true, false},
		{".slmcode/scratch", true, true},
		{".slmcode/scratch/TODO.md", true, true},
		{".slmcode/scratchpad/x", true, false},
		{"pkg/a.go", false, false},
		{"./.slmcode/hooks.json", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := IsHarnessStatePath(tc.path); got != tc.harness {
				t.Fatalf("IsHarnessStatePath=%v want %v", got, tc.harness)
			}
			if got := AllowedScratchPath(tc.path); got != tc.scratch {
				t.Fatalf("AllowedScratchPath=%v want %v", got, tc.scratch)
			}
		})
	}
}

func TestFocusCheckEnforcesHarnessBoundaryEvenWhenDisabled(t *testing.T) {
	var g *FocusGuard // nil guard = enforcement disabled
	ctx := context.Background()
	if err := g.Check(ctx, ".slmcode/hooks.json"); err == nil {
		t.Fatal("the harness-state boundary is not part of the focus heuristic")
	}
	if err := g.Check(ctx, "pkg/a.go"); err != nil {
		t.Fatalf("normal paths still pass: %v", err)
	}
	if err := g.Check(ctx, ".slmcode/scratch/notes.md"); err != nil {
		t.Fatalf("scratch still passes: %v", err)
	}
}

// retryFlavored is wording that tells a model the CALL was wrong and should be
// re-attempted. Every phrase here was observed in the live run that motivated
// this file: a 9B explorer read the old refusal as an edit-syntax problem and
// spent six LLM calls and its whole task budget rewording ws_edit.
var retryFlavored = []string{
	"retry", "try again", "more context", "surrounding", "include more",
	"add context", "again with",
}

// The decisive fact for a read-only role is not which paths are in scope — it
// is that this role never writes at all. A refusal that only says "out of
// scope" invites the model to keep editing, better.
func TestExplorerToldItDoesNotEditRatherThanHowToEditBetter(t *testing.T) {
	g := NewFocusGuard()
	g.SetWave([][]string{{"stats_test.go"}})

	err := g.Check(WithRole(context.Background(), "explorer"), "stats.go")
	if err == nil {
		t.Fatal("explorer writing stats.go must be refused")
	}
	msg := err.Error()

	// It names the role, and states the contract as a fact about the role.
	if !strings.Contains(msg, "explorer") {
		t.Errorf("refusal never names the role:\n%s", msg)
	}
	if !strings.Contains(msg, "does not edit files at all") {
		t.Errorf("refusal never states that this role does not edit:\n%s", msg)
	}
	// It names the next action, and that action is to finish.
	if !strings.Contains(msg, "Next action:") || !strings.Contains(msg, "finish now") {
		t.Errorf("refusal gives no terminating next action:\n%s", msg)
	}
	// And it never suggests the fix is a better-formed edit.
	low := strings.ToLower(msg)
	for _, bad := range retryFlavored {
		if strings.Contains(low, bad) {
			t.Errorf("refusal contains retry-flavored wording %q — this is what burned the budget:\n%s", bad, msg)
		}
	}
}

// Language-specialised ids (go-explorer, python-reviewer) carry the same
// contract as the generic role they specialise.
func TestReadOnlyRoleRecognisedThroughLanguagePrefix(t *testing.T) {
	cases := map[string]bool{
		"explorer": true, "go-explorer": true, "python-reviewer": true,
		"reviewer-strict": true, "docs": true, "context": true,
		"planner": true, "splitter": true, "  Explorer  ": true,
		"worker": false, "go-worker": false, "tester": false,
		"corrector": false, "editor": false, "placeholder": false,
		"": false,
	}
	for role, want := range cases {
		if got := IsReadOnlyRole(role); got != want {
			t.Errorf("IsReadOnlyRole(%q)=%v want %v", role, got, want)
		}
	}
}

// An editing role out of its focus set keeps the old meaning — stay in the
// focus files — and gains the next action it never had.
func TestEditingRoleOutOfFocusGetsFocusFileGuidanceAndANextAction(t *testing.T) {
	g := NewFocusGuard()
	g.SetWave([][]string{{"pkg/stats/stats.go"}})

	err := g.Check(WithRole(context.Background(), "worker"), "pkg/other/util.go")
	if err == nil {
		t.Fatal("worker writing outside focus must be refused")
	}
	msg := err.Error()
	for _, want := range []string{
		"out-of-scope write blocked",
		"pkg/other/util.go",
		"the worker role",
		"task focus files",
		"Next action:",
		"make the change in a focus file",
		"planner can rescope",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in editing-role refusal:\n%s", want, msg)
		}
	}
	// The escape hatch is "say so", never "write it anyway".
	if !strings.Contains(msg, "say so in your answer") {
		t.Errorf("no rescope path offered:\n%s", msg)
	}
}

// Root entrypoints keep their own clause, and the role plumbing does not lose it.
func TestRootEntrypointRefusalKeepsItsClause(t *testing.T) {
	g := NewFocusGuard()
	g.SetWave([][]string{{"pkg/stats/stats.go"}})
	err := g.Check(context.Background(), "main.go")
	if err == nil {
		t.Fatal("root main.go must be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "root entrypoints are never created here") {
		t.Errorf("entrypoint clause lost:\n%s", msg)
	}
	// No role in ctx: the sentence still has to read correctly.
	if !strings.Contains(msg, "this task writes only") {
		t.Errorf("role-agnostic phrasing broken:\n%s", msg)
	}
}

// The review-side message follows the same contract as the tool-side one.
func TestOutOfScopeReasonIsRoleAware(t *testing.T) {
	if got := OutOfScopeReason("worker", nil); got != "" {
		t.Fatalf("no paths → no reason, got %q", got)
	}
	ro := OutOfScopeReason("explorer", []string{"stats.go"})
	if !strings.Contains(ro, "does not edit files at all") {
		t.Errorf("read-only role review message misses the contract:\n%s", ro)
	}
	rw := OutOfScopeReason("worker", []string{"main.go", "x.go"})
	if !strings.Contains(rw, "out-of-scope files_changed: main.go, x.go") {
		t.Errorf("path list lost:\n%s", rw)
	}
	if !strings.Contains(rw, "planner can rescope") {
		t.Errorf("editing-role review message gives no next action:\n%s", rw)
	}
}

// The role rides on context, exactly like the task id.
func TestWithRoleRoundTrips(t *testing.T) {
	if got := RoleFrom(context.Background()); got != "" {
		t.Fatalf("untagged ctx → %q", got)
	}
	//nolint:staticcheck // deliberately checking the nil-context guard
	if got := RoleFrom(nil); got != "" {
		t.Fatalf("nil ctx → %q", got)
	}
	//nolint:staticcheck // same: WithRole must substitute context.Background()
	if got := RoleFrom(WithRole(nil, "explorer")); got != "explorer" {
		t.Fatalf("nil ctx tagging → %q", got)
	}
	ctx := WithRole(context.Background(), "  Go-Worker  ")
	if got := RoleFrom(ctx); got != "go-worker" {
		t.Fatalf("role not normalized: %q", got)
	}
	// An empty role leaves the context alone rather than storing "".
	if got := RoleFrom(WithRole(context.Background(), "   ")); got != "" {
		t.Fatalf("blank role stored: %q", got)
	}
}

// The write path carries the role all the way from ctx to the refusal text.
func TestWorkspaceWriteRefusalNamesTheRole(t *testing.T) {
	root := t.TempDir()
	g := NewFocusGuard()
	g.SetWave([][]string{{"stats_test.go"}})
	w := &Workspace{Root: root, Focus: g}

	_, err := w.writeFile(WithRole(context.Background(), "explorer"),
		map[string]interface{}{"path": "stats.go", "content": "package x\n"})
	if err == nil {
		t.Fatal("expected the focus guard to refuse")
	}
	if !strings.Contains(err.Error(), "the explorer role does not edit files at all") {
		t.Fatalf("role never reached the tool layer:\n%s", err)
	}
}
