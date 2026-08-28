package backends

import (
	"net/url"
	"strings"
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

func TestProviderInstanceKey(t *testing.T) {
	if got := ProviderInstanceKey("openai", "", ""); got != "openai" {
		t.Fatalf("empty endpoint → friendly name, got %q", got)
	}
	k1 := ProviderInstanceKey("openai", "http://127.0.0.1:9000", "")
	k2 := ProviderInstanceKey("openai", "http://127.0.0.1:9001/v1", "")
	if k1 == "openai" || k2 == "openai" {
		t.Fatalf("explicit endpoints must uniquify: %q %q", k1, k2)
	}
	if k1 == k2 {
		t.Fatalf("different endpoints must differ: %q", k1)
	}
	if !strings.HasPrefix(k1, "openai@") || !strings.Contains(k1, "9000") {
		t.Fatalf("key shape: %q", k1)
	}
	// Trailing slash / missing /v1 normalize to same key.
	if ProviderInstanceKey("openai", "http://host:1", "") != ProviderInstanceKey("openai", "http://host:1/v1/", "") {
		t.Fatal("endpoint canonicalization")
	}
	withKey := ProviderInstanceKey("openai", "https://api.openai.com/v1", "sk-secret")
	if !strings.Contains(withKey, "#") {
		t.Fatalf("api key should fingerprint: %q", withKey)
	}
}

func TestSameProviderDifferentEndpointsGetDistinctBackends(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Endpoint = config.DefaultEndpointFor("omlx")
	m := llm.NewProviderManager()
	if err := RegisterLLM(m, cfg); err != nil {
		t.Fatal(err)
	}
	epA := "http://127.0.0.1:9000/v1"
	epB := "http://127.0.0.1:9001/v1"
	overrides := []AgentProviderOverride{
		{Provider: "openai", Model: "model-a", Endpoint: epA, APIKey: "key-a"},
		{Provider: "openai", Model: "model-b", Endpoint: epB, APIKey: "key-b"},
	}
	if err := EnsureAgentProviders(m, cfg, overrides); err != nil {
		t.Fatal(err)
	}
	keyA := ProviderInstanceKey("openai", epA, "key-a")
	keyB := ProviderInstanceKey("openai", epB, "key-b")
	if keyA == keyB {
		t.Fatalf("keys collided: %q", keyA)
	}
	pa, err := m.GetProvider(keyA)
	if err != nil {
		t.Fatalf("missing %s: %v", keyA, err)
	}
	pb, err := m.GetProvider(keyB)
	if err != nil {
		t.Fatalf("missing %s: %v", keyB, err)
	}
	ea, _ := pa.GetConfig()["endpoint"].(string)
	eb, _ := pb.GetConfig()["endpoint"].(string)
	if ea == "" || eb == "" || ea == eb {
		t.Fatalf("backends must hit different endpoints: %q vs %q", ea, eb)
	}
	if !strings.Contains(ea, "9000") || !strings.Contains(eb, "9001") {
		t.Fatalf("endpoint mismatch: %q %q", ea, eb)
	}
	// Agent Complete() uses ResolveAgentProviderKey — must match registered keys.
	if got := ResolveAgentProviderKey("omlx", "openai", epA, "key-a"); got != keyA {
		t.Fatalf("resolve A: %q want %q", got, keyA)
	}
	if got := ResolveAgentProviderKey("omlx", "openai", epB, "key-b"); got != keyB {
		t.Fatalf("resolve B: %q want %q", got, keyB)
	}
}

// ── Shaping a configured endpoint into a provider base URL ───────────────
//
// "127.0.0.1:1234/v1" is a spelling config files genuinely carry — pkg/config
// tolerates it when deciding whether an endpoint is local — and net/url refuses
// to build a request from one. This is the path EVERY model call goes through,
// so without a scheme the whole harness failed on a config that reads as
// perfectly correct.

func TestOpenAIBaseURLShapesWhatTheClientNeeds(t *testing.T) {
	for _, tc := range []struct{ endpoint, provider, want string }{
		// The bug: scheme-less spellings.
		{"127.0.0.1:1234/v1", "lmstudio", "http://127.0.0.1:1234/v1"},
		{"localhost:8000", "omlx", "http://localhost:8000/v1"},
		// go-openai wants the base to end at /v1.
		{"http://127.0.0.1:8000", "omlx", "http://127.0.0.1:8000/v1"},
		{"http://127.0.0.1:8000/", "omlx", "http://127.0.0.1:8000/v1"},
		{"http://127.0.0.1:8000/v1/", "omlx", "http://127.0.0.1:8000/v1"},
		// Already correct: untouched, https preserved.
		{"https://api.openai.com/v1", "openai", "https://api.openai.com/v1"},
		// Empty falls back to the provider default.
		{"", "openai", "https://api.openai.com/v1"},
		{"   ", "ollama", "http://127.0.0.1:11434/v1"},
	} {
		if got := openAIBaseURL(tc.endpoint, tc.provider); got != tc.want {
			t.Errorf("openAIBaseURL(%q, %q) = %q, want %q", tc.endpoint, tc.provider, got, tc.want)
		}
	}
}

// Ollama serves its own API at the root, so /v1 must come OFF rather than go on.
func TestOllamaBaseURLDropsTheV1Suffix(t *testing.T) {
	for _, tc := range []struct{ endpoint, want string }{
		{"127.0.0.1:11434", "http://127.0.0.1:11434"},
		{"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434"},
		// A trailing slash after /v1 used to leave the suffix on, sending every
		// Ollama call to /v1/api/tags.
		{"http://127.0.0.1:11434/v1/", "http://127.0.0.1:11434"},
		{"127.0.0.1:11434/v1/", "http://127.0.0.1:11434"},
		{"http://127.0.0.1:11434", "http://127.0.0.1:11434"},
		{"", "http://127.0.0.1:11434"},
	} {
		if got := ollamaBaseURL(tc.endpoint); got != tc.want {
			t.Errorf("ollamaBaseURL(%q) = %q, want %q", tc.endpoint, got, tc.want)
		}
	}
}

// Whatever shape comes out, net/url has to accept it — that is the whole point.
func TestEveryShapedBaseURLParses(t *testing.T) {
	for _, ep := range []string{"127.0.0.1:1234/v1", "localhost:8000", "http://x/v1", ""} {
		for name, got := range map[string]string{
			"openai": openAIBaseURL(ep, "omlx"),
			"ollama": ollamaBaseURL(ep),
		} {
			u, err := url.Parse(got + "/models")
			if err != nil {
				t.Errorf("%s base for %q = %q, which url.Parse rejects: %v", name, ep, got, err)
				continue
			}
			if u.Host == "" || u.Scheme == "" {
				t.Errorf("%s base for %q = %q parsed with no host/scheme", name, ep, got)
			}
		}
	}
}

func TestRegistrationAcceptsASchemelessEndpoint(t *testing.T) {
	for _, provider := range []string{"openai", "ollama"} {
		cfg := config.Default(t.TempDir())
		cfg.Provider = provider
		cfg.Endpoint = "127.0.0.1:1234/v1"
		m := llm.NewProviderManager()
		var err error
		if provider == "ollama" {
			err = registerOllamaNamed(m, provider, cfg, true)
		} else {
			err = registerOpenAICompat(m, provider, cfg, true)
		}
		if err != nil {
			t.Errorf("register %s with a scheme-less endpoint: %v", provider, err)
		}
	}
}
