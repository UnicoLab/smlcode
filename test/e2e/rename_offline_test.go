package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Offline rename acceptance: symbol + file rename recognized without LLM.
func TestOfflineRenameAcceptance(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg", "greet"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg", "greet", "greet.go"),
		[]byte("package greet\n\nfunc Greet() string { return \"hello\" }\n"), 0o644)

	spec := plan.DetectRenameIntent("In pkg/greet/greet.go rename Hello to Greet")
	if spec.Kind != plan.RenameSymbol {
		t.Fatalf("spec=%+v", spec)
	}
	if !plan.RenameSatisfied(root, spec, []string{"pkg/greet/greet.go"}) {
		t.Fatal("symbol rename should be satisfied on disk")
	}

	_ = os.WriteFile(filepath.Join(root, "pkg", "greet", "old.go"), []byte("package greet\n"), 0o644)
	from := filepath.Join(root, "pkg", "greet", "old.go")
	to := filepath.Join(root, "pkg", "greet", "renamed.go")
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	fspec := plan.DetectRenameIntent("rename pkg/greet/old.go to pkg/greet/renamed.go")
	if fspec.Kind != plan.RenameFile {
		t.Fatalf("%+v", fspec)
	}
	if !plan.RenameSatisfied(root, fspec, nil) {
		t.Fatal("file rename should be satisfied")
	}
}
