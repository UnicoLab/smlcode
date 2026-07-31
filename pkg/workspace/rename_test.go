package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveFileAndDelete(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg", "old.go"), []byte("package pkg\n\nfunc Hello() {}\n"), 0o644)
	w := &Workspace{Root: root}
	out, err := w.moveFile(context.Background(), map[string]interface{}{
		"from": "pkg/old.go", "to": "pkg/new.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "moved") {
		t.Fatalf("%v", out)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "old.go")); !os.IsNotExist(err) {
		t.Fatal("old path should be gone")
	}
	data, err := os.ReadFile(filepath.Join(root, "pkg", "new.go"))
	if err != nil || !strings.Contains(string(data), "Hello") {
		t.Fatalf("new file: %v %s", err, data)
	}
}

func TestMoveFileRespectsFocus(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0o644)
	g := NewFocusGuard()
	g.SetWave([][]string{{"a.go"}})
	w := &Workspace{Root: root, Focus: g}
	_, err := w.moveFile(context.Background(), map[string]interface{}{
		"from": "a.go", "to": "other/b.go",
	})
	if err == nil {
		t.Fatal("expected focus block for destination outside scope")
	}
}

func TestDeleteFile(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "tmp.go"), []byte("package tmp\n"), 0o644)
	w := &Workspace{Root: root}
	_, err := w.deleteFile(context.Background(), map[string]interface{}{"path": "tmp.go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tmp.go")); !os.IsNotExist(err) {
		t.Fatal("expected deleted")
	}
}
