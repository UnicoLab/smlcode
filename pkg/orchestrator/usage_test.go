package orchestrator

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

func TestRecordResultUsageEstimatesWhenEmpty(t *testing.T) {
	o := &Orchestrator{cfg: config.Default(t.TempDir())}
	prompt := "please plan a careful change across several modules with enough detail"
	completion := `{"status":"done","summary":"ok","files_changed":["a.go"]}`
	o.recordResultUsage(ggagent.SubAgentResult{}, prompt, completion)
	u := o.snapshotUsage()
	if u == nil {
		t.Fatal("expected usage")
	}
	if u.PromptTokens <= 0 || u.CompletionTokens <= 0 {
		t.Fatalf("expected estimated tokens, got %+v", u)
	}
	if !u.Estimated {
		t.Fatal("expected Estimated=true")
	}
	if u.CostConfigured {
		t.Fatal("no rate table configured — must not invent $ cost")
	}
	if !strings.Contains(u.CostNote, "price_") {
		t.Fatalf("expected tokens-only note, got %q", u.CostNote)
	}
	// Tokenizer path should differ from naive chars/4 when tiktoken is available.
	if llm.TokenizerAvailable() {
		heurPrompt := (len(prompt) + 3) / 4
		if u.PromptTokens == heurPrompt {
			t.Fatalf("expected tiktoken prompt tokens != heuristic %d, got %d", heurPrompt, u.PromptTokens)
		}
	}
}

func TestRecordUsagePreservesProviderCounts(t *testing.T) {
	o := &Orchestrator{cfg: config.Default(t.TempDir())}
	o.recordResultUsage(ggagent.SubAgentResult{
		Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 40, TotalTokens: 140},
	}, "ignored", "ignored")
	u := o.snapshotUsage()
	if u.PromptTokens != 100 || u.CompletionTokens != 40 || u.TotalTokens != 140 {
		t.Fatalf("got %+v", u)
	}
	if u.Estimated {
		t.Fatal("provider usage should not be marked estimated")
	}
}

func TestEstimateCostWhenRatesConfigured(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PricePromptPerMTok = 1.0
	cfg.PriceCompletionPerMTok = 2.0
	o := &Orchestrator{cfg: cfg}
	o.recordUsage(llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000, TotalTokens: 1_500_000}, true)
	u := o.snapshotUsage()
	if !u.CostConfigured {
		t.Fatal("expected cost configured")
	}
	// 1.0 + 0.5*2.0 = 2.0
	if u.CostUSD < 1.99 || u.CostUSD > 2.01 {
		t.Fatalf("cost=%v", u.CostUSD)
	}
	head := usageHead(u)
	if head == "" || !strings.Contains(head, "tokens") || !strings.Contains(head, "total=") {
		t.Fatalf("head=%q", head)
	}
}

func TestEstimateCostPresetLocalVsOff(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.PricePreset = "off"
	o := &Orchestrator{cfg: cfg}
	o.recordUsage(llm.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}, true)
	u := o.snapshotUsage()
	if u.CostConfigured {
		t.Fatal("off preset must not invent $")
	}
	if !strings.Contains(formatUsage(u), "tokens only") {
		t.Fatalf("want tokens-only hint, got %q", formatUsage(u))
	}

	cfg.PricePreset = "local"
	u2 := o.snapshotUsage()
	if !u2.CostConfigured {
		t.Fatal("local preset is explicitly $0 — CostConfigured should be true")
	}
	if u2.CostUSD != 0 {
		t.Fatalf("local cost=%v", u2.CostUSD)
	}

	cfg.PricePreset = "openai"
	u3 := o.snapshotUsage()
	if !u3.CostConfigured || u3.CostUSD <= 0 {
		t.Fatalf("openai preset should yield ballpark $, got %+v", u3)
	}

	// Explicit rates win over preset.
	cfg.PricePreset = "openai"
	cfg.PricePromptPerMTok = 10
	cfg.PriceCompletionPerMTok = 20
	cost, ok := estimateCostUSD(cfg, TokenUsage{PromptTokens: 1_000_000, CompletionTokens: 0})
	if !ok || cost < 9.9 || cost > 10.1 {
		t.Fatalf("explicit win: ok=%v cost=%v", ok, cost)
	}
}

func TestPricePresetUnknownNoFake(t *testing.T) {
	pin, cout, ok := config.PricePresetRates("totally-unknown-cloud", "custom")
	if ok || pin != 0 || cout != 0 {
		t.Fatalf("unknown must not invent rates: %v %v %v", pin, cout, ok)
	}
}
