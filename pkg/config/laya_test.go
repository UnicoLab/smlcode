package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLayaConfigLayersAndSecrets(t *testing.T) {
	c := Default(t.TempDir())
	if c.LayaEndpoint != "" || c.LayaModel != "multilingual" || c.LayaTimeout != 2*time.Second {
		t.Fatalf("bad defaults: %q %q %v", c.LayaEndpoint, c.LayaModel, c.LayaTimeout)
	}
	var p Patch
	if err := json.Unmarshal([]byte(`{"laya_endpoint":"http://localhost:8091/","laya_model":"english","laya_timeout":"3s","laya_api_key":"laya-canary"}`), &p); err != nil {
		t.Fatal(err)
	}
	c.ApplyPatch(p)
	if c.LayaEndpoint != "http://localhost:8091" || c.LayaModel != "english" || c.LayaTimeout != 3*time.Second {
		t.Fatal("patch did not apply")
	}
	for _, key := range []any{"***", " ", " *** ", nil} {
		masked := Patch{}.WithValues(map[string]any{"laya_api_key": key})
		c.ApplyPatch(masked)
		if c.LayaAPIKey != "laya-canary" {
			t.Fatal("masked patch overwrote credential")
		}
	}
	t.Setenv("SLMCODE_LAYA_ENDPOINT", "http://localhost:8092")
	c.ApplyEnv()
	c.Normalize()
	if c.LayaEndpoint != "http://localhost:8092" {
		t.Fatal("env did not override patch")
	}
	if c.Public().LayaAPIKey != "***" {
		t.Fatal("unredacted public key")
	}
	t.Setenv("SLMCODE_PERSIST_API_KEY", "")
	raw, err := c.MarshalIntent()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "laya-canary") {
		t.Fatal("key persisted")
	}
	p = Patch{}
	if err := json.Unmarshal([]byte(`{"laya_endpoint":""}`), &p); err != nil {
		t.Fatal(err)
	}
	c.ApplyPatch(p)
	if c.LayaEndpoint != "" {
		t.Fatal("cannot disable sidecar")
	}
}

func TestOpenAIDecisionConfig(t *testing.T) {
	c := Default(t.TempDir())
	c.ApplyPatch(Patch{}.WithValues(map[string]any{"laya_provider": "openai", "laya_model": "Org/Model-Case", "laya_guidance": true}))
	if c.LayaProvider != "openai" || c.LayaModel != "Org/Model-Case" || !c.LayaGuidance {
		t.Fatal("provider settings lost")
	}
	t.Setenv("SLMCODE_LAYA_GUIDANCE", "false")
	t.Setenv("SLMCODE_LAYA_PROVIDER", "laya")
	c.ApplyEnv()
	c.Normalize()
	if c.LayaGuidance || c.LayaProvider != "laya" {
		t.Fatal("environment override lost")
	}
}
