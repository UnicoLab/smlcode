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

func TestDetectProjectCommandPythonCompileall(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.py"), []byte("print('hi')\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "requirements.txt"), []byte("# none\n"), 0o644)
	cmd := DetectProjectCommand(root)
	if cmd != "python -m compileall -q ." {
		t.Fatalf("got %q", cmd)
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
