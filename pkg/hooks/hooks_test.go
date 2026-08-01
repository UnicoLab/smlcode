package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPreToolUseBlocks(t *testing.T) {
	r := &Runner{
		Root: t.TempDir(),
		Cfg: Config{Hooks: map[string][]Hook{
			"PreToolUse": {{Matcher: "ws_shell", Command: "exit 1"}},
		}},
	}
	err := r.RunEvent(context.Background(), "PreToolUse", "ws_shell", map[string]interface{}{"command": "rm -rf /"}, "")
	if err == nil {
		t.Fatal("expected block")
	}
}

func TestLoadMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "hooks.json"))
	if err != nil || len(c.Hooks) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestLoadOK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	_ = os.WriteFile(path, []byte(`{"hooks":{"PostToolUse":[{"matcher":"ws_write","command":"true"}]}}`), 0o644)
	c, err := Load(path)
	if err != nil || len(c.Hooks["PostToolUse"]) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
}
