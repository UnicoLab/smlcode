package quality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestScanProjectPlaceholdersFindsStubs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "src")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "class A:\n    def run(self):\n        # Placeholder implementation\n        return {\"output\": \"run_result\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "agent.py"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gaps := ScanProjectPlaceholders(root, &plan.Board{Tasks: []plan.Task{{
		Files: []string{"src/agent.py"},
	}}})
	if len(gaps) == 0 {
		t.Fatal("expected placeholder gaps")
	}
	rep := FormatPlaceholderReport(gaps)
	if !strings.Contains(rep, "Placeholder gaps") || !strings.Contains(rep, "src/agent.py") {
		t.Fatalf("report=%q", rep)
	}
}
