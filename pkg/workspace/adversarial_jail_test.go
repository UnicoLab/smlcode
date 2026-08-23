package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func advTestWS(t *testing.T) (*Workspace, string) {
	t.Helper()
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	ws, _, err := NewWorkspace(real, ToolOpts{Permission: "auto", ShellPermission: "allow",
		SlmDir: filepath.Join(real, ".slmcode"), DisableSyntaxCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	return ws, real
}

func advCall(t *testing.T, fn func(context.Context, map[string]interface{}) (interface{}, error), args map[string]interface{}) (string, error) {
	t.Helper()
	out, err := fn(context.Background(), args)
	s, _ := out.(string)
	return s, err
}

func TestJailEscapes(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(secret, []byte("TOPSECRET"), 0o600)

	ws, root := advTestWS(t)

	// 1. symlink committed in repo pointing outside (read)
	_ = os.Symlink(secret, filepath.Join(root, "link.txt"))
	if s, err := advCall(t, ws.readFile, map[string]interface{}{"path": "link.txt"}); err == nil && strings.Contains(s, "TOPSECRET") {
		t.Errorf("READ ESCAPE via repo symlink: %s", s)
	}
	// 2. symlink write
	if s, err := advCall(t, ws.writeFile, map[string]interface{}{"path": "link.txt", "content": "PWNED"}); err == nil && !strings.Contains(s, "refus") && !strings.Contains(s, "escape") {
		t.Errorf("WRITE ESCAPE via repo symlink: %v %v", s, err)
	}
	if b, _ := os.ReadFile(secret); string(b) != "TOPSECRET" {
		t.Errorf("secret was overwritten: %s", b)
	}
	// 3. symlinked PARENT dir
	_ = os.Symlink(outside, filepath.Join(root, "dir"))
	if s, err := advCall(t, ws.writeFile, map[string]interface{}{"path": "dir/new.txt", "content": "PWNED"}); err == nil && !strings.Contains(s, "escape") {
		t.Errorf("WRITE ESCAPE via symlinked parent: %v %v", s, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); err == nil {
		t.Errorf("file created outside root via symlinked parent")
	}
	// 4. .. in path
	for _, p := range []string{"../escape.txt", "a/../../escape.txt", "./../../escape.txt",
		"..", "../..", `..\..\escape.txt`, "a/b/../../../escape.txt"} {
		if _, err := ws.resolve(p); err == nil {
			abs, _ := ws.resolve(p)
			if !strings.HasPrefix(abs, root) {
				t.Errorf(".. ESCAPE: %q -> %q", p, abs)
			}
		}
	}
	// 5. absolute path
	for _, p := range []string{secret, "/etc/passwd", "//etc/passwd", "/etc/../etc/passwd"} {
		abs, err := ws.resolve(ws.normalizeRelPath(p))
		if err == nil && !strings.HasPrefix(abs, root) {
			t.Errorf("ABS ESCAPE: %q -> %q", p, abs)
		}
	}
	// 6. NUL / newline in path
	for _, p := range []string{"a\x00b", "a\nb", "a\rb", "a\tb", "ok\x00/../../etc/passwd"} {
		if abs, err := ws.resolve(p); err == nil {
			t.Errorf("CONTROL CHAR PATH ACCEPTED: %q -> %q", p, abs)
		}
	}
	// 7. .slmcode writes via every tool
	_ = os.MkdirAll(filepath.Join(root, ".slmcode"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "src.txt"), []byte("x"), 0o644)
	ws.markRead("src.txt")
	for name, res := range map[string]func() (string, error){
		"write": func() (string, error) {
			return advCall(t, ws.writeFile, map[string]interface{}{"path": ".slmcode/hooks.json", "content": "{}"})
		},
		"mv": func() (string, error) {
			return advCall(t, ws.moveFile, map[string]interface{}{"from": "src.txt", "to": ".slmcode/hooks.json"})
		},
		"patch": func() (string, error) {
			return advCall(t, ws.patchFile, map[string]interface{}{"path": ".slmcode/hooks.json", "patch": "@@\n+x\n"})
		},
		"delete": func() (string, error) {
			return advCall(t, ws.deleteFile, map[string]interface{}{"path": ".slmcode/config.yaml"})
		},
	} {
		s, err := res()
		if err == nil && !strings.Contains(s, "refused") && !strings.Contains(s, "blocked") {
			t.Errorf(".slmcode write via %s NOT blocked: %q err=%v", name, s, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".slmcode", "hooks.json")); err == nil {
		t.Errorf(".slmcode/hooks.json was created")
	}
	// 8. shell redirection into .slmcode
	if err := GuardShellWrites(root, `echo x > .slmcode/hooks.json`); err == nil {
		t.Errorf("shell redirect into .slmcode NOT blocked")
	}
	if _, blocked := GuardShellWhitelist(`echo x > .slmcode/hooks.json`, nil); !blocked {
		t.Errorf("shell redirect into .slmcode not whitelist-blocked")
	}
	// 9. agent creates symlink then uses it (simulate)
	_ = os.Symlink("/etc", filepath.Join(root, "etcx"))
	if _, err := ws.resolve("etcx/passwd"); err == nil {
		t.Errorf("agent-created symlink escape not blocked")
	}
}

func TestSymlinkLoopAndProc(t *testing.T) {
	ws, root := advTestWS(t)
	_ = os.Symlink(filepath.Join(root, "b"), filepath.Join(root, "a"))
	_ = os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "b"))
	done := make(chan struct{})
	go func() { defer close(done); _, _ = ws.resolve("a") }()
	<-done
	if _, err := ws.resolve("/proc/self/environ"); err == nil {
		abs, _ := ws.resolve(ws.normalizeRelPath("/proc/self/environ"))
		if strings.HasPrefix(abs, "/proc") {
			t.Errorf("/proc escape: %s", abs)
		}
	}
}

func TestSecretsReadableByAgent(t *testing.T) {
	ws, root := advTestWS(t)
	_ = os.MkdirAll(filepath.Join(root, ".slmcode"), 0o750)
	_ = os.WriteFile(filepath.Join(root, ".slmcode", "auth.json"), []byte(`{"keys":{"openai":"sk-SUPERSECRET"}}`), 0o600)
	_ = os.WriteFile(filepath.Join(root, ".slmcode", "config.yaml"), []byte("shell_whitelist: true\n"), 0o600)

	if s, _ := advCall(t, ws.readFile, map[string]interface{}{"path": ".slmcode/auth.json"}); strings.Contains(s, "SUPERSECRET") {
		t.Errorf("SECRET LEAK via ws_read: %s", s)
	}
	if s, _ := advCall(t, ws.grep, map[string]interface{}{"pattern": "sk-", "path": "."}); strings.Contains(s, "SUPERSECRET") {
		t.Errorf("SECRET LEAK via ws_grep: %s", s)
	}
	if s, _ := advCall(t, ws.glob, map[string]interface{}{"pattern": ".slmcode/**"}); strings.Contains(s, "auth.json") {
		t.Errorf("auth.json exposed via ws_glob: %s", s)
	}
	if s, _ := advCall(t, ws.listDir, map[string]interface{}{"path": ".slmcode"}); strings.Contains(s, "auth.json") {
		t.Errorf("auth.json exposed via ws_list: %s", s)
	}
}

// Denial of service: none of these may hang or exhaust memory.
func TestAdvDenialOfService(t *testing.T) {
	ws, root := advTestWS(t)

	// giant file read must be windowed
	big := strings.Repeat("x123456789\n", 2_000_000) // ~22MB, 2M lines
	_ = os.WriteFile(filepath.Join(root, "big.txt"), []byte(big), 0o644)
	out, err := advCall(t, ws.readFile, map[string]interface{}{"path": "big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 200_000 {
		t.Errorf("ws_read returned %d chars for a 22MB file", len(out))
	}

	// deeply nested directory must not blow the walk
	deep := root
	for i := 0; i < 60; i++ {
		deep = filepath.Join(deep, "d")
	}
	_ = os.MkdirAll(deep, 0o755)
	_ = os.WriteFile(filepath.Join(deep, "x.go"), []byte("package x"), 0o644)
	done := make(chan string, 1)
	go func() {
		s, _ := advCall(t, ws.glob, map[string]interface{}{"pattern": "**/*.go"})
		done <- s
	}()
	select {
	case s := <-done:
		if len(s) > 100_000 {
			t.Errorf("ws_glob returned %d chars", len(s))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("ws_glob hung on a deep tree")
	}

	// symlink loop must not hang the walk
	_ = os.Symlink(root, filepath.Join(root, "loop"))
	done2 := make(chan struct{})
	go func() { defer close(done2); _, _ = advCall(t, ws.glob, map[string]interface{}{"pattern": "**/*.go"}) }()
	select {
	case <-done2:
	case <-time.After(30 * time.Second):
		t.Fatal("ws_glob followed a symlink loop")
	}

	// runaway grep must be bounded
	s, _ := advCall(t, ws.grep, map[string]interface{}{"pattern": "x"})
	if len(s) > 100_000 {
		t.Errorf("ws_grep returned %d chars", len(s))
	}
}

// A fork bomb / runaway command must be killed with its whole process group.
func TestAdvShellForkBombIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	root := t.TempDir()
	start := time.Now()
	res := RunBounded(context.Background(), root, "yes | head -c 100000000; sleep 60", 3*time.Second, 64*1024)
	if time.Since(start) > 20*time.Second {
		t.Fatalf("RunBounded did not enforce its timeout: %s", time.Since(start))
	}
	if !res.TimedOut {
		t.Error("expected a timeout verdict")
	}
	if len(res.Output) > 200*1024 {
		t.Errorf("captured %d bytes despite a 64KB cap", len(res.Output))
	}
}

// Case-insensitive filesystems (macOS, Windows): .SLMCODE/hooks.json is the
// same file as .slmcode/hooks.json, so the boundary must be case-folded.
func TestAdvHarnessBoundaryIsCaseInsensitive(t *testing.T) {
	for _, p := range []string{
		".SLMCODE/hooks.json", ".SlmCode/config.yaml", ".slmcode/HOOKS.json",
		".SLMCODE/auth.json", "./.SLMCODE/hooks.json",
	} {
		if err := CheckHarnessStateWrite(p); err == nil {
			t.Errorf("CASE BYPASS (write): %q", p)
		}
		if err := CheckHarnessStateRead(p); err == nil {
			t.Errorf("CASE BYPASS (read): %q", p)
		}
		if !HideFromListing(p) {
			t.Errorf("CASE BYPASS (listing): %q", p)
		}
	}
	if !IsHarnessSecretPath(".SLMCODE/AUTH.JSON") {
		t.Error("CASE BYPASS: secret path not recognized")
	}
	// Scratch stays writable in any case.
	if err := CheckHarnessStateWrite(".SLMCODE/scratch/notes.md"); err != nil {
		t.Errorf("scratch over-blocked: %v", err)
	}
}

// A hardlink is indistinguishable from the file it names, by design; assert the
// documented behavior rather than a guard we cannot have.
func TestAdvHardlinkIsIndistinguishable(t *testing.T) {
	ws, root := advTestWS(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(target, []byte("TOPSECRET"), 0o600)
	link := filepath.Join(root, "hard.txt")
	if err := os.Link(target, link); err != nil {
		t.Skipf("hardlinks unsupported here: %v", err)
	}
	if _, err := ws.resolve("hard.txt"); err != nil {
		t.Fatalf("hardlink path unexpectedly refused: %v", err)
	}
	t.Log("DOCUMENTED: a hardlink inside the root resolves as an in-root file. " +
		"git cannot check one in and the agent cannot create one (`ln` is a blocked mutator), " +
		"so this is not reachable by either adversary.")
}
