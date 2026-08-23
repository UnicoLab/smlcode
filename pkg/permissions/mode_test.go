package permissions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeAndPending(t *testing.T) {
	if Normalize("ASK") != ModeReview {
		t.Fatal(Normalize("ASK"))
	}
	if Normalize("dryrun") != ModeDryRun {
		t.Fatal(Normalize("dryrun"))
	}
	dir := t.TempDir()
	p, err := RecordPending(dir, "hello.go", "write", "package main\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != filepath.Join(dir, "pending") {
		t.Fatal(p)
	}
}

func TestNormalizeShell(t *testing.T) {
	if NormalizeShell("") != ShellAllow {
		t.Fatal("default allow")
	}
	if NormalizeShell("ASK") != ShellAsk {
		t.Fatal(NormalizeShell("ASK"))
	}
	if NormalizeShell("block") != ShellDeny {
		t.Fatal(NormalizeShell("block"))
	}
}

// A model-supplied path must never escape the pending queue directory, on any
// separator convention, and must never blow past NAME_MAX.
func TestAdvRecordPendingNameIsContained(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{
		"../../etc/passwd", `..\..\windows\system32\x`, "a/b/c.go",
		strings.Repeat("x", 5000) + ".go", "\x00evil", "..", ".", "",
		"a\nb.go", "$(id).go", "a b.go",
	} {
		full, err := RecordPending(dir, p, "write", "content")
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if got := filepath.Dir(full); got != filepath.Join(dir, "pending") {
			t.Errorf("QUEUE ESCAPE for %q -> %q", p, full)
		}
		if base := filepath.Base(full); len(base) > 255 {
			t.Errorf("name too long for %q: %d bytes", p, len(base))
		}
		if _, serr := os.Stat(full); serr != nil {
			t.Errorf("%q: queue file not written: %v", p, serr)
		}
	}
	// The kind field is interpolated too.
	full, err := RecordPending(dir, "a.go", "../../evil", "x")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(full) != filepath.Join(dir, "pending") {
		t.Errorf("QUEUE ESCAPE via kind -> %q", full)
	}
}
