package plan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRenameIntentSymbolAndFile(t *testing.T) {
	s := DetectRenameIntent("In pkg/greet/greet.go only, rename Hello to Greet")
	if s.Kind != RenameSymbol || s.OldSymbol != "Hello" || s.NewSymbol != "Greet" {
		t.Fatalf("%+v", s)
	}
	if s.OldPath != "pkg/greet/greet.go" {
		t.Fatalf("path=%q", s.OldPath)
	}
	f := DetectRenameIntent("rename pkg/foo/a.go to pkg/foo/b.go")
	if f.Kind != RenameFile || f.OldPath != "pkg/foo/a.go" || f.NewPath != "pkg/foo/b.go" {
		t.Fatalf("%+v", f)
	}
}

func TestRenameSatisfiedSymbol(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "greet.go")
	_ = os.WriteFile(path, []byte("package greet\n\nfunc Greet() string { return \"hello\" }\n"), 0o644)
	spec := RenameSpec{Kind: RenameSymbol, OldSymbol: "Hello", NewSymbol: "Greet", OldPath: "greet.go"}
	if !RenameSatisfied(root, spec, []string{"greet.go"}) {
		t.Fatal("expected satisfied")
	}
	_ = os.WriteFile(path, []byte("package greet\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	if RenameSatisfied(root, spec, []string{"greet.go"}) {
		t.Fatal("old symbol still present — should not satisfy")
	}
}

func TestRenameSatisfiedFile(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pkg", "new.go"), []byte("package pkg\n"), 0o644)
	spec := RenameSpec{Kind: RenameFile, OldPath: "pkg/old.go", NewPath: "pkg/new.go"}
	if !RenameSatisfied(root, spec, nil) {
		t.Fatal("expected file rename satisfied")
	}
	_ = os.WriteFile(filepath.Join(root, "pkg", "old.go"), []byte("package pkg\n"), 0o644)
	if RenameSatisfied(root, spec, nil) {
		t.Fatal("old still present")
	}
}

func TestEnrichTaskFilesForRename(t *testing.T) {
	task := &Task{ID: "T1", Title: "Rename Hello to Greet", Files: []string{"pkg/greet/greet.go"}}
	EnrichTaskFilesForRename(task, "rename Hello to Greet in pkg/greet/greet.go")
	if task.Acceptance == "" || !containsIdent(task.Acceptance, "Hello") {
		t.Fatalf("acceptance=%q", task.Acceptance)
	}
}
