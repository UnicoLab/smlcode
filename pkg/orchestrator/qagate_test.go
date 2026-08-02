package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
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

func TestDetectQACommandPythonPytest(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "test_x.py"), []byte("def test_ok():\n  assert True\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := detectQACommand(dir)
	if got != "python -m pytest -q" {
		t.Fatalf("got %q", got)
	}
}

func TestDetectQACommandPythonGreenfieldPytest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := detectQACommand(dir)
	if got != "python -m pytest -q" {
		t.Fatalf("got %q want pytest for main+requirements (fail closed)", got)
	}
	prep := bootstrapQADeps(dir, got)
	if !strings.Contains(prep, "requirements.txt") {
		t.Fatalf("bootstrap=%q", prep)
	}
}

func TestDetectQACommandPythonCompileallNoEntrypoint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := detectQACommand(dir)
	if got != "python -m compileall -q ." {
		t.Fatalf("got %q want compileall (no --help trap)", got)
	}
}

func TestDetectQACommandUV(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname='x'\ndependencies=[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uv.lock"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := detectQACommand(dir)
	if got != "uv run pytest -q" {
		t.Fatalf("got %q", got)
	}
}
