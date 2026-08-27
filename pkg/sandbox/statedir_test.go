package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"gopkg.in/yaml.v3"
)

// The contract that makes worktree isolation safe to turn on: the WORK moves
// to the sandbox, the harness's accumulated STATE does not.
//
// This is the property most likely to regress silently. Nothing fails if it
// breaks — runs still complete, gates still pass — the harness just quietly
// stops learning, and every run starts from zero with no error to explain it.

func TestSlmDirFollowsRootByDefault(t *testing.T) {
	cfg := &config.Config{Root: "/tmp/project"}
	want := filepath.Join("/tmp/project", config.DirName)
	if got := cfg.SlmDir(); got != want {
		t.Fatalf("SlmDir = %q, want %q", got, want)
	}
}

func TestStateDirPinsSlmDirAwayFromRoot(t *testing.T) {
	cfg := &config.Config{Root: "/tmp/worktree-xyz", StateDir: "/tmp/project/.slmcode"}
	if got := cfg.SlmDir(); got != "/tmp/project/.slmcode" {
		t.Fatalf("SlmDir = %q, want the pinned state directory", got)
	}
	// Everything derived from SlmDir follows it, or the split is only half done
	// and config.yaml, skills and agents come from the throwaway copy.
	for name, got := range map[string]string{
		"ConfigPath": cfg.ConfigPath(),
		"SkillsDir":  cfg.SkillsDir(),
		"AgentsDir":  cfg.AgentsDir(),
	} {
		if !strings.HasPrefix(got, "/tmp/project/.slmcode") {
			t.Errorf("%s = %q, want it under the pinned state directory", name, got)
		}
	}
}

func TestBlankStateDirIsIgnored(t *testing.T) {
	cfg := &config.Config{Root: "/tmp/project", StateDir: "   "}
	want := filepath.Join("/tmp/project", config.DirName)
	if got := cfg.SlmDir(); got != want {
		t.Fatalf("SlmDir = %q, want %q — whitespace is not a state directory", got, want)
	}
}

func TestIsolatedRunKeepsStateInTheOriginCheckout(t *testing.T) {
	// End to end against a real worktree: state written during an "isolated
	// run" lands in the operator's checkout and survives the sandbox being
	// thrown away.
	ctx := context.Background()
	origin := newRepo(t)
	sb, err := Open(ctx, origin, "slmcode/state-test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	cfg := &config.Config{
		Root:     sb.Root(),
		StateDir: filepath.Join(origin, config.DirName),
	}
	if err := os.MkdirAll(cfg.SlmDir(), 0o750); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	memory := filepath.Join(cfg.SlmDir(), "memory.json")
	if err := os.WriteFile(memory, []byte(`{"learned":"something"}`), 0o600); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	// The run writes code into the sandbox…
	write(t, sb.Root(), "main.go", "package main // isolated\n")
	if read(t, origin, "main.go") != "package main\n" {
		t.Fatal("the isolated write reached the operator's checkout")
	}

	// …and the sandbox is thrown away.
	if err := sb.Discard(ctx); err != nil {
		t.Fatalf("Discard: %v", err)
	}

	if got := read(t, origin, config.DirName+"/memory.json"); !strings.Contains(got, "something") {
		t.Fatalf("harness state was discarded with the sandbox: %q", got)
	}
	if got := read(t, origin, "main.go"); got != "package main\n" {
		t.Errorf("the operator's checkout was modified: %q", got)
	}
}

func TestStateDirIsNotPersistedToConfigYAML(t *testing.T) {
	// Same reason Root is not persisted: an absolute path baked into
	// config.yaml is not portable, and a stale one would silently point a
	// later run's state somewhere it does not belong.
	cfg := config.Default(t.TempDir())
	cfg.StateDir = "/tmp/elsewhere/.slmcode"
	blob, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "elsewhere") {
		t.Errorf("StateDir was persisted:\n%s", blob)
	}
	if strings.Contains(string(blob), "state_dir") {
		t.Errorf("state_dir appeared in config.yaml:\n%s", blob)
	}
}
