package config

import "testing"

func TestResolveModelProfileSizeBuckets(t *testing.T) {
	p := ResolveModelProfile(DefaultModelProfiles(), "qwen2.5-coder:7b")
	if p.ThinkingBudgetTokens != 2048 || p.MaxTokens != 2048 {
		t.Fatalf("expected 7b bucket to override qwen family defaults, got %+v", p)
	}
	p14 := ResolveModelProfile(DefaultModelProfiles(), "qwen2.5-coder:14b")
	if p14.MaxTokens < p.MaxTokens && p.MaxTokens > 0 {
		// 14b should allow at least as many tokens as 7b
		t.Fatalf("14b=%+v 7b=%+v", p14, p)
	}
}

func TestResolveModelProfileFamilyThenSize(t *testing.T) {
	profiles := map[string]ModelProfile{
		"default": {MaxTokens: 4096, SkillTokenBudget: 300, KnowledgeTokenBudget: 200, ThinkingBudgetTokens: 4096},
		"qwen":    {SkillTokenBudget: 280, KnowledgeTokenBudget: 190},
		"7b":      {MaxTokens: 1536, ThinkingBudgetTokens: 1024, SkillTokenBudget: 180},
	}
	p := ResolveModelProfile(profiles, "Qwen3-Coder-7B-Instruct")
	if p.MaxTokens != 1536 || p.ThinkingBudgetTokens != 1024 || p.SkillTokenBudget != 180 {
		t.Fatalf("size bucket should win budget fields: %+v", p)
	}
	if p.KnowledgeTokenBudget != 190 {
		t.Fatalf("family fields should survive when size bucket omits them: %+v", p)
	}
}

func TestNormProfileKey(t *testing.T) {
	if NormProfileKey("llamacpp/qwen3.6:35b") != NormProfileKey("llamacpp/qwen3.6-35b") {
		t.Fatal("colon/hyphen normalize")
	}
}

func TestResolveExactProfile(t *testing.T) {
	profiles := map[string]ModelProfile{
		"default": {SkillTokenBudget: 300, KnowledgeTokenBudget: 200, ThinkingBudgetTokens: 4096, MaxTokens: 3072},
		"my-slm":  {SkillTokenBudget: 100, ThinkingBudgetTokens: 512, MaxTokens: 1024},
	}
	p := ResolveModelProfile(profiles, "my-slm")
	if p.SkillTokenBudget != 100 || p.ThinkingBudgetTokens != 512 {
		t.Fatalf("%+v", p)
	}
}
