package quality

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestStaticQualityCatchesNotImplemented(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mod.py")
	if err := os.WriteFile(path, []byte("def foo():\n    raise NotImplementedError\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := CheckStaticQuality(root, plan.Task{Files: []string{"mod.py"}})
	if len(issues) == 0 {
		t.Fatal("expected static issue")
	}
}

func TestStaticQualityAllowsRealCode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mod.py")
	body := "def add(a, b):\n    return a + b\n\ndef mul(a, b):\n    return a * b\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := CheckStaticQuality(root, plan.Task{Files: []string{"mod.py"}})
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}

func TestFinalizeWarnAndTextTools(t *testing.T) {
	if FinalizeWarnMessage(14) == "" {
		t.Fatal("expected warn")
	}
	if FinalizeWarnMessage(2) != "" {
		t.Fatal("cap too small for warn")
	}
	if FinalizeSteerMessage(3) == "" {
		t.Fatal("expected steer")
	}
	if !ShouldFinalizeSteer(13, 16) {
		t.Fatal("13/16 is in warn band")
	}
	if ShouldFinalizeSteer(10, 16) {
		t.Fatal("10/16 is early")
	}
	names := DetectTextToolCalls("```tool\n{\"name\": \"ws_edit\"}\n```")
	if len(names) == 0 {
		t.Fatal("expected text tool detection")
	}
}

func TestSmokePassedInOutput(t *testing.T) {
	ok := "## Deterministic smoke\nPASSED\ncmd: x\n"
	fail := "## Deterministic smoke\nFAILED\ncmd: x\n"
	if !SmokePassedInOutput(ok) || SmokeFailedInOutput(ok) {
		t.Fatal("pass")
	}
	if SmokePassedInOutput(fail) || !SmokeFailedInOutput(fail) {
		t.Fatal("fail")
	}
}
