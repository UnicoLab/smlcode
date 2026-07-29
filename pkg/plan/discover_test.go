package plan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverRelevantFiles(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg/greet"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg/greet/greet.go"), []byte("package greet\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	files := DiscoverRelevantFiles(root, "Add doc comment to greet.Hello() in pkg/greet/greet.go", "see also main.go")
	if len(files) == 0 {
		t.Fatal("expected greet.go discovery")
	}
	for _, f := range files {
		if f == "main.go" {
			t.Fatalf("must not advertise missing main.go: %v", files)
		}
	}
	found := false
	for _, f := range files {
		if f == "pkg/greet/greet.go" || f == "greet.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("files=%v", files)
	}
}
