package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileCheckpointerFirstWriteWins(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	target := filepath.Join(root, "a.go")
	if err := os.WriteFile(target, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := NewFileCheckpointer(slm, root, "sess1")
	cp.BackupIfNeeded("a.go")
	if err := os.WriteFile(target, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp.BackupIfNeeded("a.go") // should not overwrite backup
	if err := cp.Restore("a.go"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "v1\n" {
		t.Fatalf("got %q", got)
	}
}

func TestFileCheckpointerAbsentSentinel(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	cp := NewFileCheckpointer(slm, root, "sess2")
	cp.BackupIfNeeded("new.py")
	_ = os.WriteFile(filepath.Join(root, "new.py"), []byte("x"), 0o644)
	if err := cp.Restore("new.py"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.py")); !os.IsNotExist(err) {
		t.Fatal("expected file removed on absent restore")
	}
}
