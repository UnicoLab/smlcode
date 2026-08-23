package models

import (
	"context"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/schema"
)

// DecodingSupport is the structured-decoding view of the active endpoint, so
// Studio and `slmcode doctor` can show WHY a role is on prompt-only JSON
// instead of a schema — which is otherwise invisible until output quality drops.
type DecodingSupport struct {
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`

	JSONObject  bool `json:"json_object"`
	JSONSchema  bool `json:"json_schema"`
	GuidedJSON  bool `json:"guided_json"`
	GBNFGrammar bool `json:"gbnf_grammar"`
	NativeTools bool `json:"native_tools"`
	Streaming   bool `json:"streaming"`

	// Mechanism is what a strict contract (the reviewer's) would actually use.
	Mechanism string `json:"mechanism"`
	// Source is probe | cache | manual | demoted | unreachable | unprobed.
	Source string `json:"source"`
	// Probed reports whether these values were confirmed against the server.
	Probed bool `json:"probed"`
}

// DescribeDecoding reports the cached capability record for cfg's endpoint
// without issuing any HTTP. Use ProbeDecoding when a fresh answer is wanted.
func DescribeDecoding(cfg *config.Config) DecodingSupport {
	if cfg == nil {
		return DecodingSupport{Mechanism: backends.MechPromptOnly, Source: "unprobed"}
	}
	caps, ok := backends.CachedCapabilities(cfg.Provider, cfg.Endpoint, cfg.Model)
	if !ok {
		return DecodingSupport{
			Provider:  config.NormalizeProvider(cfg.Provider),
			Endpoint:  strings.TrimSpace(cfg.Endpoint),
			Model:     strings.TrimSpace(cfg.Model),
			Mechanism: backends.MechPromptOnly,
			Source:    "unprobed",
		}
	}
	return toDecodingSupport(cfg, caps)
}

// ProbeDecoding negotiates capabilities against the live endpoint (cached
// afterwards, so repeat calls are free). Never fatal: an unreachable endpoint
// reports prompt-only.
func ProbeDecoding(ctx context.Context, cfg *config.Config) DecodingSupport {
	if cfg == nil {
		return DecodingSupport{Mechanism: backends.MechPromptOnly, Source: "unprobed"}
	}
	cfg.ResolveAPIKey()
	caps := backends.Probe(ctx, cfg.Provider, cfg.Endpoint, cfg.Model, cfg.APIKey)
	return toDecodingSupport(cfg, caps)
}

func toDecodingSupport(cfg *config.Config, caps backends.Capabilities) DecodingSupport {
	spec, _ := schema.For(schema.RoleReview)
	src := caps.Source
	if src == "" {
		src = "unprobed"
	}
	return DecodingSupport{
		Provider:    config.NormalizeProvider(cfg.Provider),
		Endpoint:    strings.TrimSpace(cfg.Endpoint),
		Model:       strings.TrimSpace(cfg.Model),
		JSONObject:  caps.JSONObject,
		JSONSchema:  caps.JSONSchema,
		GuidedJSON:  caps.GuidedJSON,
		GBNFGrammar: caps.GBNFGrammar,
		NativeTools: caps.NativeTools,
		Streaming:   caps.Streaming,
		Mechanism:   caps.SelectMechanism(spec, nil),
		Source:      src,
		Probed:      !caps.Probed.IsZero(),
	}
}

// Summary renders a single human line for the CLI.
func (d DecodingSupport) Summary() string {
	var b strings.Builder
	b.WriteString(d.Mechanism)
	var extra []string
	if d.NativeTools {
		extra = append(extra, "tools")
	}
	if d.Streaming {
		extra = append(extra, "stream")
	}
	if len(extra) > 0 {
		b.WriteString(" (+")
		b.WriteString(strings.Join(extra, ", "))
		b.WriteString(")")
	}
	b.WriteString(" · ")
	b.WriteString(d.Source)
	return b.String()
}
