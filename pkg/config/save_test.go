package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// writeProject drops a raw config.yaml into a fresh project root.
func writeProject(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, DirName, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// isolateHome points HOME / XDG / SLMCODE_USER_CONFIG at a scratch dir so a
// developer's real ~/.slmcode/config.yaml cannot leak into a test.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SLMCODE_USER_CONFIG", "")
	return home
}

func TestSaveWritesOnlyIntent(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	cfg := Default(root)
	cfg.MaxParallel = 9

	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfg.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unreadable config: %v\n%s", err, data)
	}
	// config_version plus the one changed key — nothing else.
	if len(doc) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(doc), doc)
	}
	if doc["max_parallel"] != 9 {
		t.Fatalf("max_parallel = %v", doc["max_parallel"])
	}
	if doc["config_version"] != CurrentConfigVersion {
		t.Fatalf("config_version = %v", doc["config_version"])
	}
	if _, ok := doc["root"]; ok {
		t.Fatal("root must never be persisted: it is another machine's path")
	}
	if !strings.Contains(string(data), "slmcode config show --all") {
		t.Fatal("header pointing at `config show --all` is missing")
	}
}

func TestSaveRoundTripsEveryFieldType(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	cfg := Default(root)
	cfg.MaxParallel = 12                       // int
	cfg.Temperature = 0.55                     // float
	cfg.ArchitectEditor = true                 // bool
	cfg.QABootstrap = QABootstrapAuto          // enum
	cfg.EscalateAskTimeout = 11 * time.Minute  // duration
	cfg.EnabledModels = []string{"a:1", "b:2"} // string[]
	cfg.ContextRoleBudget = map[string]int{"worker": 90}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case got.MaxParallel != 12:
		t.Errorf("max_parallel = %d", got.MaxParallel)
	case got.Temperature != 0.55:
		t.Errorf("temperature = %v", got.Temperature)
	case !got.ArchitectEditor:
		t.Error("architect_editor lost")
	case got.QABootstrap != QABootstrapAuto:
		t.Errorf("qa_bootstrap = %q", got.QABootstrap)
	case got.EscalateAskTimeout != 11*time.Minute:
		t.Errorf("escalate_ask_timeout = %s", got.EscalateAskTimeout)
	case len(got.EnabledModels) != 2:
		t.Errorf("enabled_models = %v", got.EnabledModels)
	case got.ContextRoleBudget["worker"] != 90:
		t.Errorf("context_role_budget = %v", got.ContextRoleBudget)
	}
	if got.Root != root {
		t.Errorf("root = %q, want the directory it was loaded from", got.Root)
	}
}

func TestSaveWritesDurationsReadably(t *testing.T) {
	isolateHome(t)
	cfg := Default(t.TempDir())
	cfg.ShellTimeout = 90 * time.Second
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg.ConfigPath())
	if !strings.Contains(string(data), "shell_timeout: 1m30s") {
		t.Fatalf("durations must be human-readable, got:\n%s", data)
	}
}

func TestSaveNeverPersistsSecrets(t *testing.T) {
	isolateHome(t)
	cfg := Default(t.TempDir())
	cfg.APIKey = "sk-super-secret"
	cfg.EmbeddingAPIKey = "sk-also-secret"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg.ConfigPath())
	if strings.Contains(string(data), "secret") {
		t.Fatalf("api key leaked into config.yaml:\n%s", data)
	}
}

func TestSaveDoesNotCopyTheUserLayerIntoTheProject(t *testing.T) {
	home := isolateHome(t)
	userPath := filepath.Join(home, DirName, "config.yaml")
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte("max_parallel: 8\nfast_model: tiny\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxParallel != 8 || cfg.FastModel != "tiny" {
		t.Fatalf("user layer not applied: parallel=%d fast=%q", cfg.MaxParallel, cfg.FastModel)
	}
	cfg.Verbose = true
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg.ConfigPath())
	if strings.Contains(string(data), "max_parallel") || strings.Contains(string(data), "fast_model") {
		t.Fatalf("inherited user values were copied into the project file:\n%s", data)
	}
	if !strings.Contains(string(data), "verbose: true") {
		t.Fatalf("the project's own choice is missing:\n%s", data)
	}
}

func TestSaveInitialWritesOnlyDetectedKeys(t *testing.T) {
	isolateHome(t)
	cfg := Default(t.TempDir())
	cfg.Provider = "ollama"
	cfg.Model = "qwen2.5-coder:14b"
	cfg.ActivePack = "go"
	normalize(cfg)
	if err := cfg.SaveInitial("provider", "model", "active_pack"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg.ConfigPath())
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"config_version", "provider", "model", "active_pack"} {
		if _, ok := doc[want]; !ok {
			t.Errorf("init config is missing %q:\n%s", want, data)
		}
	}
	// endpoint follows from provider, so init has no business writing it.
	if _, ok := doc["endpoint"]; ok {
		t.Errorf("init wrote a derived key:\n%s", data)
	}
	if len(doc) > 6 {
		t.Errorf("init config should stay minimal, got %d keys:\n%s", len(doc), data)
	}
}

func TestDiffReportsExplicitKeysOnly(t *testing.T) {
	isolateHome(t)
	cfg := Default(t.TempDir())
	cfg.MaxRetries = 9
	cfg.Evolve = false
	got := map[string]bool{}
	for _, k := range cfg.Diff() {
		got[k] = true
	}
	if !got["max_retries"] || !got["evolve"] {
		t.Fatalf("Diff missed a changed key: %v", cfg.Diff())
	}
	if got["provider"] || got["model"] {
		t.Fatalf("Diff reported an unchanged key: %v", cfg.Diff())
	}
}
