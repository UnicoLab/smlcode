package backends

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

func TestEnsureAgentProvidersRegistersMissing(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Model = "local-model"
	cfg.Endpoint = config.DefaultEndpointFor("omlx")
	m := llm.NewProviderManager()
	if err := RegisterLLM(m, cfg); err != nil {
		t.Fatal(err)
	}
	// ollama is NOT registered yet — agent override must create it.
	if _, err := m.GetProvider("ollama"); err == nil {
		t.Fatal("ollama should not be pre-registered for omlx default")
	}
	if err := EnsureAgentProviders(m, cfg, []AgentProviderOverride{{
		Provider: "ollama",
		Model:    "qwen2.5-coder:7b",
		Endpoint: "http://127.0.0.1:11434",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetProvider("ollama"); err != nil {
		t.Fatalf("ollama not registered: %v", err)
	}
	// Default registration name must stay omlx (GetName() on OpenAIProvider is always "openai").
	if _, err := m.GetProvider("omlx"); err != nil {
		t.Fatalf("omlx missing after ensure: %v", err)
	}
	if _, err := m.GetDefaultProvider(); err != nil {
		t.Fatal(err)
	}
	// Idempotent
	if err := EnsureAgentProviders(m, cfg, []AgentProviderOverride{{Provider: "ollama"}}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureAgentProvidersOpenAICompatDistinct(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Endpoint = config.DefaultEndpointFor("omlx")
	m := llm.NewProviderManager()
	if err := RegisterLLM(m, cfg); err != nil {
		t.Fatal(err)
	}
	// Must NOT resolve "openai" to the omlx instance (old alias bug).
	if _, err := m.GetProvider("openai"); err == nil {
		t.Fatal("openai must not be aliased onto omlx")
	}
	if err := EnsureAgentProviders(m, cfg, []AgentProviderOverride{{
		Provider: "openai",
		Model:    "gpt-4o-mini",
		Endpoint: "https://api.openai.com/v1",
		APIKey:   "sk-test",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetProvider("openai"); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterLLMOpenAICompatAliases(t *testing.T) {
	cases := []string{"omlx", "openai", "lmstudio", "openrouter", "vllm", "my-custom-gateway"}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			cfg := config.Default(t.TempDir())
			cfg.Provider = p
			cfg.Model = "test-model"
			cfg.Endpoint = config.DefaultEndpointFor(p)
			cfg.APIKey = "test-key"
			m := llm.NewProviderManager()
			if err := RegisterLLM(m, cfg); err != nil {
				t.Fatalf("RegisterLLM(%s): %v", p, err)
			}
			got := config.NormalizeProvider(cfg.Provider)
			if got != config.NormalizeProvider(p) {
				t.Fatalf("provider normalized to %q want %q", got, p)
			}
			if _, err := m.GetProvider(got); err != nil {
				t.Fatalf("GetProvider(%s): %v", got, err)
			}
			if _, err := m.GetDefaultProvider(); err != nil {
				t.Fatalf("GetDefaultProvider: %v", err)
			}
		})
	}
}

func TestRegisterLLMOllama(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Provider = "ollama"
	cfg.Model = "qwen2.5-coder:7b"
	cfg.Endpoint = "http://127.0.0.1:11434"
	m := llm.NewProviderManager()
	if err := RegisterLLM(m, cfg); err != nil {
		t.Fatalf("RegisterLLM(ollama): %v", err)
	}
}

func TestNormalizeProviderAliases(t *testing.T) {
	if config.NormalizeProvider("mlx") != "omlx" {
		t.Fatal("mlx")
	}
	if config.NormalizeProvider("openai-compatible") != "openai" {
		t.Fatal("openai-compatible")
	}
	if config.NormalizeProvider("LM-Studio") != "lmstudio" {
		t.Fatal("lm-studio")
	}
	if !config.IsOpenAICompat("vllm") || config.IsOllama("vllm") {
		t.Fatal("vllm should be openai-compat")
	}
	if !config.IsOllama("ollama") {
		t.Fatal("ollama")
	}
}
