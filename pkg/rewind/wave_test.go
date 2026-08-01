package rewind

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRestore(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	_ = os.MkdirAll(slm, 0o755)
	_ = os.WriteFile(filepath.Join(root, "a.py"), []byte("x=1\n"), 0o644)
	mgr := &Manager{SlmDir: slm, Root: root}
	snap, err := mgr.SnapshotPaths("turn1", 1, []string{"T1"}, []string{"a.py"})
	if err != nil || snap == nil {
		t.Fatalf("%v %+v", err, snap)
	}
	_ = os.WriteFile(filepath.Join(root, "a.py"), []byte("broken\n"), 0o644)
	n, err := mgr.Restore(snap.ID)
	if err != nil || n != 1 {
		t.Fatalf("restore n=%d err=%v", n, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.py"))
	if string(data) != "x=1\n" {
		t.Fatalf("got %q", data)
	}
}
