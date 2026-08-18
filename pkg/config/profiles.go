package config

import (
	"regexp"
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
			ContextLimit: 16384, MaxTurns: 20,
			SkillTokenBudget: 260, KnowledgeTokenBudget: 180,
			ThinkingBudgetTokens: 3072, MaxTokens: 3072, Temperature: 0.12,
		},
		"7b": {
			ContextLimit: 8192, MaxTurns: 16,
			SkillTokenBudget: 220, KnowledgeTokenBudget: 160,
			ThinkingBudgetTokens: 2048, MaxTokens: 2048, Temperature: 0.1,
		},
		"1.5b": {
			ContextLimit: 4096, MaxTurns: 12,
			SkillTokenBudget: 160, KnowledgeTokenBudget: 120,
			ThinkingBudgetTokens: 1024, MaxTokens: 1536, Temperature: 0.08,
		},
		"3b": {
			ContextLimit: 4096, MaxTurns: 14,
			SkillTokenBudget: 180, KnowledgeTokenBudget: 140,
			ThinkingBudgetTokens: 1536, MaxTokens: 1792, Temperature: 0.1,
		},
		"14b": {
			ContextLimit: 16384, MaxTurns: 20,
			SkillTokenBudget: 320, KnowledgeTokenBudget: 220,
			ThinkingBudgetTokens: 3072, MaxTokens: 4096, Temperature: 0.12,
		},
		"32b": {
			ContextLimit: 32768, MaxTurns: 24,
			SkillTokenBudget: 380, KnowledgeTokenBudget: 260,
			ThinkingBudgetTokens: 4096, MaxTokens: 4096, Temperature: 0.15,
		},
		"qwen": {
			ContextLimit: 32768, MaxTurns: 24,
			SkillTokenBudget: 280, KnowledgeTokenBudget: 200,
			ThinkingBudgetTokens: 3072, MaxTokens: 3072, Temperature: 0.12,
		},
	}
}

// NormProfileKey normalizes : vs - so profile keys match runtime model ids.
func NormProfileKey(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), ":", "-")
}

var sizeProfileRe = regexp.MustCompile(`(?i)(^|[^0-9.])((?:1\.5|3|7|8|14|15|30|32|35)b)([^a-z0-9]|$)`)

// ResolveModelProfile picks the best matching profile for provider/model.
// Exact → family substring → size bucket → default. Size buckets are applied
// last so small local models keep tight budgets even when a family key such as
// "qwen" also matches.
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
	out := profiles["default"]
	var bestFamilyKey string
	var bestFamily ModelProfile
	for k, p := range profiles {
		nk := NormProfileKey(k)
		if nk == "default" || isSizeProfileKey(nk) {
			continue
		}
		if target == nk || strings.Contains(target, nk) || strings.HasPrefix(target, nk) {
			if len(nk) >= len(bestFamilyKey) {
				bestFamilyKey = nk
				bestFamily = p
			}
		}
	}
	if bestFamilyKey != "" {
		out = mergeProfile(out, bestFamily)
	}
	if sizeKey := modelSizeProfileKey(target); sizeKey != "" {
		if p, ok := profiles[sizeKey]; ok {
			out = mergeProfile(out, p)
		}
	}
	return out
}

func isSizeProfileKey(k string) bool {
	switch k {
	case "1b", "1.5b", "3b", "7b", "8b", "14b", "15b", "30b", "32b", "35b":
		return true
	default:
		return false
	}
}

func modelSizeProfileKey(target string) string {
	m := sizeProfileRe.FindStringSubmatch(target)
	if len(m) < 3 {
		return ""
	}
	switch strings.ToLower(m[2]) {
	case "1b", "1.5b":
		return "1.5b"
	case "3b":
		return "3b"
	case "7b", "8b":
		return "7b"
	case "14b", "15b":
		return "14b"
	case "30b", "32b", "35b":
		return "32b"
	default:
		return ""
	}
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
