package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/authstore"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// authWorkspace initializes a project and points --root at it for the test.
func authWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Root = root
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	prev := flagRoot
	flagRoot = root
	t.Cleanup(func() { flagRoot = prev })
	return root
}

func runAuth(t *testing.T, args ...string) error {
	t.Helper()
	c := authCmd()
	c.SetArgs(args)
	c.SetOut(os.Stderr)
	return c.Execute()
}

// pkg/cli/probe.go tells a user whose endpoint returned 401 to run
// `slmcode auth set <key>`. Before this command existed the remediation named
// something that did not exist, and pkg/authstore was reachable only through
// Studio's PUT /api/auth.
func TestAuthSetGetRmRoundTrip(t *testing.T) {
	root := authWorkspace(t)
	slmDir := filepath.Join(root, ".slmcode")

	// The exact form probe.go's remediation prints: a bare key.
	if err := runAuth(t, "set", "sk-test-abcdefgh1234"); err != nil {
		t.Fatalf("auth set: %v", err)
	}
	got, ok := authstore.Get(slmDir, config.DefaultProvider)
	if !ok || got != "sk-test-abcdefgh1234" {
		t.Fatalf("key not stored for the active provider: got=%q ok=%v", got, ok)
	}

	// Named provider form.
	if err := runAuth(t, "set", "openai", "sk-openai-zzzz"); err != nil {
		t.Fatalf("auth set openai: %v", err)
	}
	if v, ok := authstore.Get(slmDir, "openai"); !ok || v != "sk-openai-zzzz" {
		t.Fatalf("openai key not stored: %q %v", v, ok)
	}

	if err := runAuth(t, "get", "openai"); err != nil {
		t.Fatalf("auth get: %v", err)
	}
	if err := runAuth(t, "list"); err != nil {
		t.Fatalf("auth list: %v", err)
	}

	if err := runAuth(t, "rm", "openai"); err != nil {
		t.Fatalf("auth rm: %v", err)
	}
	if _, ok := authstore.Get(slmDir, "openai"); ok {
		t.Fatal("auth rm did not remove the key")
	}
}

// The store holds API keys, so it must stay owner-only on disk.
func TestAuthStoreIsOwnerOnly(t *testing.T) {
	root := authWorkspace(t)
	slmDir := filepath.Join(root, ".slmcode")
	if err := runAuth(t, "set", "sk-perm-check-1234"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(authstore.Path(slmDir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("auth.json perm=%o want 600", perm)
	}
}

func TestAuthSetRefusesAnEmptyKey(t *testing.T) {
	authWorkspace(t)
	err := runAuth(t, "set", "openai", "   ")
	if err == nil {
		t.Fatal("an empty key must be refused, not stored")
	}
	if !strings.Contains(err.Error(), "auth rm") {
		t.Fatalf("the refusal should point at `auth rm`, got %v", err)
	}
}

func TestLooksLikeProviderSeparatesKeysFromProviders(t *testing.T) {
	for _, p := range []string{"openai", "ollama", "OpenAI", "lmstudio"} {
		if !looksLikeProvider(p) {
			t.Fatalf("%q should read as a provider", p)
		}
	}
	for _, k := range []string{
		"sk-abcdef0123456789", "gsk_live_x", "hf_xxxxxxxxxxxxxxxxxxxxxxxx",
		"", "some phrase", "http://x",
	} {
		if looksLikeProvider(k) {
			t.Fatalf("%q must be read as a KEY, not a provider", k)
		}
	}
}

// A key must never be echoed back, in any form that reconstructs it.
func TestMaskSecretNeverLeaksTheKey(t *testing.T) {
	const key = "sk-proj-SUPERSECRETVALUE9999"
	got := cli.MaskSecret(key)
	if strings.Contains(got, "SUPERSECRET") || strings.Contains(got, "sk-proj") {
		t.Fatalf("MaskSecret leaked the key: %q", got)
	}
	if !strings.HasSuffix(got, "9999") {
		t.Fatalf("MaskSecret should keep a short non-identifying tail, got %q", got)
	}
	if cli.MaskSecret("") != "" {
		t.Fatal("an empty secret must mask to empty")
	}
	if got := cli.MaskSecret("short"); strings.Contains(got, "short") {
		t.Fatalf("a short secret must be fully masked, got %q", got)
	}
}
