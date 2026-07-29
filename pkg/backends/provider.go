package backends

import (
	"fmt"
	"strings"
	"time"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
	"github.com/piotrlaczkowski/slmcode/pkg/config"
)

// RegisterLLM wires omlx / ollama / openai into a ProviderManager.
func RegisterLLM(m *llm.ProviderManager, cfg *config.Config) error {
	cfg.ResolveAPIKey()
	switch strings.ToLower(cfg.Provider) {
	case "omlx", "mlx":
		return registerOpenAICompat(m, "omlx", cfg)
	case "openai":
		return registerOpenAICompat(m, "openai", cfg)
	case "ollama":
		endpoint := cfg.Endpoint
		if strings.Contains(endpoint, "/v1") {
			endpoint = strings.TrimSuffix(strings.TrimSuffix(endpoint, "/v1"), "/")
		}
		if endpoint == "" {
			endpoint = "http://127.0.0.1:11434"
		}
		p, err := llm.NewOllamaProvider(&llm.ProviderConfig{
			Type:        "ollama",
			Endpoint:    endpoint,
			Model:       cfg.Model,
			Temperature: cfg.Temperature,
			MaxTokens:   cfg.MaxTokens,
			Timeout:     180 * time.Second,
		})
		if err != nil {
			return err
		}
		if err := m.RegisterProvider("ollama", p); err != nil {
			return err
		}
		return m.SetDefaultProvider("ollama")
	default:
		return fmt.Errorf("unsupported provider %q (omlx|ollama|openai)", cfg.Provider)
	}
}

func registerOpenAICompat(m *llm.ProviderManager, name string, cfg *config.Config) error {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = config.DefaultEndpoint
	}
	// go-openai expects base URL ending at /v1
	if !strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/v1") {
		endpoint = strings.TrimRight(endpoint, "/") + "/v1"
	}
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "local"
	}
	p, err := llm.NewOpenAIProvider(&llm.ProviderConfig{
		Type:        "openai",
		Name:        name,
		Endpoint:    endpoint,
		APIKey:      apiKey,
		Model:       cfg.Model,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		Timeout:     180 * time.Second,
	})
	if err != nil {
		return err
	}
	if err := m.RegisterProvider(name, p); err != nil {
		return err
	}
	// Also alias as provider name used in AgentConfig
	if name != cfg.Provider && cfg.Provider != "" {
		_ = m.RegisterProvider(cfg.Provider, p)
	}
	return m.SetDefaultProvider(name)
}
