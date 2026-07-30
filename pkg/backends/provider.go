package backends

import (
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// AgentProviderOverride describes a per-agent LLM backend that must exist
// in the ProviderManager at orchestrator rebuild time.
type AgentProviderOverride struct {
	Provider string
	Model    string
	Endpoint string
	APIKey   string
}

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
		return registerOllama(m, cfg, true)
	}
	return registerOpenAICompat(m, name, cfg, true)
}

// EnsureAgentProviders registers any per-agent provider overrides that are not
// already present. Safe to call after RegisterLLM — never changes the default
// provider. Unknown names are treated as OpenAI-compatible gateways.
//
// Model-only overrides on the active provider need no registration (AgentConfig.Model
// is enough). A different provider name (e.g. agent=ollama while global=omlx) is
// auto-registered with the agent's endpoint or DefaultEndpointFor(provider).
func EnsureAgentProviders(m *llm.ProviderManager, cfg *config.Config, overrides []AgentProviderOverride) error {
	if m == nil || cfg == nil {
		return nil
	}
	cfg.ResolveAPIKey()
	seen := map[string]bool{}
	for _, o := range overrides {
		name := config.NormalizeProvider(o.Provider)
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		if _, err := m.GetProvider(name); err == nil {
			seen[name] = true
			continue
		}
		agentCfg := *cfg
		agentCfg.Provider = name
		if strings.TrimSpace(o.Model) != "" {
			agentCfg.Model = strings.TrimSpace(o.Model)
		}
		if strings.TrimSpace(o.Endpoint) != "" {
			agentCfg.Endpoint = strings.TrimSpace(o.Endpoint)
		} else {
			agentCfg.Endpoint = config.DefaultEndpointFor(name)
		}
		if strings.TrimSpace(o.APIKey) != "" {
			agentCfg.APIKey = strings.TrimSpace(o.APIKey)
		}
		agentCfg.ResolveAPIKey()
		var err error
		if config.IsOllama(name) {
			err = registerOllamaNamed(m, name, &agentCfg, false)
		} else {
			err = registerOpenAICompat(m, name, &agentCfg, false)
		}
		if err != nil {
			return fmt.Errorf("register agent provider %q: %w", name, err)
		}
		seen[name] = true
	}
	return nil
}

func registerOllama(m *llm.ProviderManager, cfg *config.Config, setDefault bool) error {
	return registerOllamaNamed(m, "ollama", cfg, setDefault)
}

func registerOllamaNamed(m *llm.ProviderManager, regName string, cfg *config.Config, setDefault bool) error {
	endpoint := cfg.Endpoint
	if strings.Contains(endpoint, "/v1") {
		endpoint = strings.TrimSuffix(strings.TrimSuffix(endpoint, "/v1"), "/")
	}
	if endpoint == "" {
		endpoint = config.DefaultEndpointFor("ollama")
	}
	if regName == "" {
		regName = "ollama"
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
	if err := m.RegisterProvider(regName, p); err != nil {
		return err
	}
	if setDefault {
		return m.SetDefaultProvider(regName)
	}
	return nil
}

func registerOpenAICompat(m *llm.ProviderManager, name string, cfg *config.Config, setDefault bool) error {
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
	// Alias only true synonyms of THIS provider — never map openai↔omlx.
	// Cross-mapping broke per-agent provider overrides (agent asked for openai
	// but hit the omlx endpoint).
	for _, alias := range providerSynonyms(regName) {
		if alias == "" || alias == regName {
			continue
		}
		if _, err := m.GetProvider(alias); err == nil {
			continue
		}
		_ = m.RegisterProvider(alias, p)
	}
	if setDefault {
		return m.SetDefaultProvider(regName)
	}
	return nil
}

func providerSynonyms(name string) []string {
	switch config.NormalizeProvider(name) {
	case "omlx":
		return []string{"mlx"}
	case "openai":
		return []string{"openai-compatible", "openai_compat"}
	case "lmstudio":
		return []string{"lm-studio", "lm_studio"}
	default:
		return nil
	}
}
