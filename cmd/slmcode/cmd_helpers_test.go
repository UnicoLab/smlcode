package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

func TestEnsureSlmGitignoreCoversSecrets(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	if err := ensureSlmGitignore(slm); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(slm, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{"auth.json", "pending/", "sessions/", "queries/", "archives/", "errors/"} {
		if !strings.Contains(body, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, body)
		}
	}
}

func TestEnsureSlmGitignoreDoesNotClobber(t *testing.T) {
	slm := filepath.Join(t.TempDir(), ".slmcode")
	if err := os.MkdirAll(slm, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "# mine\nauth.json\n"
	path := filepath.Join(slm, ".gitignore")
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureSlmGitignore(slm); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != custom {
		t.Fatalf("existing .gitignore was overwritten:\n%s", data)
	}
}

// TestGitIgnoresAuthJSON proves the written rules actually keep the API-key
// store out of `git add -A`, which is what `slmcode commit` runs.
func TestGitIgnoresAuthJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %s %v", out, err)
	}
	slm := filepath.Join(root, ".slmcode")
	if err := ensureSlmGitignore(slm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slm, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !gitIgnores(root, ".slmcode/auth.json") {
		t.Fatal(".slmcode/auth.json is stageable — API keys can leak into a commit")
	}
	for name, probe := range gitignoreProbes {
		if !gitIgnores(root, probe) {
			t.Errorf(".slmcode/%s is not ignored (probe %q)", name, probe)
		}
	}
}

func TestGitIgnoresOutsideRepoIsTrue(t *testing.T) {
	if gitIgnores(t.TempDir(), "anything") != true {
		t.Fatal("outside a repo nothing can be staged, so everything counts as ignored")
	}
}

func TestSchemaCoversEveryPatchableKey(t *testing.T) {
	byKey := map[string]config.FieldSchema{}
	for _, f := range config.Schema() {
		byKey[f.Key] = f
	}
	// These were settable through config.Patch but absent from config.Schema(),
	// which is why the CLI carried a duplicate table. One table now.
	for _, key := range []string{
		"max_parallel", "max_retries", "think_passes", "permission",
		"shell_permission", "qa_gate", "mode", "listen", "dry_run",
		"qa_gate_max_rounds", "escalate_ask_timeout", "plan_approve_on_timeout",
		"evolve", "deterministic", "memory_tokens", "max_task_calls",
		"regression_checks", "architect_editor", "read_window_lines",
		"max_tool_chars", "shell_timeout", "disable_syntax_check",
		"structured_decoding", "qa_bootstrap",
		"repo_map_tokens", "excerpt_window_lines", "skill_disclosure",
		"retrieval_min_score", "retrieval_cache_dir",
	} {
		f, ok := byKey[key]
		if !ok {
			t.Errorf("schema is missing %q", key)
			continue
		}
		if f.Type == "" || !f.Patchable {
			t.Errorf("%q has no usable type/patchable flag: %+v", key, f)
		}
		if f.Group == "" || f.Label == "" {
			t.Errorf("%q has no group/label: %+v", key, f)
		}
		if f.Env == "" {
			t.Errorf("%q has no environment variable", key)
		}
	}
}

func TestSchemaCoversEveryConfigField(t *testing.T) {
	described := map[string]bool{}
	for _, f := range config.Schema() {
		described[f.Key] = true
	}
	for _, key := range config.Keys() {
		if key == "config_version" {
			continue
		}
		if !described[key] {
			t.Errorf("config key %q has no schema entry", key)
		}
	}
}

func TestSchemaDefaultsMatchDefaultConfig(t *testing.T) {
	def := config.Default(t.TempDir())
	for _, f := range config.Schema() {
		if f.Secret {
			continue
		}
		want, ok := def.Get(f.Key)
		if !ok {
			t.Errorf("schema key %q is not a config field", f.Key)
			continue
		}
		if f.Default == nil {
			continue // empty defaults are omitted from JSON
		}
		if fmt.Sprint(f.Default) != fmt.Sprint(want) {
			t.Errorf("%q schema default %v != Default() %v", f.Key, f.Default, want)
		}
	}
}

func TestConfigSetRejectsGarbage(t *testing.T) {
	// The whole point of routing through the schema: a bad value is an error,
	// not a cheerful "✔ set parallel = abc".
	c := config.Default(t.TempDir())
	for _, tc := range []struct{ key, value string }{
		{"max_parallel", "abc"},
		{"permission", "sudo"},
		{"qa_gate", "maybe"},
	} {
		if err := c.Set(tc.key, tc.value); err == nil {
			t.Errorf("Set(%q, %q) should have failed", tc.key, tc.value)
		}
	}
}

func TestConfigSetAcceptsValidValues(t *testing.T) {
	c := config.Default(t.TempDir())
	if err := c.Set("max_parallel", "6"); err != nil || c.MaxParallel != 6 {
		t.Fatalf("max_parallel=%d err=%v", c.MaxParallel, err)
	}
	if err := c.Set("permission", "review"); err != nil || c.Permission != "review" {
		t.Fatalf("permission=%q err=%v", c.Permission, err)
	}
}

func TestCanonicalConfigKeyAliases(t *testing.T) {
	for in, want := range map[string]string{
		"parallel": "max_parallel",
		"retries":  "max_retries",
		"think":    "think_passes",
		"perm":     "permission",
		"model":    "model",
	} {
		if got := config.CanonicalKey(in); got != want {
			t.Errorf("config.CanonicalKey(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRedactKey(t *testing.T) {
	if redactKey("") != "" {
		t.Fatal("empty stays empty")
	}
	if redactKey("short") != "****" {
		t.Fatal("short keys are fully masked")
	}
	got := redactKey("sk-1234567890abcdef")
	if strings.Contains(got, "567890") {
		t.Fatalf("key body leaked: %q", got)
	}
	if !strings.HasPrefix(got, "sk-1") {
		t.Fatalf("prefix lost: %q", got)
	}
}

func TestExitCodeMapping(t *testing.T) {
	if exitCodeFor(nil) != 0 {
		t.Fatal("nil is success")
	}
	if got := exitCodeFor(failf(4, "provider down")); got != 4 {
		t.Fatalf("coded error exit=%d", got)
	}
	if got := exitCodeFor(errString("context canceled")); got != 130 {
		t.Fatalf("interrupt exit=%d", got)
	}
	if got := exitCodeFor(errString("unknown flag: --nope")); got != 2 {
		t.Fatalf("usage exit=%d", got)
	}
	if got := exitCodeFor(errString("something broke")); got != 1 {
		t.Fatalf("generic exit=%d", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestParseSHA256SUMS(t *testing.T) {
	body := "abc123  slmcode_1.2.3_linux_amd64\ndef456 *slmcode_1.2.3_darwin_arm64\n\n"
	sums := parseSHA256SUMS(body)
	if sums["slmcode_1.2.3_linux_amd64"] != "abc123" {
		t.Fatalf("sums=%v", sums)
	}
	if sums["slmcode_1.2.3_darwin_arm64"] != "def456" {
		t.Fatalf("binary-mode '*' prefix not stripped: %v", sums)
	}
}

func TestAssetName(t *testing.T) {
	got := assetName("v1.2.3")
	if !strings.HasPrefix(got, "slmcode_1.2.3_") {
		t.Fatalf("assetName=%q (the leading v must be stripped)", got)
	}
}

func TestResolveUpdateRepoRejectsUnknownUpstream(t *testing.T) {
	t.Setenv("SLMCODE_UPDATE_REPO", "attacker/evil")
	if _, err := resolveUpdateRepo(nil); err == nil {
		t.Fatal("an unlisted repo must be refused — the updater downloads and executes it")
	}
}

func TestResolveUpdateRepoAllowsUpstream(t *testing.T) {
	t.Setenv("SLMCODE_UPDATE_REPO", "")
	repo, err := resolveUpdateRepo(nil)
	if err != nil || repo != updateDefaultRepo {
		t.Fatalf("repo=%q err=%v", repo, err)
	}
}

func TestAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplace(src, dst); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "new" {
		t.Fatalf("dst=%q", data)
	}
}
