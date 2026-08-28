package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Endpoint-aware parallelism.
//
// max_parallel is how many role calls the orchestrator has in flight at once.
// It used to be a flat 4 for every backend, which quietly assumed that a second
// concurrent request lands on a second piece of hardware. That is true of a
// hosted API and false of a single local model server, where every extra
// request shares one GPU and one KV cache — so the fan-out buys a little
// throughput and pays for it in per-request latency.
//
// That is a correctness problem, not a tuning preference: role timeouts are
// wall-clock (see pkg/orchestrator/roletimeout.go), so inflating observed
// latency 2.5× is a direct cause of role timeouts. This file decides the
// DEFAULT only. An explicit setting — file, env, flag, patch or stack — always
// wins; see Config.SetMaxParallel and Config.MaxParallelExplicit.
//
// Since v0.19 the default is also only the FALLBACK: pkg/calibrate measures the
// real knee for the concrete (model, endpoint) pair and overrides it when the
// user has not pinned a value. This static table is what applies before any
// calibration exists, and when calibration is off or fails.

// IsLocalProvider reports whether a provider name always denotes a model server
// running as ONE process on the user's own machine.
//
// This is the single local-vs-hosted notion in the codebase; pkg/readiness and
// pkg/models both defer to it rather than keeping a second list.
func IsLocalProvider(provider string) bool {
	switch NormalizeProvider(provider) {
	case "local", "omlx", "ollama", "lmstudio", "vllm", "litellm", "custom",
		"llamacpp", "llama-cpp", "llama_cpp":
		// "mlx" normalizes to "omlx"; "lm-studio"/"lm_studio" to "lmstudio".
		return true
	default:
		return false
	}
}

// IsLocalEndpoint reports whether this (provider, endpoint) pair is a single
// local model server rather than a horizontally-scaled hosted API.
//
// The provider name decides first: an `ollama` reachable over the LAN is still
// one Ollama process. Otherwise the HOST decides, so a hosted-sounding name
// pointed at a loopback proxy (`provider: openai`, `endpoint:
// http://127.0.0.1:8000/v1`) is correctly treated as local — that shape is how
// most people front a local server with an OpenAI-compatible gateway, and the
// name says nothing about where the tokens are produced.
func IsLocalEndpoint(provider, endpoint string) bool {
	if IsLocalProvider(provider) {
		return true
	}
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		ep = DefaultEndpointFor(provider)
	}
	return isLoopbackHost(endpointHost(ep))
}

// NormalizeEndpoint gives a base URL a scheme when it has none.
//
// "127.0.0.1:1234/v1" is a spelling people genuinely put in config files, and
// this package already tolerates it when deciding whether an endpoint is local.
// Anything that BUILDS a URL from it did not: net/url refuses
// "127.0.0.1:1234/v1/models" with "first path segment in URL cannot contain
// colon", so a perfectly reachable server was reported as broken and
// auto-configuration walked past it to something else.
//
// http rather than https because the shape appears almost exclusively for
// local servers, and a local server serving TLS is the rarer case that people
// spell out.
func NormalizeEndpoint(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" || strings.Contains(ep, "://") {
		return ep
	}
	return "http://" + ep
}

// endpointHost extracts the lower-cased host from a base URL, tolerating the
// scheme-less spellings people put in config files ("127.0.0.1:1234/v1").
func endpointHost(endpoint string) string {
	ep := NormalizeEndpoint(endpoint)
	if ep == "" {
		return ""
	}
	u, err := url.Parse(ep)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(u.Hostname()))
}

// isLoopbackHost reports whether a host name resolves to this machine without
// leaving it.
//
// Only the exact name "localhost", literal loopback/unspecified IPs and the
// mDNS ".local" suffix qualify. A wildcard "*.localhost" is deliberately NOT
// accepted: pkg/server refuses it for the same reason (a public resolver may
// answer for "evil.localhost"), and having two different opinions about what
// counts as loopback in one binary is worse than being slightly conservative.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		// 0.0.0.0 / :: is a bind address for a server on this machine.
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// DefaultMaxParallelFor is the fan-out to use when the user has not chosen one.
//
// See the measurement table on DefaultMaxParallelLocal for where 2 comes from.
func DefaultMaxParallelFor(provider, endpoint string) int {
	if IsLocalEndpoint(provider, endpoint) {
		return DefaultMaxParallelLocal
	}
	return DefaultMaxParallel
}

// SetMaxParallel records an EXPLICIT max_parallel choice.
//
// Assigning Config.MaxParallel directly also works, but a later Normalize would
// re-derive the endpoint-aware default over it, because a bare field write
// carries no evidence that a human chose the number. Every layer that can carry
// a user's intent — file, env, flag, patch, stack — goes through this (or
// through Set, which calls it).
func (c *Config) SetMaxParallel(n int) {
	if c == nil || n <= 0 {
		return
	}
	c.MaxParallel = n
	c.maxParallelSet = true
}

// Explicit reports whether a key was supplied by a layer above the built-in
// defaults — a user file, a project file, an env var, a flag, a patch or a
// stack.
//
// It is the authority for every consumer that wants to improve on a default
// without overriding a choice; pkg/calibrate checks it before applying any
// measurement. Provenance already records the layer for each key, so this is a
// read of existing state rather than a second bookkeeping scheme. max_parallel
// is special-cased because its default is DERIVED and therefore has to be
// tracked at write time (see SetMaxParallel).
func (c *Config) Explicit(key string) bool {
	if c == nil {
		return false
	}
	if CanonicalKey(key) == "max_parallel" {
		return c.maxParallelSet
	}
	return c.prov.Layer(key) != LayerDefault
}

// MaxParallelExplicit reports whether max_parallel was chosen by the user at
// any layer, as opposed to inherited from the default.
//
// Consumers that want to improve on the default — calibration above all — must
// check this and stay out of the way when it is true.
func (c *Config) MaxParallelExplicit() bool {
	return c != nil && c.maxParallelSet
}

// MaxParallelNotice is the one line to print at startup when the endpoint-aware
// default LOWERED max_parallel, and "" when nothing was changed.
//
// It fires only for the case a user could be surprised by: an unset
// max_parallel on a local endpoint, resolving to something below the historical
// flat default. An explicit setting, a hosted endpoint, or a default that did
// not move all return "" — silence is the correct output when nothing happened.
//
// Callers print it ONCE per process (see printMaxParallelNotice in the CLI),
// never per wave.
func (c *Config) MaxParallelNotice() string {
	if c == nil || c.maxParallelSet {
		return ""
	}
	if !IsLocalEndpoint(c.Provider, c.Endpoint) {
		return ""
	}
	if c.MaxParallel != DefaultMaxParallelLocal || DefaultMaxParallelLocal >= DefaultMaxParallel {
		return ""
	}
	ep := strings.TrimSpace(c.Endpoint)
	if ep == "" {
		ep = DefaultEndpointFor(c.Provider)
	}
	return fmt.Sprintf(
		"max_parallel=%d (default %d): %s is a single local endpoint, which shares one GPU across concurrent calls — measured %d-way throughput was ~%d%% of ideal while per-request latency nearly doubled, and role timeouts are wall-clock. Override: slmcode config set max_parallel %d",
		c.MaxParallel, DefaultMaxParallel, ep,
		DefaultMaxParallel, measuredLocalEfficiencyPercent, DefaultMaxParallel)
}

// measuredLocalEfficiencyPercent is the 4-way scaling efficiency observed on
// the single-endpoint oMLX measurement documented on DefaultMaxParallelLocal
// (39% on a 9B, 37% on a 27B — the notice quotes the friendlier of the two).
const measuredLocalEfficiencyPercent = 39
