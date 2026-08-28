package plan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFiles(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The regression this exists for. "Build a Go backend serving a React frontend"
// targets cmd/server/main.go and web/src/App.tsx — the conventional layouts for
// exactly that request. Neither starts with src/, tests/, lib/ or app/, so every
// task the splitter wrote was parked as unscoped and the run produced nothing.
func TestGreenfieldKeepsConventionalLayouts(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "go.mod") // manifest only: still greenfield

	claimed := []string{
		"cmd/server/main.go",
		"internal/store/todo.go",
		"web/src/App.tsx",
		"web/package.json",
	}
	got := ReconcileFiles(root, claimed, nil)
	if !reflect.DeepEqual(got, claimed) {
		t.Fatalf("a greenfield repo must keep its own conventional targets:\n got %v\nwant %v", got, claimed)
	}
}

// The looser rule is confined to the greenfield state. Once a repository has
// source in it, a claimed path that does not exist is far more likely invented
// than intended — the existing contract.
func TestAnExistingRepoStaysConservative(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "main.go", "helper.go")

	// Same claim as above, but now there is code to reconcile against.
	got := ReconcileFiles(root, []string{"cmd/server/main.go"}, []string{"main.go"})
	if !reflect.DeepEqual(got, []string{"main.go"}) {
		t.Fatalf("an established repo must fall back to discovered files, got %v", got)
	}
	// src/ stays blessed everywhere, greenfield or not.
	if got := ReconcileFiles(root, []string{"src/new.go"}, nil); !reflect.DeepEqual(got, []string{"src/new.go"}) {
		t.Errorf("src/ must stay allowed in an established repo, got %v", got)
	}
}

func TestIsGreenfieldRoot(t *testing.T) {
	empty := t.TempDir()
	if !isGreenfieldRoot(empty) {
		t.Error("an empty directory is greenfield")
	}

	manifests := t.TempDir()
	writeFiles(t, manifests, "go.mod", "package.json", "README.md")
	if !isGreenfieldRoot(manifests) {
		t.Error("manifests alone are a project about to be written, not one to reconcile against")
	}

	withSource := t.TempDir()
	writeFiles(t, withSource, "go.mod", "main.go")
	if isGreenfieldRoot(withSource) {
		t.Error("a repo with source is not greenfield")
	}

	if !isGreenfieldRoot("") {
		t.Error("no root at all is greenfield")
	}
}

// A model describing a file rather than naming one must still be refused, or
// the looser greenfield rule becomes a write allowlist for anything.
func TestLooksLikeSourceTargetRejectsDescriptions(t *testing.T) {
	good := []string{
		"cmd/server/main.go", "web/src/App.tsx", "internal/a/b/c.go",
		"Dockerfile.dockerfile", "config.yaml", "api/schema.json", "docs/readme.md",
	}
	for _, f := range good {
		if !looksLikeSourceTarget(f) {
			t.Errorf("%q should be a plausible target", f)
		}
	}
	bad := []string{
		"", "/etc/passwd", "../escape.go", "path/to/file.go", "src/<name>.go",
		"your-app/main.go", "example/main.go", "placeholder.go",
		"a/b/c/d/e/f/g/h/i.go", // eight levels deep is a description
		"cmd/server",           // no extension
		"logo.png", "archive.zip", "binary.exe",
	}
	for _, f := range bad {
		if looksLikeSourceTarget(f) {
			t.Errorf("%q should NOT be accepted as a target", f)
		}
	}
}

// Even greenfield, an invented path with no real extension is refused rather
// than becoming scope.
func TestGreenfieldStillRefusesNonSourceClaims(t *testing.T) {
	root := t.TempDir()
	got := ReconcileFiles(root, []string{"path/to/your/file.go", "assets/logo.png"}, nil)
	if len(got) != 0 {
		t.Fatalf("placeholders and binaries must not become scope, got %v", got)
	}
}

// The two predicates answer different questions and must not be conflated: a
// README may be created by a task, but its presence says nothing about where
// this project puts its code.
func TestDocsAndConfigDoNotMakeARepoEstablished(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "README.md", "config.yaml", ".gitignore", "go.mod")
	if !isGreenfieldRoot(root) {
		t.Fatal("docs and config alone leave a repository greenfield")
	}
	// …but each of them is still something a task may legitimately create.
	for _, f := range []string{"README.md", "config.yaml"} {
		if !looksLikeSourceTarget(f) {
			t.Errorf("%q should still be creatable", f)
		}
	}
	// One real source file settles it.
	writeFiles(t, root, "main.go")
	if isGreenfieldRoot(root) {
		t.Fatal("one source file makes a repository established")
	}
}
