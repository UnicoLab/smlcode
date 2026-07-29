package contextstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedProjectMarkdownFromGoMod(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Demo\n\nA tiny demo harness.\n"), 0o644)

	md := SeedProjectMarkdown(dir, "demo")
	if !strings.Contains(md, "# Project: demo") {
		t.Fatalf("missing title: %s", md)
	}
	if !strings.Contains(md, "tiny demo") && !strings.Contains(md, "example.com/demo") {
		t.Fatalf("overview not seeded: %s", md)
	}
	if !strings.Contains(md, "| pkg/ |") {
		t.Fatalf("key paths missing pkg/: %s", md)
	}
	if ProjectNeedsSeed(md) {
		t.Fatal("seeded project should not need seed")
	}
}

func TestProjectNeedsSeedScaffold(t *testing.T) {
	scaffold := "# Project: x\n\n## Overview\n\n\n\n## Conventions\n\n\n\n## Key paths\n\n| Path | Role |\n|------|------|\n| | |\n"
	if !ProjectNeedsSeed(scaffold) {
		t.Fatal("expected scaffold to need seed")
	}
}

func TestMergeProjectSectionsFillsEmpty(t *testing.T) {
	existing := "# Project: x\n\n## Overview\n\n\n\n## Conventions\n\nkeep me\n\n## Key paths\n\n| Path | Role |\n|------|------|\n| | |\n"
	seeded := "# Project: x\n\n## Overview\n\nFilled overview\n\n## Conventions\n\n- conv\n\n## Key paths\n\n| Path | Role |\n|------|------|\n| cmd/ | CLI |\n"
	out := MergeProjectSections(existing, seeded)
	if !strings.Contains(out, "Filled overview") {
		t.Fatalf("overview not filled: %s", out)
	}
	if !strings.Contains(out, "keep me") {
		t.Fatalf("conventions wiped: %s", out)
	}
	if !strings.Contains(out, "cmd/") {
		t.Fatalf("key paths not filled: %s", out)
	}
}
