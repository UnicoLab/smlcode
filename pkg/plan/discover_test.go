package plan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverRelevantFiles(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	files := DiscoverRelevantFiles(root, "Add doc comment to Hello()", "nothing useful")
	if len(files) == 0 {
		t.Fatal("expected hello.go discovery")
	}
	found := false
	for _, f := range files {
		if f == "hello.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("files=%v", files)
	}
}
