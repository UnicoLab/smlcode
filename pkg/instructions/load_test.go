package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectInstructions(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Agents\nPrefer tiny edits.\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, ".slmcode"), 0o755)
	_ = os.WriteFile(filepath.Join(root, ".slmcode", "PROJECT.md"), []byte("# Project\nGo app\n"), 0o644)
	out := LoadProjectInstructions(root)
	if !strings.Contains(out, "AGENTS.md") || !strings.Contains(out, "tiny edits") {
		t.Fatalf("%s", out)
	}
}
