package main

import (
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

func TestMergedSchemaCoversPatchOnlyKeys(t *testing.T) {
	byKey := map[string]config.FieldSchema{}
	for _, f := range mergedSchema() {
		byKey[f.Key] = f
	}
	// These are settable through config.Patch but absent from config.Schema();
	// without them `config set parallel 6` silently did nothing.
	for _, key := range []string{
		"max_parallel", "max_retries", "think_passes", "permission",
		"shell_permission", "qa_gate", "mode", "listen", "dry_run",
	} {
		f, ok := byKey[key]
		if !ok {
			t.Errorf("merged schema is missing %q", key)
			continue
		}
		if f.Type == "" || !f.Patchable {
			t.Errorf("%q has no usable type/patchable flag: %+v", key, f)
		}
	}
}

func TestMergedSchemaKeepsUpstreamDefinitions(t *testing.T) {
	upstream := map[string]config.FieldSchema{}
	for _, f := range config.Schema() {
		upstream[f.Key] = f
	}
	for _, f := range mergedSchema() {
		if u, ok := upstream[f.Key]; ok {
			if f.Label != u.Label || len(f.Enum) != len(u.Enum) {
				t.Errorf("%q was overridden by the CLI table: %+v vs %+v", f.Key, f, u)
			}
		}
	}
}

func TestConfigSetRejectsGarbage(t *testing.T) {
	// The whole point of routing through the schema: a bad value is an error,
	// not a cheerful "✔ set parallel = abc".
	if _, _, err := configPatchFromSchemaValue("max_parallel", "abc"); err == nil {
		t.Fatal("expected an int parse error")
	}
	if _, _, err := configPatchFromSchemaValue("permission", "sudo"); err == nil {
		t.Fatal("expected an enum error")
	}
	if _, _, err := configPatchFromSchemaValue("qa_gate", "maybe"); err == nil {
		t.Fatal("expected a bool error")
	}
}

func TestConfigSetAcceptsValidValues(t *testing.T) {
	patch, ok, err := configPatchFromSchemaValue("max_parallel", "6")
	if err != nil || !ok || patch.MaxParallel == nil || *patch.MaxParallel != 6 {
		t.Fatalf("patch=%+v ok=%v err=%v", patch, ok, err)
	}
	patch, ok, err = configPatchFromSchemaValue("permission", "review")
	if err != nil || !ok || patch.Permission == nil || *patch.Permission != "review" {
		t.Fatalf("patch=%+v ok=%v err=%v", patch, ok, err)
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
		if got := canonicalConfigKey(in); got != want {
			t.Errorf("canonicalConfigKey(%q)=%q want %q", in, got, want)
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
