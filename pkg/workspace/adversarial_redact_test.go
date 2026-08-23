package workspace

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func redactWS(t *testing.T) (*Workspace, string) {
	t.Helper()
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	slm := filepath.Join(real, ".slmcode")
	if err := os.MkdirAll(slm, 0o750); err != nil {
		t.Fatal(err)
	}
	ws, _, err := NewWorkspace(real, ToolOpts{
		Permission: "auto", ShellPermission: "allow", SlmDir: slm,
		ShellWhitelist: true, DisableSyntaxCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ws, slm
}

// REGRESSION: OMLX_API_KEY is read by pkg/config.ResolveAPIKey for the default
// `omlx` provider but was missing from the scrub list, so it was the one
// provider variable `env` through ws_shell still returned in full.
//
// This reads pkg/config's SOURCE rather than trusting secretEnvVars, because a
// test that iterates the list it is checking passes by construction — deleting
// an entry would silently "fix" it. Every credential variable the config layer
// can turn into the harness's API key must be one this layer scrubs.
func TestAdvScrubListCoversEveryProviderEnvVar(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "config", "config.go"))
	if err != nil {
		t.Skipf("cannot read pkg/config source: %v", err)
	}
	re := regexp.MustCompile(`os\.Getenv\("([A-Z0-9_]*(?:API_KEY|_TOKEN))"\)`)
	known := map[string]bool{}
	for _, v := range secretEnvVars {
		known[v] = true
	}
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if !known[name] {
			t.Errorf("%s can become the API key but is NOT redacted from tool results "+
				"(add it to secretEnvVars in redact.go)", name)
		}
	}
	if len(seen) == 0 {
		t.Fatal("found no provider env vars in pkg/config — the drift check is not working")
	}
}

// Every listed variable must actually be scrubbed out of a tool result.
func TestAdvEveryProviderEnvKeyIsRedacted(t *testing.T) {
	for _, v := range secretEnvVars {
		canary := "sk-canary-" + strings.ToLower(v) + "-0123456789"
		t.Setenv(v, canary)
		ws, _ := redactWS(t)
		if got := ws.RedactSecrets("value is " + canary); strings.Contains(got, canary) {
			t.Errorf("%s SURVIVES A TOOL RESULT: %s", v, got)
		}
	}
}

// A key persisted into config.yaml (SLMCODE_PERSIST_API_KEY) is a live
// credential; `cat .slmcode/config.yaml` used to return it verbatim.
func TestAdvConfigFileAPIKeyIsRedacted(t *testing.T) {
	ws, slm := redactWS(t)
	const canary = "sk-configfile-CANARY-4242"
	if err := os.WriteFile(filepath.Join(slm, "config.yaml"),
		[]byte("provider: openai\napi_key: "+canary+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ws.RedactSecrets("key=" + canary); strings.Contains(got, canary) {
		t.Fatalf("CONFIG API KEY LEAKED: %s", got)
	}
}

// The scrub list used to load exactly once per Workspace, so a key stored after
// the first tool call (Studio's auth endpoint, `slmcode auth set` in another
// terminal) was never redacted for the rest of the run.
func TestAdvSecretsAddedMidRunAreRedacted(t *testing.T) {
	ws, slm := redactWS(t)
	const canary = "sk-ROTATED-CANARY-777888"

	// Prime the cache with an empty store.
	if got := ws.RedactSecrets("nothing here"); got != "nothing here" {
		t.Fatalf("unexpected scrub: %q", got)
	}
	if err := os.WriteFile(filepath.Join(slm, "auth.json"),
		[]byte(`{"keys":{"openai":"`+canary+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ws.RedactSecrets("key=" + canary); strings.Contains(got, canary) {
		t.Fatalf("MID-RUN CREDENTIAL LEAKED: %s", got)
	}
}

// AddSecrets is the hook for credentials only the caller knows (--api-key, a
// user-level config).
func TestAddSecretsRegistersACredential(t *testing.T) {
	ws, _ := redactWS(t)
	const canary = "sk-flag-supplied-CANARY"
	ws.AddSecrets(canary, "short") // "short" must be ignored, not blanket-matched
	if got := ws.RedactSecrets("k=" + canary); strings.Contains(got, canary) {
		t.Fatalf("registered secret survived: %s", got)
	}
	if got := ws.RedactSecrets("this is a short sentence"); !strings.Contains(got, "short") {
		t.Fatalf("a too-short value was redacted from ordinary prose: %s", got)
	}
}

// The whole point of redaction is the ws_shell channel, which no path guard can
// close. Re-assert it end to end for the newly covered sources.
func TestAdvShellCannotEchoAnyKnownCredential(t *testing.T) {
	t.Setenv("OMLX_API_KEY", "sk-omlx-CANARY-11223344")
	ws, slm := redactWS(t)
	if err := os.WriteFile(filepath.Join(slm, "config.yaml"),
		[]byte("api_key: sk-cfg-CANARY-55667788\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scrub := ws.capped(ws.shell)
	for _, cmd := range []string{"env", "printenv", "cat .slmcode/config.yaml"} {
		out, err := scrub(context.Background(), map[string]interface{}{"command": cmd})
		if err != nil {
			continue
		}
		s, _ := out.(string)
		for _, canary := range []string{"sk-omlx-CANARY-11223344", "sk-cfg-CANARY-55667788"} {
			if strings.Contains(s, canary) {
				t.Errorf("CREDENTIAL LEAKED via %q:\n%s", cmd, s)
			}
		}
	}
}
