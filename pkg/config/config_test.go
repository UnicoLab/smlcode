package config

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/permissions"
)

func TestApplyPatchIgnoresRedactedAPIKey(t *testing.T) {
	c := Default(t.TempDir())
	c.APIKey = "real-secret"
	redacted := "***"
	c.ApplyPatch(Patch{APIKey: &redacted})
	if c.APIKey != "real-secret" {
		t.Fatalf("redacted key overwrote secret: %q", c.APIKey)
	}
	next := "new-secret"
	c.ApplyPatch(Patch{APIKey: &next})
	if c.APIKey != "new-secret" {
		t.Fatalf("api_key=%q", c.APIKey)
	}
}

func TestApplyPatchPartialDoesNotClearDryRun(t *testing.T) {
	c := Default(t.TempDir())
	c.DryRun = true
	c.Permission = permissions.ModeDryRun

	model := "test-model"
	c.ApplyPatch(Patch{Model: &model})

	if !c.DryRun {
		t.Fatal("partial model update cleared dry_run")
	}
	if c.Permission != permissions.ModeDryRun {
		t.Fatalf("permission=%s", c.Permission)
	}
	if c.Model != model {
		t.Fatalf("model=%s", c.Model)
	}
}

func TestApplyPatchPermissionSync(t *testing.T) {
	c := Default(t.TempDir())

	perm := permissions.ModeReview
	c.ApplyPatch(Patch{Permission: &perm})
	if c.Permission != permissions.ModeReview || c.DryRun {
		t.Fatalf("review: perm=%s dry=%v", c.Permission, c.DryRun)
	}

	dry := true
	c.ApplyPatch(Patch{DryRun: &dry})
	if c.Permission != permissions.ModeDryRun || !c.DryRun {
		t.Fatalf("dry-run: perm=%s dry=%v", c.Permission, c.DryRun)
	}

	off := false
	c.ApplyPatch(Patch{DryRun: &off})
	if c.Permission != permissions.ModeAuto || c.DryRun {
		t.Fatalf("clear dry-run: perm=%s dry=%v", c.Permission, c.DryRun)
	}
}

func TestApplyPatchMaxRetriesZero(t *testing.T) {
	c := Default(t.TempDir())
	zero := 0
	c.ApplyPatch(Patch{MaxRetries: &zero})
	if c.MaxRetries != 0 {
		t.Fatalf("max_retries=%d want 0", c.MaxRetries)
	}
}

func TestNormalizePermissionFromYAML(t *testing.T) {
	root := t.TempDir()
	c := Default(root)
	c.Permission = "dry-run"
	c.DryRun = false
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.DryRun || loaded.Permission != permissions.ModeDryRun {
		t.Fatalf("load sync: dry=%v perm=%s", loaded.DryRun, loaded.Permission)
	}
}

func TestProviderModelSelection(t *testing.T) {
	c := Default(t.TempDir())
	prov := "lmstudio"
	model := "local-coder"
	c.ApplyPatch(Patch{Provider: &prov, Model: &model})
	if c.Provider != "lmstudio" {
		t.Fatalf("provider=%s", c.Provider)
	}
	if c.Model != model {
		t.Fatalf("model=%s", c.Model)
	}
	if c.Endpoint != DefaultEndpointFor("lmstudio") {
		t.Fatalf("endpoint=%s", c.Endpoint)
	}

	openai := "openai-compatible"
	ep := "https://api.openai.com/v1"
	c.ApplyPatch(Patch{Provider: &openai, Endpoint: &ep})
	if c.Provider != "openai" {
		t.Fatalf("normalized provider=%s", c.Provider)
	}
	if c.Endpoint != ep {
		t.Fatalf("explicit endpoint overwritten: %s", c.Endpoint)
	}
}

func TestApplyEnvProviderModel(t *testing.T) {
	t.Setenv("SLMCODE_PROVIDER", "ollama")
	t.Setenv("SLMCODE_MODEL", "qwen2.5-coder:14b")
	t.Setenv("SLMCODE_ENDPOINT", "http://127.0.0.1:11434")
	c := Default(t.TempDir())
	c.ApplyEnv()
	if c.Provider != "ollama" || c.Model != "qwen2.5-coder:14b" || c.Endpoint != "http://127.0.0.1:11434" {
		t.Fatalf("env overlay failed: %+v", c)
	}
}

func TestDefaultEndpointFor(t *testing.T) {
	if DefaultEndpointFor("openai") != "https://api.openai.com/v1" {
		t.Fatal("openai")
	}
	if DefaultEndpointFor("ollama") != "http://127.0.0.1:11434" {
		t.Fatal("ollama")
	}
	if DefaultEndpointFor("openrouter") != "https://openrouter.ai/api/v1" {
		t.Fatal("openrouter")
	}
	if DefaultEndpointFor("my-gateway") != DefaultEndpoint {
		t.Fatal("unknown gateway should keep local default endpoint")
	}
}

func TestApplyEnvOpenAIBaseURL(t *testing.T) {
	t.Setenv("SLMCODE_PROVIDER", "openai")
	t.Setenv("SLMCODE_MODEL", "gpt-4o-mini")
	t.Setenv("SLMCODE_ENDPOINT", "")
	t.Setenv("OPENAI_BASE_URL", "https://proxy.example/v1")
	c := Default(t.TempDir())
	c.Provider = "omlx" // overwritten by env
	c.ApplyEnv()
	if c.Provider != "openai" || c.Model != "gpt-4o-mini" {
		t.Fatalf("provider/model: %s / %s", c.Provider, c.Model)
	}
	if c.Endpoint != "https://proxy.example/v1" {
		t.Fatalf("OPENAI_BASE_URL not applied: %s", c.Endpoint)
	}
	if !IsOpenAICompat(c.Provider) {
		t.Fatal("openai must stay openai-compat")
	}
}

func TestCustomGatewayStaysOpenAICompat(t *testing.T) {
	c := Default(t.TempDir())
	name := "corp-llm"
	ep := "https://llm.corp.internal/v1"
	model := "corp-coder"
	c.ApplyPatch(Patch{Provider: &name, Endpoint: &ep, Model: &model})
	if c.Provider != "corp-llm" {
		t.Fatalf("custom provider mutated: %s", c.Provider)
	}
	if !IsOpenAICompat(c.Provider) || IsOllama(c.Provider) {
		t.Fatal("custom gateway must route OpenAI-compat")
	}
	if c.Endpoint != ep || c.Model != model {
		t.Fatalf("endpoint/model: %s / %s", c.Endpoint, c.Model)
	}
}
