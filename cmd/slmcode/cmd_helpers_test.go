package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
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
	// Every entry pkg/config declares must be in the rendered file — the CLI
	// no longer keeps its own list, so this is the check that init cannot fall
	// behind the workspace layout again.
	if len(config.SlmIgnoreEntries) < 20 {
		t.Fatalf("the ignore list shrank to %d entries — that is a leak, not a cleanup", len(config.SlmIgnoreEntries))
	}
	for _, e := range config.SlmIgnoreEntries {
		if !strings.Contains(body, "\n"+e.Pattern+"\n") {
			t.Errorf(".gitignore missing pattern %q:\n%s", e.Pattern, body)
		}
	}
	// The paths a team is meant to share must NOT be ignored.
	for _, shared := range []string{"config.yaml", "board.json", "hooks.json"} {
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) == shared {
				t.Errorf("%q is ignored — that is shared, reviewable state", shared)
			}
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
	probes := gitignoreProbes()
	if len(probes) != len(config.SlmIgnoreEntries) {
		t.Fatalf("doctor probes %d paths but init writes %d rules", len(probes), len(config.SlmIgnoreEntries))
	}
	// Real `git check-ignore`, one probe per rule: this is the assertion that
	// the file a fresh `init` writes actually covers everything doctor claims
	// to have checked. A rule that is present but ineffective (a trailing
	// comment, a missing "/") fails here and nowhere else.
	for name, probe := range probes {
		if !gitIgnores(root, probe) {
			t.Errorf(".slmcode/%s is not ignored (probe %q) — `git add -A` would stage it", name, probe)
		}
	}
}

// TestDoctorGitignoreGapsNameTheRealPaths proves the doctor warning lists the
// paths that are actually stageable, not a hardcoded subset of six.
func TestDoctorGitignoreGapsNameTheRealPaths(t *testing.T) {
	status := map[string]any{"ok": false}
	for _, e := range config.SlmIgnoreEntries {
		status[strings.TrimSuffix(e.Pattern, "/")] = true
	}
	status["memory"] = false
	status["metrics"] = false
	gaps := gitignoreGaps(status)
	want := []string{".slmcode/memory/", ".slmcode/metrics/"}
	if len(gaps) != len(want) {
		t.Fatalf("gaps=%v want %v", gaps, want)
	}
	for i := range want {
		if gaps[i] != want[i] {
			t.Fatalf("gaps=%v want %v", gaps, want)
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
	if got := exitCodeFor(fmt.Errorf("call failed: %w", context.Canceled)); got != 130 {
		t.Fatalf("wrapped context.Canceled exit=%d (errors.Is must win)", got)
	}
	// A provider error is not a Ctrl-C. Exit 130 used to be handed out for the
	// bare word "interrupted" anywhere in the message, so a wrapper script
	// could not tell an upstream hiccup from a user pressing Ctrl-C.
	for _, msg := range []string{
		"upstream request interrupted by the model server",
		"stream interrupted after 3 tokens",
		"HTTP 502: connection interrupted",
	} {
		if got := exitCodeFor(errString(msg)); got == 130 {
			t.Errorf("%q exits 130 — that claims the user interrupted a run they never touched", msg)
		}
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

// TestNoBannerFlagActuallySuppressesTheBanner is a regression test for a flag
// that was accepted, documented and inert: --no-banner was bound to a variable
// nothing read, so `slmcode --no-banner --help` printed the ASCII logo anyway.
func TestNoBannerFlagActuallySuppressesTheBanner(t *testing.T) {
	t.Cleanup(func() { cli.SetBannerEnabled(true) })

	cli.SetBannerEnabled(true)
	if cli.Banner() == "" {
		t.Fatal("the banner is empty with banners enabled")
	}
	cli.SetBannerEnabled(false)
	if got := cli.Banner(); got != "" {
		t.Fatalf("SetBannerEnabled(false) still rendered %q", got)
	}

	// And the root help body has to survive the swap: stripping the banner must
	// not take the usage text with it.
	body := strings.TrimLeft(rootLongBody, "\n")
	for _, want := range []string{"Designed for local SLMs", "deterministic exit codes"} {
		if !strings.Contains(body, want) {
			t.Errorf("the banner-free help body lost %q", want)
		}
	}
	if strings.Contains(body, "███") {
		t.Error("the banner-free help body still contains the ASCII logo")
	}
}

// TestEveryGroupRejectsAnUnknownSubcommand pins the contract the root comment
// claims: `slmcode <group> <typo>` is a usage error, not a cheerful listing.
//
// The guard used to skip any group that had its own default action, which was
// most of them — `slmcode blocks nosuchthing` printed the block listing and
// exited 0, so a script could not tell a typo from a success.
func TestEveryGroupRejectsAnUnknownSubcommand(t *testing.T) {
	groups := []*cobra.Command{
		agentCmd(), authCmd(), blockCmd(), configCmd(), contextCmd(), docsCmd(),
		evolveCmd(), hooksCmd(), memoryCmd(), metricsCmd(), sessionCmd(),
		skillsCmd(), stackCmd(), taskCmd(),
	}
	for _, g := range groups {
		rejectUnknownSubcommands(g)
		if g.Args == nil {
			t.Errorf("%q has no Args policy — an unknown subcommand would be accepted", g.Name())
			continue
		}
		if err := g.Args(g, []string{"definitely-not-a-subcommand"}); err == nil {
			t.Errorf("%q accepts an unknown subcommand", g.Name())
		} else if got := exitCodeFor(err); got != 2 {
			t.Errorf("%q rejects with exit %d, want the documented 2 (%v)", g.Name(), got, err)
		}
		// The bare form must still be allowed — these groups list something.
		if err := g.Args(g, nil); err != nil {
			t.Errorf("bare `slmcode %s` was rejected: %v", g.Name(), err)
		}
	}
}
