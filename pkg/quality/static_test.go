package quality

import (
	"os"
	"path/filepath"
	"strings"
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

func TestStaticQualityCatchesPlaceholderImplementation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent.py")
	body := "from langgraph import Graph\nfrom typing import Any, Dict\n\n" +
		"class BaseAgent:\n" +
		"    def run(self, input_data: Dict[str, Any]) -> Dict[str, Any]:\n" +
		"        # Placeholder implementation\n" +
		"        return {\"output\": \"run_result\"}\n" +
		"    def process(self, input_data: Dict[str, Any]) -> Dict[str, Any]:\n" +
		"        # Placeholder implementation\n" +
		"        return {\"output\": \"processed_result\"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := CheckStaticQuality(root, plan.Task{Files: []string{"agent.py"}})
	if len(issues) == 0 {
		t.Fatal("expected Placeholder implementation to fail static quality")
	}
	joined := ""
	for _, is := range issues {
		joined += is.Reason + " "
	}
	if !strings.Contains(joined, "Placeholder") && !strings.Contains(joined, "placeholder") &&
		!strings.Contains(joined, "StateGraph") && !strings.Contains(joined, "stub") {
		t.Fatalf("expected stub/bad-import reasons, got %#v", issues)
	}
}

func TestStaticQualityCatchesBadLangGraphImport(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent.py")
	body := "from langgraph import Graph\n\nclass A:\n    def build(self):\n        return Graph()\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := CheckStaticQuality(root, plan.Task{Files: []string{"agent.py"}})
	if len(issues) == 0 {
		t.Fatal("expected bad LangGraph import to fail")
	}
}

func TestIsWeakQACommand(t *testing.T) {
	if !IsWeakQACommand("python -m compileall -q .") {
		t.Fatal("compileall is weak")
	}
	if !IsWeakQACommand("python -m py_compile agent.py") {
		t.Fatal("py_compile is weak")
	}
	if IsWeakQACommand("python -m pytest -q") {
		t.Fatal("pytest is strong")
	}
	if IsWeakQACommand("go test ./... -short") {
		t.Fatal("go test is strong")
	}
	if IsWeakQACommand("python main.py") {
		t.Fatal("python main.py is strong")
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
