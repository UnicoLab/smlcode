package models

import (
	"context"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

func TestResolveAuthLocal(t *testing.T) {
	cfg := &config.Config{Provider: "ollama", Endpoint: "http://127.0.0.1:11434", Model: "qwen"}
	st := ResolveAuth(cfg)
	if !st.Configured || st.Required {
		t.Fatalf("local auth: %+v", st)
	}
}

func TestResolveAuthCloudMissing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("SLMCODE_API_KEY", "")
	cfg := &config.Config{Root: t.TempDir(), Provider: "openai", Endpoint: "https://api.openai.com/v1", Model: "gpt-4o"}
	st := ResolveAuth(cfg)
	if st.Configured || !st.Required {
		t.Fatalf("expected missing key: %+v", st)
	}
}

func TestFindFailsClosedWithoutAuth(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("SLMCODE_API_KEY", "")
	cfg := &config.Config{Root: t.TempDir(), Provider: "openai", Endpoint: "https://api.openai.com/v1", Model: "gpt-4o"}
	cat := Find(context.Background(), cfg, "gpt", 8)
	if cat.Error == "" {
		t.Fatal("expected auth error")
	}
	if len(cat.Matches) != 1 || cat.Matches[0].ID != "gpt-4o" {
		t.Fatalf("should only expose current: %+v", cat.Matches)
	}
}

func TestFindRespectsEnabledModels(t *testing.T) {
	cfg := &config.Config{
		Root: t.TempDir(), Provider: "ollama", Endpoint: "http://127.0.0.1:1",
		Model: "keep-me", EnabledModels: []string{"keep-me"},
	}
	cat := Find(context.Background(), cfg, "", 8)
	if len(cat.Models) != 1 || cat.Models[0] != "keep-me" {
		t.Fatalf("%+v", cat.Models)
	}
	cfg.EnabledModels = []string{"other"}
	cat = Find(context.Background(), cfg, "", 8)
	if len(cat.Models) != 0 {
		t.Fatalf("expected filtered out, got %v", cat.Models)
	}
}

func TestParseSelector(t *testing.T) {
	p, m := ParseSelector("openrouter/qwen/qwen-2.5")
	if p != "openrouter" || m != "qwen/qwen-2.5" {
		t.Fatalf("%s %s", p, m)
	}
	p, m = ParseSelector("gpt-4o")
	if p != "" || m != "gpt-4o" {
		t.Fatalf("%s %s", p, m)
	}
}

func TestFindFiltersQuery(t *testing.T) {
	// Force local so Fetch may fail; we still filter the fallback current model.
	cfg := &config.Config{
		Provider: "ollama",
		Endpoint: "http://127.0.0.1:1", // unreachable
		Model:    "qwen2.5-coder:7b",
	}
	cat := Find(context.Background(), cfg, "qwen", 8)
	if len(cat.Models) != 1 || cat.Models[0] != "qwen2.5-coder:7b" {
		t.Fatalf("filter: %+v err=%s", cat.Models, cat.Error)
	}
	cat = Find(context.Background(), cfg, "nomatch", 8)
	if len(cat.Models) != 0 {
		t.Fatalf("expected empty, got %v", cat.Models)
	}
}
