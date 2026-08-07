// Package models discovers and searches LLM ids for the active provider,
// with auth-aware status (prime-agent find_models style, SLMCode-native).
package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/authstore"
	"github.com/UnicoLab/slmcode/pkg/config"
)

// Match is one selectable model (provider/id style).
type Match struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Selector string `json:"selector"` // "provider/id"
}

// AuthStatus reports whether the active provider has credentials.
type AuthStatus struct {
	Provider   string `json:"provider"`
	Configured bool   `json:"configured"`
	Required   bool   `json:"required"`
	Source     string `json:"source,omitempty"` // config | environment | omlx_settings | none | local
	EnvKey     string `json:"env_key,omitempty"`
	HasAPIKey  bool   `json:"has_api_key"`
	Message    string `json:"message,omitempty"`
}

// Catalog is the /api/models response shape.
type Catalog struct {
	Models        []string    `json:"models"`
	Matches       []Match     `json:"matches"`
	Current       string      `json:"current"`
	Provider      string      `json:"provider"`
	Endpoint      string      `json:"endpoint"`
	ActiveStack   string      `json:"active_stack,omitempty"`
	Query         string      `json:"query,omitempty"`
	Auth          AuthStatus  `json:"auth"`
	EnabledModels []string    `json:"enabled_models,omitempty"`
	Costs         []ModelCost `json:"costs,omitempty"`
	Error         string      `json:"error,omitempty"`
}

// ResolveAuth inspects config + env + auth.json for the active provider (does not mutate cfg).
func ResolveAuth(cfg *config.Config) AuthStatus {
	if cfg == nil {
		return AuthStatus{Configured: false, Source: "none"}
	}
	p := config.NormalizeProvider(cfg.Provider)
	st := AuthStatus{
		Provider: p,
		Required: requiresAPIKey(p),
		EnvKey:   envKeyFor(p),
	}
	hadConfigKey := strings.TrimSpace(cfg.APIKey) != "" && cfg.APIKey != "***"
	envKey := ""
	if st.EnvKey != "" {
		envKey = os.Getenv(st.EnvKey)
	}
	if envKey == "" {
		envKey = os.Getenv("SLMCODE_API_KEY")
	}
	authJSONKey, hasAuthJSON := authstore.Get(cfg.SlmDir(), p)

	cp := *cfg
	cp.APIKey = cfg.APIKey
	if cp.APIKey == "***" {
		cp.APIKey = ""
	}
	cp.ResolveAPIKey()
	st.HasAPIKey = strings.TrimSpace(cp.APIKey) != ""
	switch {
	case !st.Required:
		st.Configured = true
		st.Source = "local"
		st.Message = "local provider — API key optional"
	case hadConfigKey:
		st.Configured = true
		st.Source = "config"
	case envKey != "":
		st.Configured = true
		st.Source = "environment"
		if p == "omlx" {
			st.Source = "omlx_settings"
		}
	case hasAuthJSON && authJSONKey != "":
		st.Configured = true
		st.Source = "auth.json"
	case st.HasAPIKey:
		st.Configured = true
		st.Source = "environment"
		if p == "omlx" {
			st.Source = "omlx_settings"
		}
	default:
		st.Configured = false
		st.Source = "none"
		if st.EnvKey != "" {
			st.Message = "missing API key — set " + st.EnvKey + ", config.api_key, or .slmcode/auth.json"
		} else {
			st.Message = "missing API key"
		}
	}
	return st
}

func requiresAPIKey(provider string) bool {
	switch config.NormalizeProvider(provider) {
	case "omlx", "ollama", "lmstudio", "vllm", "litellm", "custom":
		return false
	default:
		return true
	}
}

func envKeyFor(provider string) string {
	switch config.NormalizeProvider(provider) {
	case "openai":
		return "OPENAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "together":
		return "TOGETHER_API_KEY"
	case "gemini", "google":
		return "GOOGLE_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "omlx":
		return "OMLX_API_KEY"
	default:
		return "SLMCODE_API_KEY"
	}
}

// Fetch lists models from the provider endpoint.
func Fetch(ctx context.Context, cfg *config.Config) (names []string, err error) {
	if cfg == nil {
		return nil, fmt.Errorf("config required")
	}
	cp := *cfg
	cp.ResolveAPIKey()
	endpoint := strings.TrimRight(cp.Endpoint, "/")
	url := endpoint + "/models"
	if config.IsOllama(cp.Provider) {
		base := strings.TrimSuffix(endpoint, "/v1")
		url = strings.TrimRight(base, "/") + "/api/tags"
	} else if !strings.HasSuffix(endpoint, "/v1") {
		url = endpoint + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if cp.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cp.APIKey)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return parseModelNames(body), nil
}

func parseModelNames(body []byte) []string {
	var names []string
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return names
	}
	if data, ok := payload["data"].([]any); ok {
		for _, m := range data {
			if mm, ok := m.(map[string]any); ok {
				if id, ok := mm["id"].(string); ok {
					names = append(names, id)
				}
			}
		}
	}
	if models, ok := payload["models"].([]any); ok {
		for _, m := range models {
			if mm, ok := m.(map[string]any); ok {
				if id, ok := mm["name"].(string); ok {
					names = append(names, id)
				} else if id, ok := mm["model"].(string); ok {
					names = append(names, id)
				}
			}
		}
	}
	return names
}

// Find filters model ids by query (substring, case-insensitive) with optional limit.
// When auth is required and missing, returns empty matches + auth status (fail closed).
func Find(ctx context.Context, cfg *config.Config, query string, limit int) Catalog {
	out := Catalog{
		Current:  "",
		Provider: "",
		Endpoint: "",
		Query:    strings.TrimSpace(query),
		Models:   []string{},
		Matches:  []Match{},
	}
	if cfg == nil {
		out.Error = "config required"
		return out
	}
	out.Current = cfg.Model
	out.Provider = config.NormalizeProvider(cfg.Provider)
	out.Endpoint = cfg.Endpoint
	out.ActiveStack = cfg.ActiveStack
	out.EnabledModels = append([]string{}, cfg.EnabledModels...)
	out.Auth = ResolveAuth(cfg)

	if out.Auth.Required && !out.Auth.Configured {
		out.Error = out.Auth.Message
		// Fail closed for cloud providers — do not invent catalog noise.
		if cfg.Model != "" && ModelAllowed(cfg.Model, cfg.EnabledModels) {
			out.Models = []string{cfg.Model}
			out.Matches = []Match{toMatch(out.Provider, cfg.Model)}
			out.Costs = []ModelCost{LookupCost(cfg, cfg.Model)}
		}
		return out
	}

	names, err := Fetch(ctx, cfg)
	if err != nil {
		out.Error = err.Error()
		if cfg.Model != "" {
			names = []string{cfg.Model}
		}
	}
	if len(names) == 0 && cfg.Model != "" {
		names = []string{cfg.Model}
	}
	names = FilterEnabled(names, cfg.EnabledModels)

	q := strings.ToLower(strings.TrimSpace(query))
	var filtered []string
	for _, n := range names {
		if q == "" || strings.Contains(strings.ToLower(n), q) ||
			strings.Contains(strings.ToLower(out.Provider+"/"+n), q) {
			filtered = append(filtered, n)
		}
	}
	sort.Strings(filtered)
	if limit <= 0 {
		limit = 64
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	out.Models = filtered
	for _, n := range filtered {
		out.Matches = append(out.Matches, toMatch(out.Provider, n))
		out.Costs = append(out.Costs, LookupCost(cfg, n))
	}
	return out
}

// ParseSelector splits "provider/model" or bare "model".
func ParseSelector(sel string) (provider, model string) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return "", ""
	}
	if i := strings.Index(sel, "/"); i > 0 {
		return config.NormalizeProvider(sel[:i]), strings.TrimSpace(sel[i+1:])
	}
	return "", sel
}

func toMatch(provider, id string) Match {
	p := config.NormalizeProvider(provider)
	return Match{
		Provider: p,
		ID:       id,
		Name:     id,
		Selector: p + "/" + id,
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
