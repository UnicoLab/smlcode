package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestPreToolUseTimeoutIsReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not guaranteed on Windows")
	}
	r := &Runner{Root: t.TempDir(), Cfg: Config{Hooks: map[string][]Hook{
		"PreToolUse": {{Matcher: "*", Command: "sleep 30", Timeout: 1}},
	}}}
	start := time.Now()
	err := r.RunEvent(context.Background(), "PreToolUse", "ws_edit", nil, "")
	if err == nil {
		t.Fatal("a timed-out PreToolUse hook must block the tool")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("hook timeout not enforced (%s)", elapsed)
	}
	for _, want := range []string{"timed out", "timeout_sec"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message must be actionable, got %v", err)
		}
	}
}

func TestHookKillsChildProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups differ on Windows")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "child-alive")
	r := &Runner{Root: root, Cfg: Config{Hooks: map[string][]Hook{
		"PostToolUse": {{Matcher: "*", Command: "bash -c 'sleep 5; touch " + marker + "' & wait", Timeout: 1}},
	}}}
	start := time.Now()
	_ = r.RunEvent(context.Background(), "PostToolUse", "ws_edit", nil, "")
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("orphaned hook child held the call for %s", elapsed)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hook child survived — the process group was not killed")
	}
}

func TestHookOutputIsBounded(t *testing.T) {
	cases := []struct {
		name     string
		writes   int
		chunk    int
		wantNote bool
	}{
		{"small", 1, 10, false},
		{"exactly at cap", 1, maxHookOutput, false},
		{"over cap", 4, maxHookOutput, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b boundedBuffer
			for i := 0; i < tc.writes; i++ {
				n, err := b.Write([]byte(strings.Repeat("x", tc.chunk)))
				if err != nil || n != tc.chunk {
					t.Fatalf("Write must report the full length: n=%d err=%v", n, err)
				}
			}
			s := b.String()
			if len(s) > maxHookOutput+120 {
				t.Fatalf("buffer grew to %d", len(s))
			}
			if strings.Contains(s, "dropped") != tc.wantNote {
				t.Fatalf("overflow note = %v, want %v", !tc.wantNote, tc.wantNote)
			}
		})
	}
}

func TestHooksDoNotUseLoginShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not guaranteed on Windows")
	}
	var logged string
	r := &Runner{
		Root: t.TempDir(),
		Cfg: Config{Hooks: map[string][]Hook{
			"PostToolUse": {{Matcher: "*", Command: `shopt -q login_shell && echo LOGIN || echo NONLOGIN`}},
		}},
		Log: func(f string, a ...interface{}) { logged = fmt.Sprintf(f, a...) },
	}
	_ = r.RunEvent(context.Background(), "PostToolUse", "ws_edit", nil, "")
	if !strings.Contains(logged, "NONLOGIN") {
		t.Fatalf("hooks must not source the login profile: %q", logged)
	}
}
