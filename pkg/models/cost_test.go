package models

import (
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

func TestFilterEnabled(t *testing.T) {
	names := []string{"gpt-4o-mini", "gpt-4o", "local-coder"}
	got := FilterEnabled(names, []string{"gpt-4o-mini", "openai/local-coder"})
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	all := FilterEnabled(names, nil)
	if len(all) != 3 {
		t.Fatalf("empty enabled should keep all: %v", all)
	}
}

func TestLookupCostCatalog(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PricePreset = "off"
	cfg.PricePromptPerMTok = 0
	cfg.PriceCompletionPerMTok = 0
	c := LookupCost(cfg, "gpt-4o-mini")
	if !c.Known || c.Source != "catalog" {
		t.Fatalf("expected catalog cost, got %+v", c)
	}
	usd, ok := EstimateUSD(c, 1_000_000, 1_000_000)
	if !ok || usd < 0.7 || usd > 0.8 {
		t.Fatalf("usd=%v ok=%v", usd, ok)
	}
}

func TestModelAllowed(t *testing.T) {
	if !ModelAllowed("x", nil) {
		t.Fatal("nil allow-list should allow")
	}
	if !ModelAllowed("gpt-4o", []string{"gpt-4o"}) {
		t.Fatal("exact allow")
	}
	if ModelAllowed("secret", []string{"gpt-4o"}) {
		t.Fatal("should deny")
	}
}
