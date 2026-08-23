package models

import (
	"context"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/config"
)

func TestDescribeDecodingWithoutAProbe(t *testing.T) {
	backends.ResetCapabilityCache()
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Model = "qwen3-coder-30b"
	cfg.Endpoint = "http://127.0.0.1:9000/v1"

	got := DescribeDecoding(cfg)
	if got.Mechanism != backends.MechPromptOnly {
		t.Errorf("mechanism = %q, want prompt_only before any probe", got.Mechanism)
	}
	if got.Probed {
		t.Error("unprobed record claims to be probed")
	}
	if got.Provider != "omlx" || got.Model != "qwen3-coder-30b" {
		t.Errorf("got %+v", got)
	}
	if DescribeDecoding(nil).Mechanism != backends.MechPromptOnly {
		t.Error("nil config must degrade to prompt-only")
	}
}

func TestDescribeDecodingReportsNegotiatedMechanism(t *testing.T) {
	cases := []struct {
		name string
		caps backends.Capabilities
		want string
	}{
		{"json_schema endpoint", backends.Capabilities{JSONSchema: true, JSONObject: true, NativeTools: true}, backends.MechJSONSchema},
		{"vllm", backends.Capabilities{GuidedJSON: true, JSONObject: true}, backends.MechGuidedJSON},
		{"llama.cpp", backends.Capabilities{GBNFGrammar: true, JSONObject: true}, backends.MechGrammar},
		{"json mode only", backends.Capabilities{JSONObject: true}, backends.MechJSONObject},
		{"nothing", backends.Capabilities{}, backends.MechPromptOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backends.ResetCapabilityCache()
			cfg := config.Default(t.TempDir())
			cfg.Provider = "omlx"
			cfg.Model = "m"
			cfg.Endpoint = "http://127.0.0.1:9000/v1"
			backends.SetCapabilities(cfg.Provider, cfg.Endpoint, cfg.Model, tc.caps)

			got := DescribeDecoding(cfg)
			if got.Mechanism != tc.want {
				t.Errorf("mechanism = %q, want %q", got.Mechanism, tc.want)
			}
			if !got.Probed {
				t.Error("seeded record should count as probed")
			}
			if !strings.Contains(got.Summary(), tc.want) {
				t.Errorf("summary %q does not name the mechanism", got.Summary())
			}
		})
	}
}

func TestProbeDecodingIsNeverFatal(t *testing.T) {
	backends.ResetCapabilityCache()
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Model = "m"
	cfg.Endpoint = "http://127.0.0.1:1/v1" // nothing listens here
	got := ProbeDecoding(context.Background(), cfg)
	if got.Mechanism != backends.MechPromptOnly {
		t.Errorf("unreachable endpoint = %q, want prompt_only", got.Mechanism)
	}
	if got.Source != "unreachable" {
		t.Errorf("source = %q", got.Source)
	}
}

func TestCatalogCarriesDecodingSupport(t *testing.T) {
	backends.ResetCapabilityCache()
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Model = "m"
	cfg.Endpoint = "http://127.0.0.1:9000/v1"
	backends.SetCapabilities(cfg.Provider, cfg.Endpoint, cfg.Model,
		backends.Capabilities{JSONSchema: true, JSONObject: true, NativeTools: true, Streaming: true})

	cat := Find(context.Background(), cfg, "", 4)
	if cat.Decoding.Mechanism != backends.MechJSONSchema {
		t.Errorf("catalog decoding = %+v", cat.Decoding)
	}
	if !cat.Decoding.NativeTools {
		t.Error("catalog lost the tools capability")
	}
}
