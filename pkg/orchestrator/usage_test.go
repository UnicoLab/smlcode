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
