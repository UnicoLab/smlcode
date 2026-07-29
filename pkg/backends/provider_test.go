package backends

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

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
