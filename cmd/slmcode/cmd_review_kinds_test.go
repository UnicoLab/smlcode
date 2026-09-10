package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKindFixture(t *testing.T, slmDir, name string, obj map[string]string) {
	t.Helper()
	dir := filepath.Join(slmDir, "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// `slmcode apply --all` used to write every entry's content to its path:
// a delete became a 0-byte file, a move left its source, a shell ask became
// a literal shell.sh. Each kind now does what it says.
func TestWritePatchHonorsKind(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")

	// delete
	if err := os.WriteFile(filepath.Join(root, "gone.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePatch(root, pendingPatch{Path: "gone.go", Kind: "delete"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone.go")); !os.IsNotExist(err) {
		t.Fatalf("delete left the file (err=%v)", err)
	}
	// deleting an already-missing file is not an error
	if err := writePatch(root, pendingPatch{Path: "gone.go", Kind: "delete"}); err != nil {
		t.Fatalf("second delete: %v", err)
	}

	// mv
	if err := os.MkdirAll(filepath.Join(root, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "old", "a.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // executable fixture
		t.Fatal(err)
	}
	if err := writePatch(root, pendingPatch{Path: "new/a.sh", Kind: "mv", From: "old/a.sh", Content: "#!/bin/sh\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old", "a.sh")); !os.IsNotExist(err) {
		t.Fatalf("mv left the source (err=%v)", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "new", "a.sh")); err != nil || string(got) != "#!/bin/sh\n" {
		t.Fatalf("mv destination: %q err=%v", got, err)
	}
	// mv whose source vanished falls back to the recorded content
	if err := writePatch(root, pendingPatch{Path: "new/b.go", Kind: "mv", From: "old/b.go", Content: "package b\n"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "new", "b.go")); string(got) != "package b\n" {
		t.Fatalf("mv fallback: %q", got)
	}
	// mv onto an existing destination is refused
	if err := writePatch(root, pendingPatch{Path: "new/b.go", Kind: "mv", From: "new/a.sh", Content: "x"}); err == nil {
		t.Fatal("mv over an existing destination was allowed")
	}

	// shell: never a file
	err := writePatch(root, pendingPatch{Path: "shell.sh", Kind: "shell", Content: "rm -rf /"})
	if err == nil || !strings.Contains(err.Error(), "shell") {
		t.Fatalf("shell entry applied: err=%v", err)
	}
	if _, serr := os.Stat(filepath.Join(root, "shell.sh")); serr == nil {
		t.Fatal("shell.sh written to the project root")
	}

	// loadPending hides stale shell mirrors entirely, and surfaces from/stamps.
	writeKindFixture(t, slm, "1000_shell_shell.sh.patch.json", map[string]string{"path": "shell.sh", "kind": "shell", "content": "ls"})
	writeKindFixture(t, slm, "2000_mv_x.patch.json", map[string]string{"path": "n.go", "kind": "mv", "from": "o.go", "content": "p", "task_id": "T1", "agent": "worker"})
	got, err := loadPending(slm)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != "mv" || got[0].From != "o.go" || got[0].TaskID != "T1" || got[0].Agent != "worker" {
		t.Fatalf("loadPending = %+v", got)
	}

	// The diff of a delete is everything removed; of a move, source vs content.
	if err := os.WriteFile(filepath.Join(root, "d.go"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fd := pendingPatch{Path: "d.go", Kind: "delete"}.diff(root)
	if fd.Removed != 2 || fd.Added != 0 {
		t.Fatalf("delete diff: +%d -%d", fd.Added, fd.Removed)
	}
	fd = pendingPatch{Path: "moved.go", Kind: "mv", From: "d.go", Content: "a\nb\n"}.diff(root)
	if fd.Removed != 0 || fd.Added != 0 {
		t.Fatalf("mv diff should be empty for unchanged content: +%d -%d", fd.Added, fd.Removed)
	}
}
