package quality

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestClaimsGateHallucinatedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.py"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := plan.Task{
		Output: `{"status":"done","files_changed":["real.py","ghost.py"],"summary":"ok"}`,
	}
	issues := CheckClaimedFiles(root, task)
	if len(issues) != 1 || issues[0].Path != "ghost.py" {
		t.Fatalf("got %#v", issues)
	}
	sec := FormatClaimsSection(issues)
	if !ClaimsFailedInOutput(sec) {
		t.Fatal("section marker missing")
	}
}

func TestClaimsGateOK(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.py"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := plan.Task{
		Output: `{"status":"done","files_changed":["real.py"],"summary":"ok"}`,
	}
	if issues := CheckClaimedFiles(root, task); len(issues) != 0 {
		t.Fatalf("unexpected %#v", issues)
	}
}
