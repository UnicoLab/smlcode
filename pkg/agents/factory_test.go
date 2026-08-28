package agents

import (
	"sort"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/schema"
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

// TestSetPreferFastIsPerRole covers the per-role fast-model override.
//
// EffectiveModel keyed the fast model off isLightAgent(spec.ID) alone, so the
// orchestrator's DecRoleModel bandit arm was a per-RUN choice over the whole
// light-agent set. A per-role override makes the same decision per role, and
// leaves every role without one on exactly the previous behavior.
func TestSetPreferFastIsPerRole(t *testing.T) {
	cases := []struct {
		name      string
		fastModel string
		overrides map[string]bool
		role      string
		specModel string
		want      string
	}{
		{"default: light agent takes the fast model", "fast-7b", nil, "reviewer", "", "fast-7b"},
		{"default: heavy agent takes the main model", "fast-7b", nil, "worker", "", "main-32b"},
		{"no fast model configured: everyone is on main", "", nil, "reviewer", "", "main-32b"},
		{"override pins a light agent to the main model", "fast-7b",
			map[string]bool{"reviewer": false}, "reviewer", "", "main-32b"},
		{"override puts a heavy agent on the fast model", "fast-7b",
			map[string]bool{"worker": true}, "worker", "", "fast-7b"},
		{"an override for another role does not leak", "fast-7b",
			map[string]bool{"reviewer": false}, "planner", "", "fast-7b"},
		{"override is case-insensitive", "fast-7b",
			map[string]bool{"REVIEWER": false}, "reviewer", "", "main-32b"},
		{"a per-agent model still wins over everything", "fast-7b",
			map[string]bool{"worker": true}, "worker", "pinned-70b", "pinned-70b"},
		{"fast=true with no fast model configured falls back to main", "",
			map[string]bool{"worker": true}, "worker", "", "main-32b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFactory(nil, nil, "main-32b", "ollama")
			f.FastModel = tc.fastModel
			for role, fast := range tc.overrides {
				f.SetPreferFast(role, fast)
			}
			got := f.EffectiveModel(RoleSpec{ID: tc.role, Model: tc.specModel})
			if got != tc.want {
				t.Fatalf("EffectiveModel(%s) = %q, want %q", tc.role, got, tc.want)
			}
		})
	}
}

func TestPreferFastAccessorsAreNilSafeAndClearable(t *testing.T) {
	var nilF *Factory
	nilF.SetPreferFast("worker", true) // must not panic
	nilF.ClearPreferFast("")
	if fast, ok := nilF.PreferFast("worker"); fast || ok {
		t.Fatal("a nil factory has no overrides")
	}

	f := NewFactory(nil, nil, "main-32b", "ollama")
	f.FastModel = "fast-7b"
	if _, ok := f.PreferFast("reviewer"); ok {
		t.Fatal("no override should be set yet")
	}
	f.SetPreferFast("", true) // empty role is ignored
	if _, ok := f.PreferFast(""); ok {
		t.Fatal("an empty role must not create an override")
	}
	f.SetPreferFast("reviewer", false)
	f.SetPreferFast("worker", true)
	if fast, ok := f.PreferFast("reviewer"); !ok || fast {
		t.Fatalf("reviewer override = (%v,%v)", fast, ok)
	}
	f.ClearPreferFast("reviewer")
	if _, ok := f.PreferFast("reviewer"); ok {
		t.Fatal("ClearPreferFast must drop the role")
	}
	if got := f.EffectiveModel(RoleSpec{ID: "reviewer"}); got != "fast-7b" {
		t.Fatalf("clearing must restore the default classification, got %q", got)
	}
	f.ClearPreferFast("")
	if _, ok := f.PreferFast("worker"); ok {
		t.Fatal("ClearPreferFast(\"\") must drop every override")
	}
}

// ── Who can be a team's project manager ──────────────────────────────────
//
// Existence is not enough. The decoding grammar for a request is derived from
// the agent's own system prompt, so an agent that does not answer the triage
// contract replies with something the reassignment step cannot read — and it
// only finds that out after a full model call. Offering a choice the harness
// would then refuse is worse than offering a short list.

func TestOnlyTriageAgentsCanManageATeam(t *testing.T) {
	f := NewFactory(nil, nil, "m", "p")
	if !f.EmitsSchema(RoleTriage, schema.RoleTriage) {
		t.Error("the built-in project manager must be eligible")
	}
	for _, id := range []string{plan.RoleWorker, plan.RoleReviewer, plan.RoleTester, "planner"} {
		if f.EmitsSchema(id, schema.RoleTriage) {
			t.Errorf("%q answers a different contract and must not be offered as a manager", id)
		}
	}
	// An agent that does not exist is not eligible either.
	if f.EmitsSchema("cobol-pm", schema.RoleTriage) {
		t.Error("an unregistered agent must never be eligible")
	}
	for _, id := range []string{"", "  "} {
		if f.EmitsSchema(id, schema.RoleTriage) || f.EmitsSchema(RoleTriage, id) {
			t.Errorf("EmitsSchema accepted a blank argument (%q)", id)
		}
	}
}

func TestAgentsEmittingListsTheEligibleManagers(t *testing.T) {
	got := AgentsEmitting(schema.RoleTriage, nil)
	if len(got) == 0 {
		t.Fatal("the built-in roster must offer at least one manager")
	}
	has := map[string]bool{}
	for _, id := range got {
		has[id] = true
	}
	if !has[RoleTriage] {
		t.Errorf("AgentsEmitting = %v, missing the built-in manager", got)
	}
	for _, unwanted := range []string{plan.RoleWorker, plan.RoleReviewer, plan.RoleTester} {
		if has[unwanted] {
			t.Errorf("AgentsEmitting offered %q, which answers a different contract", unwanted)
		}
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("AgentsEmitting = %v, want a stable sorted list", got)
	}
	if AgentsEmitting("", nil) != nil {
		t.Error("no contract, no candidates")
	}
}

// A user who writes their own manager in the Agents page gets it offered.
//
// The naming convention is what makes it eligible: NormalizeDecoding derives
// the contract from the id, so a `-triage` agent answers the triage schema
// while an agent called "backend-pm" answers nothing in particular.
func TestACustomManagerJoinsTheEligibleList(t *testing.T) {
	custom := []CustomSpec{{
		ID: "backend-triage", Title: "Backend PM", SystemPrompt: PromptTriage, MaxIter: 2,
	}}
	got := AgentsEmitting(schema.RoleTriage, custom)
	found := false
	for _, id := range got {
		if id == "backend-triage" {
			found = true
		}
	}
	if !found {
		t.Errorf("AgentsEmitting = %v, missing the user's own manager", got)
	}
	// A custom agent that answers something else stays out.
	other := []CustomSpec{{ID: "helper", Title: "Helper", SystemPrompt: PromptWorker, MaxIter: 2}}
	for _, id := range AgentsEmitting(schema.RoleTriage, other) {
		if id == "helper" {
			t.Error("a non-triage custom agent must not be offered as a manager")
		}
	}
}
