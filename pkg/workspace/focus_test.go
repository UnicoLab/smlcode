package workspace

import (
	"context"
	"os"
	"path/filepath"
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
	if err := g.Check("main.go"); err == nil {
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
