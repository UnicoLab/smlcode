package backends

import (
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// llmTimeout aligns HTTP LLM calls with task timeout so multi-iteration
// ReAct workers are not killed mid-tool-loop by a short provider deadline.
func llmTimeout(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.TaskTimeout > 0 {
		// Single completion should finish before the whole task budget.
		d := cfg.TaskTimeout
		if d > 10*time.Minute {
			d = 10 * time.Minute
		}
		if d < 3*time.Minute {
			d = 3 * time.Minute
		}
		return d
	}
	return 5 * time.Minute
}

// RegisterLLM wires the configured provider into a ProviderManager.
//
// Supported:
//   - ollama — native Ollama HTTP API
//   - anything else — OpenAI-compatible Chat Completions (/v1), including
//     omlx, openai, lmstudio, openrouter, vllm, litellm, together, groq,
//     deepseek, and any custom OpenAI-compatible gateway.
func RegisterLLM(m *llm.ProviderManager, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	cfg.ResolveAPIKey()
	name := config.NormalizeProvider(cfg.Provider)
	cfg.Provider = name

	if config.IsOllama(name) {
		return registerOllama(m, cfg)
	}
	return registerOpenAICompat(m, name, cfg)
}

func registerOllama(m *llm.ProviderManager, cfg *config.Config) error {
	endpoint := cfg.Endpoint
	if strings.Contains(endpoint, "/v1") {
		endpoint = strings.TrimSuffix(strings.TrimSuffix(endpoint, "/v1"), "/")
	}
	if endpoint == "" {
		endpoint = config.DefaultEndpointFor("ollama")
	}
	p, err := llm.NewOllamaProvider(&llm.ProviderConfig{
		Type:        "ollama",
		Endpoint:    endpoint,
		Model:       cfg.Model,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		Timeout:     llmTimeout(cfg),
	})
	if err != nil {
		return err
	}
	if err := m.RegisterProvider("ollama", p); err != nil {
		return err
	}
	return m.SetDefaultProvider("ollama")
}

func registerOpenAICompat(m *llm.ProviderManager, name string, cfg *config.Config) error {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = config.DefaultEndpointFor(name)
	}
	// go-openai expects base URL ending at /v1
	if !strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/v1") {
		endpoint = strings.TrimRight(endpoint, "/") + "/v1"
	}
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "local"
	}
	regName := name
	if regName == "" {
		regName = "openai"
	}
	p, err := llm.NewOpenAIProvider(&llm.ProviderConfig{
		Type:        "openai",
		Name:        regName,
		Endpoint:    endpoint,
		APIKey:      apiKey,
		Model:       cfg.Model,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		Timeout:     llmTimeout(cfg),
	})
	if err != nil {
		return err
	}
	if err := m.RegisterProvider(regName, p); err != nil {
		return err
	}
	// Alias common synonyms so AgentConfig.Provider always resolves.
	for _, alias := range []string{"openai", "omlx", cfg.Provider} {
		alias = config.NormalizeProvider(alias)
		if alias == "" || alias == regName {
			continue
		}
		_ = m.RegisterProvider(alias, p)
	}
	return m.SetDefaultProvider(regName)
}
