package quality

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestRunPostWorkerSmokePythonCompile(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "ok.py")
	bad := filepath.Join(root, "bad.py")
	if err := os.WriteFile(good, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("def broken(\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ok := RunPostWorkerSmoke(ctx, root, plan.Task{
		Role: plan.RoleWorker, Files: []string{"ok.py"},
	}, time.Minute)
	if !ok.OK || !ok.Ran {
		t.Fatalf("good file: %+v", ok)
	}
	fail := RunPostWorkerSmoke(ctx, root, plan.Task{
		Role: plan.RoleWorker, Files: []string{"bad.py"},
	}, time.Minute)
	if fail.OK || !fail.Ran {
		t.Fatalf("bad file should fail: %+v", fail)
	}
	sec := FormatSmokeSection(fail)
	if !SmokeFailedInOutput(sec) {
		t.Fatalf("section=%q", sec)
	}
}

func TestDetectProjectCommandGreenfieldPrefersPytest(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.py"), []byte("print('hi')\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "requirements.txt"), []byte("# none\n"), 0o644)
	cmd := DetectProjectCommand(root)
	if cmd != "python -m pytest -q" {
		t.Fatalf("greenfield main+requirements should fail-closed on pytest, got %q", cmd)
	}
}

func TestDetectProjectCommandPythonCompileallNoEntrypoint(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "lib.py"), []byte("x = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "requirements.txt"), []byte("# none\n"), 0o644)
	cmd := DetectProjectCommand(root)
	if cmd != "python -m compileall -q ." {
		t.Fatalf("got %q", cmd)
	}
}

func TestExtractAcceptanceCommands(t *testing.T) {
	cmds := ExtractAcceptanceCommands("python -m pytest tests/ -q exits 0; agent imports; python main.py prints hello")
	if len(cmds) < 2 {
		t.Fatalf("got %#v", cmds)
	}
	if cmds[0] != "python -m pytest tests/ -q" {
		t.Fatalf("pytest cmd: %q", cmds[0])
	}
	foundMain := false
	for _, c := range cmds {
		if c == "python main.py" {
			foundMain = true
		}
	}
	if !foundMain {
		t.Fatalf("missing main.py: %#v", cmds)
	}
	// Must not extract free-form prose as shell.
	if got := ExtractAcceptanceCommands("files exist and contain real code"); len(got) != 0 {
		t.Fatalf("prose should yield no cmds: %#v", got)
	}
}

func TestDetectProjectCommandPytest(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "tests"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "tests", "test_x.py"), []byte("def test_ok():\n    assert True\n"), 0o644)
	cmd := DetectProjectCommand(root)
	if cmd != "python -m pytest -q" {
		t.Fatalf("got %q", cmd)
	}
}

func TestShouldSmokeTask(t *testing.T) {
	if !ShouldSmokeTask(plan.Task{Role: plan.RoleWorker, Files: []string{"a.py"}}) {
		t.Fatal("worker py")
	}
	if ShouldSmokeTask(plan.Task{Role: plan.RoleTester, Files: []string{"a.py"}}) {
		t.Fatal("tester skip")
	}
}

func TestDetectPostWorkerCommandGoNoModuleUsesGofmt(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	cmd := DetectPostWorkerCommand(root, []string{"hello.go"})
	if cmd != "gofmt -e hello.go" {
		t.Fatalf("no-module Go should use gofmt -e, got %q", cmd)
	}
}

func TestDetectPostWorkerCommandGoModuleUsesGoTest(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	cmd := DetectPostWorkerCommand(root, []string{"hello.go"})
	if cmd != "go test . -short" {
		t.Fatalf("module Go should use go test, got %q", cmd)
	}
}

func TestDetectPostWorkerCommandTypeScriptUsesExplicitPackageScript(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"lint":"tsc --noEmit"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(root, "src.ts"), []byte("export const x: number = 1\n"), 0o644)
	cmd := DetectPostWorkerCommand(root, []string{"src.ts"})
	if cmd != "npm run -s lint" {
		t.Fatalf("TypeScript smoke should use explicit tsc script, got %q", cmd)
	}
}

func TestDetectPostWorkerCommandTypeScriptUsesLocalTSC(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{}}`), 0o644)
	_ = os.WriteFile(filepath.Join(bin, "tsc"), []byte("#!/bin/sh\n"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "component.tsx"), []byte("export function C() { return null }\n"), 0o644)
	cmd := DetectPostWorkerCommand(root, []string{"component.tsx"})
	if cmd != "./node_modules/.bin/tsc --noEmit --pretty false" {
		t.Fatalf("TypeScript smoke should use local tsc, got %q", cmd)
	}
}

func TestDetectPostWorkerCommandTypeScriptWithoutToolingIsEmpty(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"lint":"eslint ."}}`), 0o644)
	_ = os.WriteFile(filepath.Join(root, "src.ts"), []byte("export const x = 1\n"), 0o644)
	cmd := DetectPostWorkerCommand(root, []string{"src.ts"})
	if cmd != "" {
		t.Fatalf("TypeScript smoke should not invent tooling, got %q", cmd)
	}
}

func TestDetectProjectLanguageHTMLAndGoScript(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "index.html"), []byte("<html></html>"), 0o644)
	if got := DetectProjectLanguage(root); got != "html" {
		t.Fatalf("html workspace got %q", got)
	}

	rootGo := t.TempDir()
	_ = os.WriteFile(filepath.Join(rootGo, "main.go"), []byte("package main\n"), 0o644)
	if got := DetectProjectLanguage(rootGo); got != "go" {
		t.Fatalf("lone .go file got %q, want go", got)
	}
}

func TestRunPostWorkerSmokeGoVetStripsTestFlags(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package hello\n\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	sr := RunPostWorkerSmoke(context.Background(), root, plan.Task{
		Role: plan.RoleWorker, Files: []string{"hello.go"},
	}, time.Minute)
	if strings.Contains(sr.Command, "-short") || strings.Contains(sr.Command, "-race") ||
		strings.Contains(sr.Command, "-count") {
		t.Fatalf("go vet command must not carry test flags: %q", sr.Command)
	}
	if !strings.HasPrefix(sr.Command, "go vet ") {
		t.Fatalf("expected go vet smoke, got %q", sr.Command)
	}
}
