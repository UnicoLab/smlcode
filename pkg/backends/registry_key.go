package backends

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// ProviderInstanceKey returns the ProviderManager registration name for a
// provider+endpoint(+apiKey) combination.
//
// UX keeps friendly names in YAML/UI (`provider: openai`). When an agent uses a
// different endpoint or API key than the bare name alone would imply, the system
// derives a unique key such as:
//
//	openai@http://127.0.0.1:9000/v1
//	openai@https://api.openai.com/v1#a1b2c3d4
//
// Empty endpoint (and local/empty API key) → friendly name only, so agents share
// the default RegisterLLM slot.
func ProviderInstanceKey(provider, endpoint, apiKey string) string {
	name := config.NormalizeProvider(provider)
	if name == "" {
		name = "openai"
	}
	ep := canonicalEndpoint(name, endpoint)
	fp := apiKeyFingerprint(apiKey)
	if ep == "" && fp == "" {
		return name
	}
	key := name
	if ep != "" {
		key = name + "@" + ep
	}
	if fp != "" {
		key = key + "#" + fp
	}
	return key
}

// canonicalEndpoint normalizes an endpoint for registry keys.
// Empty input stays empty (caller intends to use the friendly-name slot).
func canonicalEndpoint(provider, endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return ""
	}
	ep = strings.TrimRight(ep, "/")
	if config.IsOllama(provider) {
		ep = strings.TrimSuffix(ep, "/v1")
		ep = strings.TrimRight(ep, "/")
		return ep
	}
	if !strings.HasSuffix(ep, "/v1") {
		ep = ep + "/v1"
	}
	return ep
}

func apiKeyFingerprint(apiKey string) string {
	k := strings.TrimSpace(apiKey)
	if k == "" || k == "local" || k == "***" {
		return ""
	}
	sum := sha256.Sum256([]byte(k))
	return hex.EncodeToString(sum[:4])
}

// ResolveAgentProviderKey picks the registry key an agent should use for Complete().
// Friendly provider names from YAML are preserved when endpoint is unset.
func ResolveAgentProviderKey(globalProvider, agentProvider, agentEndpoint, agentAPIKey string) string {
	name := strings.TrimSpace(agentProvider)
	if name == "" {
		name = globalProvider
	}
	return ProviderInstanceKey(name, agentEndpoint, agentAPIKey)
}
