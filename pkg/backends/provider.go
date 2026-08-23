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

// llmTimeout is the transport-level ceiling on a single HTTP call. It is a
// backstop only: the real per-call deadline is derived from the role's
// max_tokens and the model's observed tokens/sec by retryProvider (see
// EstimateTimeout), so a hung 1.5B no longer holds a worker slot for the old
// three-minute floor.
func llmTimeout(cfg *config.Config) time.Duration {
	d := MaxCallTimeout
	if cfg != nil && cfg.TaskTimeout > 0 && cfg.TaskTimeout < d {
		d = cfg.TaskTimeout
	}
	if d < MinCallTimeout {
		d = MinCallTimeout
	}
	return d
}

// retryPolicy maps config into the slmcode retry policy. Unlike the provider's
// own fixed-delay retry it classifies the failure first, so a 400
// context_length_exceeded is surfaced immediately instead of costing three full
// prefills against a local model.
func retryPolicy(cfg *config.Config) RetryPolicy {
	p := DefaultRetryPolicy()
	if cfg == nil {
		return p
	}
	if cfg.LLMRetryCount >= 0 {
		// Config counts retries; the policy counts attempts.
		p.MaxAttempts = cfg.LLMRetryCount + 1
	}
	if cfg.LLMRetryDelayMS > 0 {
		p.BaseDelay = time.Duration(cfg.LLMRetryDelayMS) * time.Millisecond
	}
	return p
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
	// Persist probed endpoint capabilities next to the rest of the workspace
	// state so capability negotiation costs one round of probes per machine,
	// not one per run. No caller wiring needed.
	if root := strings.TrimSpace(cfg.Root); root != "" {
		SetCapabilityCacheDir(cfg.SlmDir())
		// Observed decode rates persist beside them, so `slmcode doctor` can
		// report a measured tokens/sec instead of the pessimistic prior.
		SetThroughputCacheDir(cfg.SlmDir())
	}

	if config.IsOllama(name) {
		return registerOllama(m, cfg, true)
	}
	return registerOpenAICompat(m, name, cfg, true)
}

// EnsureAgentProviders registers any per-agent provider overrides that are not
// already present. Safe to call after RegisterLLM — never changes the default
// provider. Unknown names are treated as OpenAI-compatible gateways.
//
// When two agents share a friendly provider name (e.g. both `openai`) but use
// different endpoints or API keys, each gets a unique registry key
// (`openai@http://…`) so Complete() hits the correct backend. Model-only
// overrides on the active provider need no registration.
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
		ep := strings.TrimSpace(o.Endpoint)
		apiKey := strings.TrimSpace(o.APIKey)
		regKey := ProviderInstanceKey(name, ep, apiKey)
		if seen[regKey] {
			continue
		}
		if _, err := m.GetProvider(regKey); err == nil {
			seen[regKey] = true
			continue
		}
		agentCfg := *cfg
		agentCfg.Provider = name
		if strings.TrimSpace(o.Model) != "" {
			agentCfg.Model = strings.TrimSpace(o.Model)
		}
		if ep != "" {
			agentCfg.Endpoint = ep
		} else {
			agentCfg.Endpoint = config.DefaultEndpointFor(name)
		}
		if apiKey != "" {
			agentCfg.APIKey = apiKey
		}
		agentCfg.ResolveAPIKey()
		var err error
		if config.IsOllama(name) {
			err = registerOllamaNamed(m, regKey, &agentCfg, false)
		} else {
			err = registerOpenAICompat(m, regKey, &agentCfg, false)
		}
		if err != nil {
			return fmt.Errorf("register agent provider %q: %w", regKey, err)
		}
		// Keep friendly name usable when nothing is registered under it yet
		// (agents with empty endpoint still resolve to the bare name).
		if regKey != name {
			if _, err := m.GetProvider(name); err != nil {
				if p, gerr := m.GetProvider(regKey); gerr == nil {
					_ = m.RegisterProvider(name, p)
					if meta, ok := lookupBackend(regKey); ok {
						rememberBackend(name, meta)
					}
				}
			}
		}
		seen[regKey] = true
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
	raw, err := llm.NewOllamaProvider(&llm.ProviderConfig{
		Type:        "ollama",
		Endpoint:    endpoint,
		Model:       cfg.Model,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		Timeout:     llmTimeout(cfg),
		// Retry is owned by retryProvider, which classifies the failure first.
		// Leaving the provider's own fixed-delay retry on would multiply attempts.
		RetryCount: 0,
		RetryDelay: 0,
	})
	if err != nil {
		return err
	}
	p := NewRetryProvider(raw, regName, cfg.Model, retryPolicy(cfg))
	if err := m.RegisterProvider(regName, p); err != nil {
		return err
	}
	meta := backendMeta{Provider: "ollama", Endpoint: endpoint, Model: cfg.Model, APIKey: cfg.APIKey}
	rememberBackend(regName, meta)
	// Dual-register instance key so agents with explicit same endpoint resolve.
	inst := ProviderInstanceKey("ollama", endpoint, cfg.APIKey)
	if inst != regName {
		if _, err := m.GetProvider(inst); err != nil {
			_ = m.RegisterProvider(inst, p)
			rememberBackend(inst, meta)
		}
	}
	if setDefault {
		return m.SetDefaultProvider(regName)
	}
	return nil
}

func registerOpenAICompat(m *llm.ProviderManager, name string, cfg *config.Config, setDefault bool) error {
	regName := name
	if regName == "" {
		regName = "openai"
	}
	baseName := config.NormalizeProvider(strings.Split(regName, "@")[0])
	if baseName == "" {
		baseName = regName
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = config.DefaultEndpointFor(baseName)
	}
	// go-openai expects base URL ending at /v1
	if !strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/v1") {
		endpoint = strings.TrimRight(endpoint, "/") + "/v1"
	}
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = "local"
	}
	raw, err := llm.NewOpenAIProvider(&llm.ProviderConfig{
		Type:        "openai",
		Name:        regName,
		Endpoint:    endpoint,
		APIKey:      apiKey,
		Model:       cfg.Model,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		Timeout:     llmTimeout(cfg),
		// Retry lives in retryProvider (classified, jittered, Retry-After aware).
		RetryCount: 0,
		RetryDelay: 0,
	})
	if err != nil {
		return err
	}
	p := NewRetryProvider(raw, regName, cfg.Model, retryPolicy(cfg))
	if err := m.RegisterProvider(regName, p); err != nil {
		return err
	}
	meta := backendMeta{Provider: baseName, Endpoint: endpoint, Model: cfg.Model, APIKey: apiKey}
	rememberBackend(regName, meta)
	// Dual-register canonical instance key (friendly name may already be regName).
	inst := ProviderInstanceKey(baseName, endpoint, apiKey)
	if inst != regName {
		if _, err := m.GetProvider(inst); err != nil {
			_ = m.RegisterProvider(inst, p)
			rememberBackend(inst, meta)
		}
	}
	// Alias only true synonyms of THIS provider — never map openai↔omlx.
	// Cross-mapping broke per-agent provider overrides (agent asked for openai
	// but hit the omlx endpoint). Skip synonyms for uniquified instance keys.
	if !strings.Contains(regName, "@") && !strings.Contains(regName, "#") {
		for _, alias := range providerSynonyms(regName) {
			if alias == "" || alias == regName {
				continue
			}
			if _, err := m.GetProvider(alias); err == nil {
				continue
			}
			_ = m.RegisterProvider(alias, p)
			rememberBackend(alias, meta)
		}
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
