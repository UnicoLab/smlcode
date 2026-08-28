// Package autoconfig finds a model server and picks a model to run against it.
//
// # WHY THIS EXISTS
//
// Every piece of this already existed and none of it was joined up. The harness
// could list an endpoint's models, measure what a (model, endpoint) pair can do,
// probe decoding support and check whether the configured endpoint is answering
// — but nothing could answer the question a new user actually has, which is
// "what do I put in the config". They got a default endpoint that may not be
// running, a default model that may not be served, and a refusal at the first
// run telling them the endpoint is down.
//
// So: find what is actually running, ask it what it serves, pick the model best
// suited to writing code, and write that down.
//
// The package is deliberately split. Everything that decides is pure and
// testable without a network; the one function that touches the wire takes the
// prober as an argument.
package autoconfig

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/models"
)

// Candidate is one (provider, endpoint) pair worth trying.
type Candidate struct {
	Provider string
	Endpoint string
	// Reason says why this is on the list, so the CLI can explain itself
	// rather than appearing to guess.
	Reason string
	// Local marks an endpoint on this machine. Credentials are never sent to
	// one unless it is the endpoint the user configured — see WantsKey.
	Local bool
}

// WantsKey reports whether this candidate should be sent the configured API key.
//
// A key belongs to the service it was issued for. Attaching the user's OpenAI
// key to a probe of `127.0.0.1:1234` because LM Studio might be listening there
// would hand a credential to whatever answers that port, which on a shared or
// compromised machine is not a hypothetical. So a key travels only to the
// endpoint the user already configured, or to a remote candidate offered
// because that provider's own key is present.
func (c Candidate) WantsKey(configured string) bool {
	if strings.EqualFold(strings.TrimRight(c.Endpoint, "/"), strings.TrimRight(configured, "/")) {
		return true
	}
	return !c.Local
}

// localProviders are the servers a developer actually runs on their own
// machine, in the order worth trying. Each is a distinct product listening on a
// distinct port, so a hit identifies the provider as well as the address.
var localProviders = []string{"omlx", "ollama", "lmstudio", "vllm"}

// Candidates lists what to probe, best guess first.
//
// The configured endpoint always leads: a user who set one is telling you where
// to look, and re-detecting around a working configuration is how a tool
// silently moves somebody's setup out from under them.
func Candidates(cfg *config.Config, envKey func(string) string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(c Candidate) {
		key := strings.ToLower(strings.TrimRight(c.Endpoint, "/"))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, c)
	}

	if cfg != nil && strings.TrimSpace(cfg.Endpoint) != "" {
		add(Candidate{
			Provider: config.NormalizeProvider(cfg.Provider),
			Endpoint: cfg.Endpoint,
			Reason:   "already configured",
			Local:    config.IsLocalEndpoint(cfg.Provider, cfg.Endpoint),
		})
	}

	for _, p := range localProviders {
		ep := config.DefaultEndpointFor(p)
		add(Candidate{Provider: p, Endpoint: ep, Reason: "default " + p + " address", Local: true})
	}

	// A remote provider is only worth probing when its key is present: without
	// one the listing is a guaranteed 401, and offering it would be noise.
	if envKey != nil {
		for _, p := range remoteProviders {
			if strings.TrimSpace(envKey(models.APIKeyEnvFor(p))) == "" {
				continue
			}
			add(Candidate{
				Provider: p,
				Endpoint: config.DefaultEndpointFor(p),
				Reason:   models.APIKeyEnvFor(p) + " is set",
			})
		}
	}
	return out
}

// remoteProviders are hosted services, probed only when their key is present.
var remoteProviders = []string{
	"openai", "openrouter", "groq", "together", "deepseek",
	"fireworks", "mistral", "gemini", "anthropic",
}
