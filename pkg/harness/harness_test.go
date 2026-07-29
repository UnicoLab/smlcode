package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

func TestOpenWorkspaceAndInit(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n"), 0o644)
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	ws, err := OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.EnsureInitialized(); err != nil {
		t.Fatal(err)
	}
	if ws.Config.Root != root {
		t.Fatalf("root=%s", ws.Config.Root)
	}
	if ws.Board == nil || ws.Store == nil {
		t.Fatal("missing board/store")
	}
}

func TestHarnessStatus(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\n"), 0o644)
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	h, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg
	st, err := h.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "Root:") {
		t.Fatalf("status=%s", st)
	}
}

func TestClip(t *testing.T) {
	if clip("hi", 10) != "hi" {
		t.Fatal("short")
	}
	got := clip(strings.Repeat("x", 100), 10)
	if !strings.Contains(got, "truncated") {
		t.Fatalf("got %q", got)
	}
}
