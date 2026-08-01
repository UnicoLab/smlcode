package config

import (
	"strings"
)

// ModelProfile tunes harness budgets per model (little-coder benchmark-profiles).
type ModelProfile struct {
	ContextLimit         int     `yaml:"context_limit" json:"context_limit"`
	MaxTokens            int     `yaml:"max_tokens" json:"max_tokens"`
	ThinkingBudgetTokens int     `yaml:"thinking_budget_tokens" json:"thinking_budget_tokens"`
	SkillTokenBudget     int     `yaml:"skill_token_budget" json:"skill_token_budget"`
	KnowledgeTokenBudget int     `yaml:"knowledge_token_budget" json:"knowledge_token_budget"`
	Temperature          float64 `yaml:"temperature" json:"temperature"`
	MaxTurns             int     `yaml:"max_turns" json:"max_turns"`
}

// DefaultModelProfiles returns sensible SLM-first defaults keyed by model id
// substrings (colon/hyphen normalized).
func DefaultModelProfiles() map[string]ModelProfile {
	return map[string]ModelProfile{
		"default": {
			SkillTokenBudget: 300, KnowledgeTokenBudget: 200,
			ThinkingBudgetTokens: 4096, MaxTokens: 3072, Temperature: 0.12,
		},
		"7b": {
			SkillTokenBudget: 220, KnowledgeTokenBudget: 160,
			ThinkingBudgetTokens: 2048, MaxTokens: 2048, Temperature: 0.1,
		},
		"1.5b": {
			SkillTokenBudget: 160, KnowledgeTokenBudget: 120,
			ThinkingBudgetTokens: 1024, MaxTokens: 1536, Temperature: 0.08,
		},
		"3b": {
			SkillTokenBudget: 180, KnowledgeTokenBudget: 140,
			ThinkingBudgetTokens: 1536, MaxTokens: 1792, Temperature: 0.1,
		},
		"14b": {
			SkillTokenBudget: 350, KnowledgeTokenBudget: 240,
			ThinkingBudgetTokens: 4096, MaxTokens: 4096, Temperature: 0.12,
		},
		"32b": {
			SkillTokenBudget: 400, KnowledgeTokenBudget: 280,
			ThinkingBudgetTokens: 6144, MaxTokens: 4096, Temperature: 0.15,
		},
		"qwen": {
			SkillTokenBudget: 280, KnowledgeTokenBudget: 200,
			ThinkingBudgetTokens: 3072, MaxTokens: 3072, Temperature: 0.12,
		},
	}
}

// NormProfileKey normalizes : vs - so profile keys match runtime model ids.
func NormProfileKey(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), ":", "-")
}

// ResolveModelProfile picks the best matching profile for provider/model.
// Exact → prefix → default. Optional env SLMCODE_BENCHMARK overlays nothing
// unless profiles define benchmark-specific keys (future).
func ResolveModelProfile(profiles map[string]ModelProfile, model string) ModelProfile {
	if profiles == nil {
		profiles = DefaultModelProfiles()
	}
	target := NormProfileKey(model)
	if p, ok := profiles[model]; ok {
		return mergeProfile(profiles["default"], p)
	}
	if p, ok := profiles[target]; ok {
		return mergeProfile(profiles["default"], p)
	}
	var bestKey string
	var best ModelProfile
	for k, p := range profiles {
		if k == "default" {
			continue
		}
		nk := NormProfileKey(k)
		if target == nk || strings.Contains(target, nk) || strings.HasPrefix(target, nk) {
			if len(nk) >= len(bestKey) {
				bestKey = nk
				best = p
			}
		}
	}
	base := profiles["default"]
	if bestKey == "" {
		// Heuristic size buckets from model name.
		switch {
		case strings.Contains(target, "1.5b") || strings.Contains(target, "1b"):
			return mergeProfile(base, profiles["1.5b"])
		case strings.Contains(target, "3b"):
			return mergeProfile(base, profiles["3b"])
		case strings.Contains(target, "7b") || strings.Contains(target, "8b"):
			return mergeProfile(base, profiles["7b"])
		case strings.Contains(target, "14b") || strings.Contains(target, "15b"):
			return mergeProfile(base, profiles["14b"])
		case strings.Contains(target, "32b") || strings.Contains(target, "30b") || strings.Contains(target, "35b"):
			return mergeProfile(base, profiles["32b"])
		default:
			return base
		}
	}
	return mergeProfile(base, best)
}

func mergeProfile(base, over ModelProfile) ModelProfile {
	out := base
	if over.ContextLimit > 0 {
		out.ContextLimit = over.ContextLimit
	}
	if over.MaxTokens > 0 {
		out.MaxTokens = over.MaxTokens
	}
	if over.ThinkingBudgetTokens > 0 {
		out.ThinkingBudgetTokens = over.ThinkingBudgetTokens
	}
	if over.SkillTokenBudget > 0 {
		out.SkillTokenBudget = over.SkillTokenBudget
	}
	if over.KnowledgeTokenBudget > 0 {
		out.KnowledgeTokenBudget = over.KnowledgeTokenBudget
	}
	if over.Temperature > 0 {
		out.Temperature = over.Temperature
	}
	if over.MaxTurns > 0 {
		out.MaxTurns = over.MaxTurns
	}
	return out
}
