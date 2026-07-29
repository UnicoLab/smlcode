package permissions

import (
	"os"
	"path/filepath"
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
