package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteGuardRefusesExisting(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, WriteGuard: true, Reads: NewReadTracker()}
	out, err := w.writeFile(context.Background(), map[string]interface{}{
		"path": "a.go", "content": "package overwritten\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := out.(string)
	if !strings.Contains(msg, "Write refused") || !strings.Contains(msg, "ws_edit") {
		t.Fatalf("expected refuse recipe, got %q", msg)
	}
	data, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(data) != "package a\n" {
		t.Fatalf("file was overwritten: %q", data)
	}
}

func TestWriteGuardAllowsNewFile(t *testing.T) {
	root := t.TempDir()
	w := &Workspace{Root: root, WriteGuard: true, Reads: NewReadTracker()}
	_, err := w.writeFile(context.Background(), map[string]interface{}{
		"path": "pkg/b.go", "content": "package b\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !w.Reads.Has("pkg/b.go") {
		t.Fatal("write should mark file as read/authored")
	}
}

func TestReadBeforeEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, ReadBeforeEdit: true, Reads: NewReadTracker()}
	out, err := w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "package a", "new_str": "package bee",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "must be read first") {
		t.Fatalf("expected read-before-edit, got %v", out)
	}
	_, err = w.readFile(context.Background(), map[string]interface{}{"path": "a.go"})
	if err != nil {
		t.Fatal(err)
	}
	out, err = w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "package a", "new_str": "package bee",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "edited") {
		t.Fatalf("expected edit success, got %v", out)
	}
}

func TestEditNotFoundRecovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, ReadBeforeEdit: true, Reads: NewReadTracker()}
	w.Reads.Mark("a.go")
	out, err := w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "wrong", "new_str": "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "RECOVERY") {
		t.Fatalf("expected recovery guidance, got %v", out)
	}
}

func TestReservedDeviceName(t *testing.T) {
	if !IsReservedDeviceName("nul") || !IsReservedDeviceName("COM1.txt") {
		t.Fatal("expected reserved")
	}
	if IsReservedDeviceName("main.go") {
		t.Fatal("main.go is not reserved")
	}
}

func TestNormalizeWritePath(t *testing.T) {
	root := "/tmp/proj"
	got, from := NormalizeWritePath("/foo.md", root)
	if from != "/foo.md" || !strings.HasSuffix(got, "foo.md") {
		t.Fatalf("got %q from %q", got, from)
	}
}

func TestShellWriteGuardBlocksCatRedirect(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, ShellWriteGuard: true}
	out, err := w.shell(context.Background(), map[string]interface{}{
		"command": "cat > main.py <<'EOF'\nprint(2)\nEOF",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "shell write refused") {
		t.Fatalf("expected shell refuse, got %v", out)
	}
	data, _ := os.ReadFile(filepath.Join(root, "main.py"))
	if string(data) != "print(1)\n" {
		t.Fatalf("file clobbered: %q", data)
	}
}

func TestShellAllowsDevNull(t *testing.T) {
	targets := DetectWriteTargets("echo hi 2>/dev/null")
	if len(targets) != 0 {
		t.Fatalf("dev/null should be ignored, got %#v", targets)
	}
}

func TestDetectWriteTargetsTee(t *testing.T) {
	targets := DetectWriteTargets("echo x | tee out.txt")
	if len(targets) != 1 || targets[0].Path != "out.txt" {
		t.Fatalf("got %#v", targets)
	}
}

func TestReadNumberedAndOffset(t *testing.T) {
	root := t.TempDir()
	body := "line1\nline2\nline3\nline4\n"
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, Reads: NewReadTracker()}
	out, err := w.readFile(context.Background(), map[string]interface{}{
		"path": "a.txt", "offset": 2, "limit": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := out.(string)
	if !strings.Contains(s, "2|line2") || !strings.Contains(s, "3|line3") {
		t.Fatalf("expected numbered slice, got %q", s)
	}
	if strings.Contains(s, "line1") && strings.Contains(s, "1|line1") {
		t.Fatalf("offset should skip line1: %q", s)
	}
}

func TestFuzzyEditHint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package demo\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{Root: root, ReadBeforeEdit: true, Reads: NewReadTracker()}
	w.Reads.Mark("a.go")
	out, err := w.editFile(context.Background(), map[string]interface{}{
		"path": "a.go", "old_str": "func Hello( ) {}", "new_str": "func Hello() string { return \"hi\" }",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.(string), "Closest matching") {
		t.Fatalf("expected fuzzy hint, got %v", out)
	}
}
