package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

func TestWorkspaceReadWriteEditJail(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{Root: root}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := w.readFile(context.Background(), map[string]interface{}{"path": "a.go"})
	if err != nil || !strings.Contains(out.(string), "package a") {
		t.Fatalf("read=%v err=%v", out, err)
	}
	_, err = w.writeFile(context.Background(), map[string]interface{}{
		"path": "pkg/b.go", "content": "package b\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.editFile(context.Background(), map[string]interface{}{
		"path": "pkg/b.go", "old_str": "package b", "new_str": "package bee",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.readFile(context.Background(), map[string]interface{}{"path": "../outside"})
	if err == nil {
		t.Fatal("expected jail error")
	}
}

func TestRegisterCodingTools(t *testing.T) {
	reg := tools.NewToolRegistry()
	if err := RegisterCodingTools(reg, t.TempDir(), true); err != nil {
		t.Fatal(err)
	}
	for _, name := range ToolNames() {
		if _, ok := reg.GetTool(name); !ok {
			t.Fatalf("missing tool %s", name)
		}
	}
}

func TestGitToolsWithoutRepo(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{Root: root}
	out, err := w.gitStatus(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "not a git repository") {
		t.Fatalf("got %v", out)
	}
	out, err = w.gitDiff(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "not a git repository") {
		t.Fatalf("got %v", out)
	}
}
