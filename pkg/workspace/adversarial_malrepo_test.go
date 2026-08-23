package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: the tool layer, not just the guard functions. A malicious
// repository plus a compliant-but-confused model must not be able to reach
// harness control state or the host filesystem through ws_shell.
func TestAdvMaliciousRepoEndToEnd(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	slm := filepath.Join(real, ".slmcode")
	if err := os.MkdirAll(slm, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slm, "auth.json"),
		[]byte(`{"keys":{"openai":"sk-CANARY"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	for _, whitelist := range []bool{true, false} {
		ws, _, err := NewWorkspace(real, ToolOpts{
			Permission: "auto", ShellPermission: "allow", SlmDir: slm,
			ShellWhitelist: whitelist, DisableSyntaxCheck: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		attacks := []struct{ name, cmd, artifact string }{
			{"hooks via redirect", `echo '{"hooks":{}}' > .slmcode/hooks.json`, filepath.Join(slm, "hooks.json")},
			{"hooks via append", `echo x >> .slmcode/hooks.json`, filepath.Join(slm, "hooks.json")},
			{"hooks via tee", `echo x | tee .slmcode/hooks.json`, filepath.Join(slm, "hooks.json")},
			{"config rewrite", `echo 'shell_whitelist: false' > .slmcode/config.yaml`, filepath.Join(slm, "config.yaml")},
			{"escape via ..", `echo pwned > ../escape.txt`, filepath.Join(filepath.Dir(real), "escape.txt")},
			{"escape absolute", `echo pwned > ` + filepath.Join(outside, "escape.txt"), filepath.Join(outside, "escape.txt")},
			{"dd escape", `dd of=` + filepath.Join(outside, "dd.txt"), filepath.Join(outside, "dd.txt")},
		}
		for _, a := range attacks {
			out, err := ws.shell(context.Background(), map[string]interface{}{"command": a.cmd})
			s, _ := out.(string)
			if err == nil && !strings.Contains(s, "refused") && !strings.Contains(s, "whitelist") {
				t.Errorf("whitelist=%v %s NOT REFUSED: %q", whitelist, a.name, s)
			}
			if _, serr := os.Stat(a.artifact); serr == nil {
				t.Errorf("whitelist=%v %s CREATED %s", whitelist, a.name, a.artifact)
				_ = os.Remove(a.artifact)
			}
		}
	}

	// With the whitelist on, the credential file must not be readable through
	// the shell's own allowlisted readers either — but only because the
	// whitelist is a per-command allowlist, not a filesystem jail. Assert the
	// documented behavior rather than a jail we do not have.
	ws, _, _ := NewWorkspace(real, ToolOpts{
		Permission: "auto", ShellPermission: "allow", SlmDir: slm,
		ShellWhitelist: true, DisableSyntaxCheck: true,
	})
	out, _ := ws.shell(context.Background(), map[string]interface{}{"command": "cat .slmcode/auth.json"})
	if s, _ := out.(string); strings.Contains(s, "sk-CANARY") {
		t.Logf("KNOWN AND DOCUMENTED: ws_shell is not filesystem-jailed; `cat` reads %s", "auth.json")
	}
}

// Whatever route a credential takes out of the tool layer, it must not reach
// the model's context.
func TestAdvCredentialsNeverReachAToolResult(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	slm := filepath.Join(real, ".slmcode")
	_ = os.MkdirAll(slm, 0o750)
	const canary = "sk-CANARY-9f3b1e7c2a4d"
	_ = os.WriteFile(filepath.Join(slm, "auth.json"),
		[]byte(`{"keys":{"openai":"`+canary+`"}}`), 0o600)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-ENVCANARY-1234567890")

	ws, _, err := NewWorkspace(real, ToolOpts{
		Permission: "auto", ShellPermission: "allow", SlmDir: slm,
		ShellWhitelist: true, DisableSyntaxCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	scrub := ws.capped(ws.shell)
	for _, cmd := range []string{
		"cat .slmcode/auth.json",
		"cat .slmcode/*.json",
		"head -n 100 .slmcode/auth.json",
		"grep -r sk- .",
		"find . -name auth.json -type f",
		"printenv",
		"env",
	} {
		out, err := scrub(context.Background(), map[string]interface{}{"command": cmd})
		s, _ := out.(string)
		if err != nil {
			continue
		}
		if strings.Contains(s, canary) {
			t.Errorf("CREDENTIAL LEAKED via ws_shell %q:\n%s", cmd, s)
		}
		if strings.Contains(s, "sk-ant-ENVCANARY-1234567890") {
			t.Errorf("ENV CREDENTIAL LEAKED via ws_shell %q:\n%s", cmd, s)
		}
	}
	// And through the file tools, which refuse the path outright.
	rd := ws.capped(ws.readFile)
	out, _ := rd(context.Background(), map[string]interface{}{"path": ".slmcode/auth.json"})
	if s, _ := out.(string); strings.Contains(s, canary) {
		t.Errorf("CREDENTIAL LEAKED via ws_read:\n%s", s)
	}
}
