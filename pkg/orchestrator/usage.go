package orchestrator

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/config"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// TokenUsage is aggregated token accounting for a run.
type TokenUsage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Estimated        bool    `json:"estimated,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	CostConfigured   bool    `json:"cost_configured,omitempty"`
	CostNote         string  `json:"cost_note,omitempty"`
}

func (o *Orchestrator) recordUsage(u llm.Usage, estimated bool) {
	if u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.usage.TotalTokens == 0 && o.usage.PromptTokens == 0 {
		o.usage = TokenUsage{}
	}
	o.usage.PromptTokens += u.PromptTokens
	o.usage.CompletionTokens += u.CompletionTokens
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	o.usage.TotalTokens += total
	if estimated {
		o.usage.Estimated = true
	}
}

func (o *Orchestrator) recordResultUsage(r ggagent.SubAgentResult, input, output string) {
	u := r.Usage
	est := r.UsageEstimated
	if u.TotalTokens == 0 && u.PromptTokens == 0 && u.CompletionTokens == 0 {
		// Fallback estimate from role IO when GoLangGraph omitted usage.
		// Prefer tiktoken (cl100k_base) via llm.EstimateTokens; heuristic only if unavailable.
		u = llm.Usage{
			PromptTokens:     llm.EstimateTokens(input),
			CompletionTokens: llm.EstimateCompletionTokens(output, nil),
		}
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
		est = true
	}
	o.recordUsage(u, est)
}

func (o *Orchestrator) snapshotUsage() *TokenUsage {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.usage.TotalTokens == 0 && o.usage.PromptTokens == 0 && o.usage.CompletionTokens == 0 {
		return nil
	}
	out := o.usage
	if cost, ok := estimateCostUSD(o.cfg, out); ok {
		out.CostUSD = cost
		out.CostConfigured = true
	} else {
		out.CostNote = "tokens only (set price_preset or price_*_per_mtok to enable $)"
	}
	return &out
}

func estimateCostUSD(cfg *config.Config, u TokenUsage) (float64, bool) {
	if cfg == nil {
		return 0, false
	}
	pin, cout, ok := cfg.PriceRates()
	if !ok {
		return 0, false
	}
	cost := (float64(u.PromptTokens)/1_000_000.0)*pin + (float64(u.CompletionTokens)/1_000_000.0)*cout
	return cost, true
}

func formatUsage(u *TokenUsage) string {
	if u == nil {
		return ""
	}
	est := ""
	if u.Estimated {
		est = " (est)"
	}
	msg := fmt.Sprintf("tokens prompt=%d completion=%d total=%d%s",
		u.PromptTokens, u.CompletionTokens, u.TotalTokens, est)
	if u.CostConfigured {
		msg += fmt.Sprintf(" · ~$%.6f", u.CostUSD)
	} else if u.CostNote != "" {
		msg += " · " + u.CostNote
	}
	return msg
}

func usageHead(u *TokenUsage) string {
	s := formatUsage(u)
	return strings.TrimSpace(s)
}
