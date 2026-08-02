package quality

import (
	"context"
	"os"
	"path/filepath"
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
