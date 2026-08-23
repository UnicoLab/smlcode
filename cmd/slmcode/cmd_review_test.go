package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/cli"
)

func writePendingFixture(t *testing.T, slmDir, name, path, content string) {
	t.Helper()
	dir := filepath.Join(slmDir, "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"path": path, "kind": "write", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPendingSortsChronologically(t *testing.T) {
	slm := t.TempDir()
	writePendingFixture(t, slm, "2000_write_b.patch.json", "b.go", "b\n")
	writePendingFixture(t, slm, "1000_write_a.patch.json", "a.go", "a\n")
	writePendingFixture(t, slm, "not-a-patch.txt", "c.go", "c\n")

	got, err := loadPending(slm)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 patches, got %d", len(got))
	}
	if got[0].Path != "a.go" || got[1].Path != "b.go" {
		t.Fatalf("wrong order: %v", []string{got[0].Path, got[1].Path})
	}
}

func TestLoadPendingMissingDirIsEmpty(t *testing.T) {
	got, err := loadPending(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v err=%v", got, err)
	}
}

func TestLoadPendingSkipsMalformed(t *testing.T) {
	slm := t.TempDir()
	dir := filepath.Join(slm, "pending")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "1_write_x.patch.json"), []byte("{not json"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "2_write_y.patch.json"), []byte(`{"path":"","content":"x"}`), 0o644)
	got, _ := loadPending(slm)
	if len(got) != 0 {
		t.Fatalf("malformed patches must be skipped, got %v", got)
	}
}

// TestWritePatchPreservesExecutableBit is the regression for the hard-coded
// 0o644: overwriting a script used to silently strip its +x.
func TestWritePatchPreservesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	root := t.TempDir()
	script := filepath.Join(root, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := pendingPatch{Path: "run.sh", Content: "#!/bin/sh\necho new\n"}
	if err := writePatch(root, p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable bit lost: %v", st.Mode().Perm())
	}
	data, _ := os.ReadFile(script)
	if !strings.Contains(string(data), "echo new") {
		t.Fatalf("content not written: %q", data)
	}
}

func TestWritePatchCreatesParentDirs(t *testing.T) {
	root := t.TempDir()
	p := pendingPatch{Path: "deep/nested/new.md", Content: "hi\n"}
	if err := writePatch(root, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "deep", "nested", "new.md")); err != nil {
		t.Fatal(err)
	}
}

func TestPendingPatchDiffAgainstDisk(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := pendingPatch{Path: "x.go", Content: "a\nB\n"}
	fd := p.diff(root)
	if fd.Added != 1 || fd.Removed != 1 {
		t.Fatalf("stat=%s", fd.Stat())
	}
	if fd.IsNew {
		t.Fatal("an existing file must not diff as new")
	}
}

func TestPendingPatchDiffNewFile(t *testing.T) {
	p := pendingPatch{Path: "brand/new.md", Content: "# hi\n"}
	fd := p.diff(t.TempDir())
	if !fd.IsNew || fd.Removed != 0 {
		t.Fatalf("%+v", fd)
	}
}

func TestFilterPatchesByPrefix(t *testing.T) {
	in := []pendingPatch{{Path: "pkg/cli/a.go"}, {Path: "pkg/loop/b.go"}, {Path: "main.go"}}
	got := filterPatches(in, []string{"pkg/cli"})
	if len(got) != 1 || got[0].Path != "pkg/cli/a.go" {
		t.Fatalf("got %v", got)
	}
	if len(filterPatches(in, nil)) != 3 {
		t.Fatal("no prefixes means everything")
	}
	if len(filterPatches(in, []string{"./main.go"})) != 1 {
		t.Fatal("./ prefix should be normalized away")
	}
}

func TestDropPatchRemovesTheProposal(t *testing.T) {
	slm := t.TempDir()
	writePendingFixture(t, slm, "1_write_a.patch.json", "a.go", "a\n")
	got, _ := loadPending(slm)
	if err := dropPatch(slm, got[0]); err != nil {
		t.Fatal(err)
	}
	after, _ := loadPending(slm)
	if len(after) != 0 {
		t.Fatalf("patch not removed: %v", after)
	}
}

func TestApplyAllAppliesEverything(t *testing.T) {
	cli.SetColorMode(cli.ColorNever)
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	writePendingFixture(t, slm, "1_write_a.patch.json", "a.go", "package a\n")
	writePendingFixture(t, slm, "2_write_b.patch.json", "sub/b.go", "package b\n")
	patches, _ := loadPending(slm)

	if err := applyAll(slm, root, patches); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a.go", "sub/b.go"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("%s not written: %v", rel, err)
		}
	}
	left, _ := loadPending(slm)
	if len(left) != 0 {
		t.Fatalf("applied patches must be consumed: %v", left)
	}
}

func TestMaxDiffPreview(t *testing.T) {
	if maxDiffPreview(50, false) != 38 {
		t.Fatalf("got %d", maxDiffPreview(50, false))
	}
	if maxDiffPreview(10, false) != 20 {
		t.Fatal("tiny terminals still get a usable minimum")
	}
	if maxDiffPreview(50, true) != 0 {
		t.Fatal("--no-pager means unlimited")
	}
}

func TestSplitLinesNonEmpty(t *testing.T) {
	got := splitLinesNonEmpty("a\n\n  b  \nc\n")
	if len(got) != 3 || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestMatchesAnyPrefix(t *testing.T) {
	if !matchesAnyPrefix("pkg/cli/x.go", []string{"pkg/cli"}) {
		t.Fatal("expected a match")
	}
	if matchesAnyPrefix("cmd/x.go", []string{"pkg"}) {
		t.Fatal("unexpected match")
	}
}
