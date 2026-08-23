package orchestrator

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// structured_decoding: off has to be ENFORCED, not merely reported.
//
// Orchestrator.StructuredDecoding() resolved the policy and pkg/config carried
// the field, but nothing acted on it: pkg/backends' structuredProvider probed
// the endpoint and used whatever mechanism it confirmed, so an operator who
// turned constrained decoding off because their gateway mangles
// `response_format` still got json_schema requests.
//
// The enforcement point is the capability cache: SelectMechanism returns
// prompt_only for a record with nothing enabled, and Probe consults the cache
// before it issues any HTTP. Seeding an all-false record for the configured
// backends therefore pins every role to prompt-only JSON.

// applyStructuredDecodingPolicy pins prompt-only decoding when the policy is
// "off". It is a no-op under "auto" (the default), which leaves pkg/backends to
// negotiate normally.
func (o *Orchestrator) applyStructuredDecodingPolicy() {
	if o == nil || o.cfg == nil {
		return
	}
	if o.StructuredDecoding() != config.DecodingOff {
		return
	}

	// Disk persistence is switched OFF first, deliberately. SetCapabilities
	// stamps Probed=now, and capabilities.json is honored for a week — so
	// persisting an all-false record would keep constrained decoding disabled
	// long after the operator set structured_decoding back to auto. The pin is
	// process-local and re-applied on every construction instead.
	backends.SetCapabilityCacheDir("")

	off := backends.Capabilities{Source: "structured_decoding=off"}
	seen := map[string]bool{}
	pin := func(provider, endpoint, model string) {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			return
		}
		key := backends.CapabilityKey(provider, endpoint, model)
		if seen[key] {
			return
		}
		seen[key] = true
		backends.SetCapabilities(provider, endpoint, model, off)
	}

	pin(o.cfg.Provider, o.cfg.Endpoint, o.cfg.Model)
	// Per-agent overrides talk to their own backends and would otherwise keep
	// negotiating, so an agent pointed at a second endpoint is pinned too.
	if o.factory != nil {
		for _, n := range o.factory.ProviderOverrides() {
			provider := n.Provider
			if strings.TrimSpace(provider) == "" {
				provider = o.cfg.Provider
			}
			ep := n.Endpoint
			if strings.TrimSpace(ep) == "" {
				ep = config.DefaultEndpointFor(config.NormalizeProvider(provider))
			}
			model := n.Model
			if strings.TrimSpace(model) == "" {
				model = o.cfg.Model
			}
			pin(provider, ep, model)
		}
	}

	o.emitFull("init", stream.KindDebug, "backends", "",
		"structured_decoding=off — constrained decoding disabled, every role uses prompt-only JSON",
		"", "")
}
