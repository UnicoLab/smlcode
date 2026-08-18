package stacks

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/config"
)

func TestParseAndApplyPreservesProjectFields(t *testing.T) {
	yaml := `
provider: deepseek
endpoint: https://api.deepseek.com
model: deepseek-chat
backend: slmcode
temperature: 0.15
max_tokens: 4096
max_parallel: 3
max_retries: 3
max_context_kb: 64
think_passes: 2
qa_gate: true
qa_gate_command: go test ./...
write_guard: true
shell_write_guard: true
file_checkpoints: true
require_smoke: true
claims_gate: true
over_edit_guard: true
finalize_warn: true
auto_text_tools: true
scope_judge: true
placeholder_pass: true
dynamic_pipeline: true
thinking_budget_tokens: 2048
read_head_lines: 60
shell_permission: ask
shell_whitelist: true
shell_allow: [go test, go build]
react_compact: true
react_compact_at_percent: 70
task_timeout_sec: 300
price_preset: deepseek
context_compact_engine: auto
llm_retry_count: 5
llm_retry_delay_ms: 250
enabled_models:
  - deepseek-chat
  - deepseek-reasoner
pinned_skills:
  - atomic-coding
session_event_log: true
auto_refine: true
model_profiles:
  default:
    max_tokens: 4096
    temperature: 0.15
    max_turns: 30
agents:
  reviewer:
    provider: openai
    model: gpt-4o-mini
`
	s, err := Parse("deepseek", "/tmp/deepseek.yaml", []byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if s.Provider != "deepseek" || s.Model != "deepseek-chat" {
		t.Fatalf("parse: %+v", s)
	}
	if s.Agents["reviewer"].Model != "gpt-4o-mini" {
		t.Fatalf("agents: %+v", s.Agents)
	}

	cfg := config.Default(t.TempDir())
	cfg.Listen = "127.0.0.1:9999"
	cfg.SkillsDirs = []string{"/custom/skills"}
	cfg.APIKey = "secret"
	cfg.Provider = "omlx"
	cfg.Model = "old"

	agentsDir := filepath.Join(cfg.Root, ".slmcode", "agents")
	res, err := Apply(cfg, s, agentsDir, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.StackID != "deepseek" || cfg.ActiveStack != "deepseek" {
		t.Fatalf("active stack: %s %+v", cfg.ActiveStack, res)
	}
	if cfg.Provider != "deepseek" || cfg.Model != "deepseek-chat" {
		t.Fatalf("llm: %s %s", cfg.Provider, cfg.Model)
	}
	if cfg.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen wiped: %s", cfg.Listen)
	}
	if len(cfg.SkillsDirs) != 1 || cfg.SkillsDirs[0] != "/custom/skills" {
		t.Fatalf("skills wiped: %v", cfg.SkillsDirs)
	}
	if cfg.APIKey != "secret" {
		t.Fatalf("api key wiped")
	}
	if cfg.Temperature != 0.15 || cfg.MaxTokens != 4096 {
		t.Fatalf("tuning: temp=%v tokens=%d", cfg.Temperature, cfg.MaxTokens)
	}
	if !cfg.WriteGuard || !cfg.QAGate {
		t.Fatalf("gates not applied")
	}
	if cfg.QAGateCommand != "go test ./..." || !cfg.ShellWriteGuard || !cfg.FileCheckpoints ||
		!cfg.RequireSmoke || !cfg.ClaimsGate || !cfg.OverEditGuard || !cfg.FinalizeWarn ||
		!cfg.AutoTextTools || !cfg.ScopeJudge || !cfg.PlaceholderPass || !cfg.DynamicPipeline {
		t.Fatalf("production guards not applied: %+v", cfg)
	}
	if cfg.ThinkingBudgetTokens != 2048 || cfg.ReadHeadLines != 60 || cfg.ShellPermission != "ask" ||
		!cfg.ShellWhitelist || len(cfg.ShellAllow) != 2 || !cfg.ReactCompact ||
		cfg.ReactCompactAtPercent != 70 || cfg.TaskTimeout != 300*time.Second {
		t.Fatalf("local tuning not applied: %+v", cfg)
	}
	if cfg.ContextCompactEngine != "auto" || cfg.LLMRetryCount != 5 || cfg.LLMRetryDelayMS != 250 {
		t.Fatalf("compact/retry: engine=%s retry=%d delay=%d",
			cfg.ContextCompactEngine, cfg.LLMRetryCount, cfg.LLMRetryDelayMS)
	}
	if len(cfg.EnabledModels) != 2 || cfg.EnabledModels[0] != "deepseek-chat" {
		t.Fatalf("enabled_models: %v", cfg.EnabledModels)
	}
	if len(cfg.PinnedSkills) != 1 || cfg.PinnedSkills[0] != "atomic-coding" {
		t.Fatalf("pinned_skills: %v", cfg.PinnedSkills)
	}
	if !cfg.AutoRefine || !cfg.SessionEventLog {
		t.Fatalf("refine/events flags")
	}
	if cfg.ModelProfiles["default"].MaxTokens != 4096 {
		t.Fatalf("profiles: %+v", cfg.ModelProfiles)
	}
}

func TestApplyAgentDefaultsAndClear(t *testing.T) {
	yaml := `
provider: ollama
endpoint: http://127.0.0.1:11434
model: qwen2.5-coder:7b
agents:
  worker:
    model: qwen2.5-coder:14b
  reviewer:
    provider: openai
    model: gpt-4o-mini
`
	s, err := Parse("ollama-local", "", []byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := config.Default(root)
	agentsDir := filepath.Join(root, ".slmcode", "agents")

	res, err := Apply(cfg, s, agentsDir, ApplyOptions{ApplyAgentDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.AgentsUpdated) != 2 {
		t.Fatalf("updated: %v", res.AgentsUpdated)
	}
	list, err := agents.LoadCustomSpecs(agentsDir)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]agents.CustomSpec{}
	for _, a := range list {
		byID[a.ID] = a
	}
	if byID["worker"].Model != "qwen2.5-coder:14b" {
		t.Fatalf("worker: %+v", byID["worker"])
	}
	if byID["reviewer"].Provider != "openai" || byID["reviewer"].Model != "gpt-4o-mini" {
		t.Fatalf("reviewer: %+v", byID["reviewer"])
	}

	// Second apply without force keeps existing pins.
	s2, _ := Parse("ollama-local", "", []byte(`
provider: ollama
endpoint: http://127.0.0.1:11434
model: qwen2.5-coder:7b
agents:
  worker:
    model: SHOULD-NOT-REPLACE
`))
	_, err = Apply(cfg, s2, agentsDir, ApplyOptions{ApplyAgentDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	list, _ = agents.LoadCustomSpecs(agentsDir)
	for _, a := range list {
		if a.ID == "worker" && a.Model != "qwen2.5-coder:14b" {
			t.Fatalf("force=false overwrote worker: %s", a.Model)
		}
	}

	res, err = Apply(cfg, s, agentsDir, ApplyOptions{ClearAgentLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.AgentsCleared) < 2 {
		t.Fatalf("cleared: %v", res.AgentsCleared)
	}
	list, _ = agents.LoadCustomSpecs(agentsDir)
	for _, a := range list {
		if a.Model != "" || a.Provider != "" {
			t.Fatalf("llm not cleared: %+v", a)
		}
	}
}

func TestMatchesActiveStack(t *testing.T) {
	s := &Stack{ID: "openai", Provider: "openai", Model: "gpt-4o"}
	cfg := &config.Config{Provider: "openai", Model: "gpt-4o-mini", ActiveStack: "openai"}
	if !s.Matches(cfg) {
		t.Fatal("active_stack should match even if model drifted")
	}
	cfg.ActiveStack = ""
	if s.Matches(cfg) {
		t.Fatal("model mismatch should not match without active_stack")
	}
}

func TestListFromRepo(t *testing.T) {
	dir := FindDir()
	if dir == "" {
		t.Skip("stacks dir not available")
	}
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 5 {
		t.Fatalf("expected shipped stacks, got %d from %s", len(list), dir)
	}
	// Ensure google YAML provider normalizes and loads.
	st, err := Load("google")
	if err != nil {
		t.Fatal(err)
	}
	if st.Provider != "gemini" {
		t.Fatalf("google stack provider=%s", st.Provider)
	}
}

func TestConflictingAgentsReported(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, "agents")
	_ = os.MkdirAll(agentsDir, 0o755)
	_, err := agents.WriteCustom(agentsDir, agents.CustomSpec{
		ID: "worker", Model: "other", Provider: "openai", Override: true, Builtin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse("x", "", []byte(`
provider: omlx
endpoint: http://127.0.0.1:8000/v1
model: Qwen
`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(root)
	res, err := Apply(cfg, s, agentsDir, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ConflictingAgents) != 1 || res.ConflictingAgents[0] != "worker" {
		t.Fatalf("conflict: %v", res.ConflictingAgents)
	}
}
