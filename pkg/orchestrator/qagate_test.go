package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectQACommandGo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := detectQACommand(dir)
	if got != "go test ./... -short" {
		t.Fatalf("got %q", got)
	}
}

func TestDetectQACommandEmpty(t *testing.T) {
	if detectQACommand(t.TempDir()) != "" {
		t.Fatal("expected empty")
	}
}
