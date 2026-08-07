package models

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// ModelCost is estimated $/MTok for a model id (prompt / completion).
type ModelCost struct {
	Provider          string  `json:"provider"`
	Model             string  `json:"model"`
	PromptPerMTok     float64 `json:"prompt_per_mtok"`
	CompletionPerMTok float64 `json:"completion_per_mtok"`
	Source            string  `json:"source"` // catalog | preset | config | none
	Known             bool    `json:"known"`
}

// catalogRates are ballpark public rates (USD / 1M tokens). Explicit config wins.
var catalogRates = map[string][2]float64{
	"gpt-4o-mini":       {0.15, 0.60},
	"gpt-4o":            {2.50, 10.00},
	"gpt-4.1-mini":      {0.40, 1.60},
	"gpt-4.1":           {2.00, 8.00},
	"o4-mini":           {1.10, 4.40},
	"claude-3-5-haiku":  {0.80, 4.00},
	"claude-3-5-sonnet": {3.00, 15.00},
	"claude-sonnet-4":   {3.00, 15.00},
	"claude-opus-4":     {15.00, 75.00},
	"deepseek-chat":     {0.14, 0.28},
	"deepseek-reasoner": {0.55, 2.19},
	"gemini-2.0-flash":  {0.10, 0.40},
	"gemini-2.5-flash":  {0.15, 0.60},
	"gemini-2.5-pro":    {1.25, 10.00},
	"llama-3.3-70b":     {0.59, 0.79},
	"qwen2.5-coder":     {0.0, 0.0},
}

// LookupCost resolves $/MTok for a model. Priority: explicit config → catalog → preset.
func LookupCost(cfg *config.Config, model string) ModelCost {
	out := ModelCost{Model: strings.TrimSpace(model), Source: "none"}
	if cfg != nil {
		out.Provider = config.NormalizeProvider(cfg.Provider)
	}
	if cfg != nil && (cfg.PricePromptPerMTok > 0 || cfg.PriceCompletionPerMTok > 0) {
		out.PromptPerMTok = cfg.PricePromptPerMTok
		out.CompletionPerMTok = cfg.PriceCompletionPerMTok
		out.Source = "config"
		out.Known = true
		return out
	}
	key := strings.ToLower(out.Model)
	if rates, ok := catalogRates[key]; ok {
		out.PromptPerMTok = rates[0]
		out.CompletionPerMTok = rates[1]
		out.Source = "catalog"
		out.Known = true
		return out
	}
	// Longest substring / prefix match (avoid gpt-4o stealing gpt-4o-mini).
	bestID := ""
	var bestRates [2]float64
	for id, rates := range catalogRates {
		if key == id || strings.HasPrefix(key, id) || strings.Contains(key, id) {
			if len(id) > len(bestID) {
				bestID = id
				bestRates = rates
			}
		}
	}
	if bestID != "" {
		out.PromptPerMTok = bestRates[0]
		out.CompletionPerMTok = bestRates[1]
		out.Source = "catalog"
		out.Known = true
		return out
	}
	if cfg != nil {
		if pin, cout, ok := cfg.PriceRates(); ok {
			out.PromptPerMTok = pin
			out.CompletionPerMTok = cout
			out.Source = "preset"
			out.Known = true
			return out
		}
	}
	return out
}

// EstimateUSD returns estimated cost for token counts.
func EstimateUSD(cost ModelCost, promptTokens, completionTokens int) (float64, bool) {
	if !cost.Known {
		return 0, false
	}
	usd := (float64(promptTokens)/1_000_000.0)*cost.PromptPerMTok +
		(float64(completionTokens)/1_000_000.0)*cost.CompletionPerMTok
	return usd, true
}

// FilterEnabled keeps only models allowed by enabled_models (empty = all).
func FilterEnabled(names []string, enabled []string) []string {
	if len(enabled) == 0 {
		return names
	}
	allow := map[string]bool{}
	for _, e := range enabled {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		allow[strings.ToLower(e)] = true
		if _, model := ParseSelector(e); model != "" {
			allow[strings.ToLower(model)] = true
		}
	}
	var out []string
	for _, n := range names {
		ln := strings.ToLower(n)
		if allow[ln] {
			out = append(out, n)
			continue
		}
		// Exact selector "provider/id" already expanded; also allow suffix after "/".
		if i := strings.LastIndex(ln, "/"); i >= 0 && allow[ln[i+1:]] {
			out = append(out, n)
		}
	}
	return out
}

// ModelAllowed reports whether model is in the enabled set (empty = allow all).
func ModelAllowed(model string, enabled []string) bool {
	if len(enabled) == 0 {
		return true
	}
	return len(FilterEnabled([]string{model}, enabled)) > 0
}
