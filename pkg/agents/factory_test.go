package agents

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

func TestSpecsRoster(t *testing.T) {
	specs := Specs()
	if len(specs) < 12 {
		t.Fatalf("expected rich roster, got %d", len(specs))
	}
	want := map[string]bool{
		"coordinator": true, "worker": true, "reviewer": true,
		"corrector": true, "tester": true, "explorer": true, "deep": true,
	}
	for _, s := range specs {
		delete(want, s.ID)
		if strings.TrimSpace(s.SystemPrompt) == "" {
			t.Fatalf("empty prompt for %s", s.ID)
		}
		if s.MaxIter <= 0 {
			t.Fatalf("max_iter for %s", s.ID)
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing specs: %v", want)
	}
}

func TestCodingAgentsAllowFindModelsAndMCP(t *testing.T) {
	need := map[string]bool{"find_models": false, "mcp_call": false}
	for _, s := range Specs() {
		if s.ID != "worker" && s.ID != "explorer" && s.ID != "deep" {
			continue
		}
		have := map[string]bool{}
		for _, tool := range s.Tools {
			have[tool] = true
		}
		for name := range need {
			if !have[name] {
				t.Fatalf("%s missing tool %s in allowlist %v", s.ID, name, s.Tools)
			}
			need[name] = true
		}
	}
	for name, ok := range need {
		if !ok {
			t.Fatalf("tool %s never checked", name)
		}
	}
}

func TestPublicSpecs(t *testing.T) {
	pub := PublicSpecs()
	if len(pub) != len(Specs()) {
		t.Fatalf("public=%d specs=%d", len(pub), len(Specs()))
	}
	for _, m := range pub {
		if _, ok := m["id"]; !ok {
			t.Fatal("missing id")
		}
		if _, ok := m["system"]; ok {
			t.Fatal("public specs must not leak system prompts")
		}
		// List stays lean — full prompts are only on AgentDetail.
		if _, ok := m["system_prompt"]; ok {
			t.Fatal("public list must not include system_prompt")
		}
	}
}

func TestAgentDetailIncludesBuiltinPrompt(t *testing.T) {
	d := AgentDetail("worker", nil)
	if d == nil {
		t.Fatal("nil detail")
	}
	sp, _ := d["system_prompt"].(string)
	if !strings.Contains(sp, "HARD SCOPE") {
		t.Fatalf("expected built-in prompt, got %q", sp)
	}
	if d["description"] == "" {
		t.Fatal("expected description")
	}
	if AgentDetail("no-such-agent", nil) != nil {
		t.Fatal("expected nil for missing agent")
	}
}

func TestPublicSpecsMergesBuiltinOverride(t *testing.T) {
	dir := t.TempDir()
	_, err := WriteCustom(dir, CustomSpec{
		ID: "worker", Provider: "ollama", Model: "qwen2.5-coder:7b", MaxIter: 22,
	})
	if err != nil {
		t.Fatal(err)
	}
	custom, err := LoadCustomSpecs(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub := PublicSpecsWithCustom(custom)
	found := false
	for _, m := range pub {
		if m["id"] == "worker" {
			found = true
			if m["override"] != true {
				t.Fatal("expected override flag")
			}
			if m["provider"] != "ollama" || m["model"] != "qwen2.5-coder:7b" {
				t.Fatalf("%v", m)
			}
			if m["max_iter"] != 22 {
				t.Fatalf("max_iter=%v", m["max_iter"])
			}
		}
	}
	if !found {
		t.Fatal("worker missing")
	}
}

func TestWorkerPromptAntiWander(t *testing.T) {
	if !strings.Contains(PromptWorker, "HARD SCOPE") {
		t.Fatal("worker prompt missing HARD SCOPE")
	}
	if !strings.Contains(PromptWorker, "main.go") {
		t.Fatal("worker prompt should mention main.go ban")
	}
	if !strings.Contains(PromptWorker, "ANTI-WANDER") {
		t.Fatal("worker prompt missing ANTI-WANDER")
	}
	if !strings.Contains(PromptReviewer, "unwanted main.go") && !strings.Contains(PromptReviewer, "outside focus") {
		t.Fatal("reviewer should reject out-of-focus paths")
	}
	if !strings.Contains(PromptReviewer, "Disk evidence") {
		t.Fatal("reviewer should trust Disk evidence")
	}
	if !strings.Contains(PromptClarifier, "recommended") {
		t.Fatal("clarifier should use recommended options")
	}
	if !strings.Contains(PromptScopeJudge, "weak_task_ids") {
		t.Fatal("scope judge prompt missing")
	}
}

func TestDefinitionUsesUniqueProviderKeyForEndpoint(t *testing.T) {
	f := &Factory{Provider: "omlx", Model: "m"}
	spec := RoleSpec{
		ID: "a1", Title: "A", SystemPrompt: "x", Provider: "openai",
		Endpoint: "http://127.0.0.1:9000/v1", MaxTokens: 128, MaxIter: 2,
	}
	def := f.definition(spec)
	cfg := def.GetConfig()
	if cfg == nil {
		t.Fatal("nil config")
	}
	want := "openai@http://127.0.0.1:9000/v1"
	if cfg.Provider != want {
		t.Fatalf("provider=%q want %q", cfg.Provider, want)
	}
	spec2 := RoleSpec{ID: "a2", SystemPrompt: "x", Provider: "openai", MaxTokens: 128, MaxIter: 2}
	def2 := f.definition(spec2)
	if def2.GetConfig().Provider != "openai" {
		t.Fatalf("friendly name lost: %q", def2.GetConfig().Provider)
	}
}

func TestDefinitionResolvesProfilePerAgentModel(t *testing.T) {
	f := &Factory{
		Provider: "openai",
		Model:    "gpt-4o",
		ModelProfiles: map[string]config.ModelProfile{
			"default":          {MaxTokens: 8192, MaxTurns: 40, Temperature: 0.2},
			"qwen2.5-coder:7b": {MaxTokens: 1024, MaxTurns: 12, Temperature: 0.1},
			"gpt-4o":           {MaxTokens: 4096, MaxTurns: 32, Temperature: 0.15},
		},
		ProfileMaxTokens: 8192,
		ProfileMaxTurns:  40,
		ProfileTemp:      0.2,
	}
	worker := RoleSpec{
		ID: "worker", SystemPrompt: "x", Model: "qwen2.5-coder:7b",
		Provider: "ollama", MaxTokens: 3072, MaxIter: 16, Temperature: 0.12,
	}
	cfg := f.definition(worker).GetConfig()
	if cfg.Model != "qwen2.5-coder:7b" {
		t.Fatalf("model=%s", cfg.Model)
	}
	if cfg.MaxTokens != 1024 {
		t.Fatalf("max_tokens=%d want 1024 from per-agent profile", cfg.MaxTokens)
	}
	if cfg.MaxIterations != 12 {
		t.Fatalf("max_iter=%d want 12", cfg.MaxIterations)
	}

	// Inherit global model → gpt-4o profile, not the factory fallback 8192.
	inherit := RoleSpec{ID: "worker", SystemPrompt: "x", MaxTokens: 8000, MaxIter: 50}
	cfg2 := f.definition(inherit).GetConfig()
	if cfg2.Model != "gpt-4o" {
		t.Fatalf("inherit model=%s", cfg2.Model)
	}
	if cfg2.MaxTokens != 4096 {
		t.Fatalf("inherit max_tokens=%d want 4096", cfg2.MaxTokens)
	}
}
