package agents

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// RoleSpec describes a specialist sub-agent.
type RoleSpec struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	SystemPrompt string   `json:"-"`
	Tools        []string `json:"tools,omitempty"`
	MaxIter      int      `json:"max_iter"`
	Temperature  float64  `json:"temperature"`
	MaxTokens    int      `json:"max_tokens"`
	Model        string   `json:"model,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Endpoint     string   `json:"endpoint,omitempty"`
	Skills       []string `json:"skills,omitempty"`
	Custom       bool     `json:"custom"`
	Override     bool     `json:"override,omitempty"`
}

// Specs returns the built-in specialist roster (Claude Code / Antigravity inspired).
func Specs() []RoleSpec {
	coding := workspace.ToolNames()
	return []RoleSpec{
		{ID: "coordinator", Title: "Coordinate board & specialists", Description: "Supervises the kanban board; does not implement code.", SystemPrompt: PromptCoordinator, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 512},
		{ID: "orchestrator", Title: "High-level orchestration", Description: "Coordinates specialists with short structured decisions.", SystemPrompt: PromptOrchestrator, Tools: nil, MaxIter: 4, Temperature: 0.2, MaxTokens: 512},
		{ID: plan.RoleContext, Title: "Maintain CONTEXT.md", Description: "Keeps a short living context for the active query.", SystemPrompt: PromptContext, Tools: nil, MaxIter: 2, Temperature: 0.3, MaxTokens: 1024},
		{ID: plan.RoleExplorer, Title: "Codebase explorer", Description: "Maps the smallest relevant file set with tools.", SystemPrompt: PromptExplorer, Tools: coding, MaxIter: 10, Temperature: 0.1, MaxTokens: 1536},
		{ID: "docs", Title: "Documentation explorer", Description: "Reads README/docs for conventions and APIs.", SystemPrompt: PromptDocsExplorer, Tools: coding, MaxIter: 8, Temperature: 0.1, MaxTokens: 1536},
		{ID: "architect", Title: "Minimal design / approach", Description: "Proposes a minimal approach without full implementations.", SystemPrompt: PromptArchitect, Tools: nil, MaxIter: 2, Temperature: 0.25, MaxTokens: 1024},
		{ID: plan.RolePlanner, Title: "High-level plan", Description: "Writes a concise structured plan for this query only.", SystemPrompt: PromptPlanner, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 1024},
		{ID: "splitter", Title: "Atomic task split", Description: "Splits the plan into tiny SLM-sized tasks.", SystemPrompt: PromptTaskSplitter, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 1536},
		{ID: plan.RoleWorker, Title: "Implement scoped change", Description: "Implements one atomic task with coding tools.", SystemPrompt: PromptWorker, Tools: coding, MaxIter: 14, Temperature: 0.15, MaxTokens: 3072},
		{ID: "deep", Title: "Deep multi-step worker", Description: "Handles deeper multi-step implementation within scope.", SystemPrompt: PromptDeepWorker, Tools: coding, MaxIter: 18, Temperature: 0.15, MaxTokens: 3072},
		{ID: plan.RoleReviewer, Title: "Self-critic / approve", Description: "Approves or rejects one task from disk evidence.", SystemPrompt: PromptReviewer, Tools: nil, MaxIter: 2, Temperature: 0.1, MaxTokens: 512},
		{ID: plan.RoleCorrector, Title: "Fix review issues", Description: "Patches reviewer issues inside HARD SCOPE.", SystemPrompt: PromptCorrector, Tools: coding, MaxIter: 10, Temperature: 0.15, MaxTokens: 3072},
		{ID: plan.RoleTester, Title: "Verify / run tests", Description: "Runs real shell checks (pytest/go test/smoke) before pass.", SystemPrompt: PromptTester, Tools: coding, MaxIter: 10, Temperature: 0.1, MaxTokens: 2048},
		{ID: "memory", Title: "Distill MEMORY.md", Description: "Distills durable project lessons into MEMORY.md.", SystemPrompt: PromptMemory, Tools: nil, MaxIter: 2, Temperature: 0.3, MaxTokens: 768},
	}
}

// PublicSpecs strips prompts for API/UI (built-ins only — callers merge customs).
func PublicSpecs() []map[string]interface{} {
	return PublicSpecsWithCustom(nil)
}

// PublicSpecsWithCustom merges built-ins + custom agents for Studio.
// Built-in overrides from YAML are merged into the corresponding roster entry.
func PublicSpecsWithCustom(custom []CustomSpec) []map[string]interface{} {
	byID := map[string]CustomSpec{}
	var extras []CustomSpec
	for _, c := range custom {
		if BuiltinIDs()[c.ID] {
			byID[c.ID] = c
		} else {
			extras = append(extras, c)
		}
	}
	var out []map[string]interface{}
	for _, s := range Specs() {
		m := map[string]interface{}{
			"id":          s.ID,
			"role":        s.Title,
			"title":       s.Title,
			"description": s.Description,
			"tools":       len(s.Tools) > 0,
			"max_iter":    s.MaxIter,
			"temperature": s.Temperature,
			"max_tokens":  s.MaxTokens,
			"skills":      s.Skills,
			"model":       s.Model,
			"provider":    s.Provider,
			"endpoint":    s.Endpoint,
			"custom":      false,
			"builtin":     true,
			"override":    false,
		}
		if o, ok := byID[s.ID]; ok {
			m["override"] = true
			m["path"] = o.Path
			if o.Description != "" {
				m["description"] = o.Description
			}
			if o.Title != "" {
				m["title"] = o.Title
				m["role"] = o.Title
			}
			if o.SystemPrompt != "" {
				m["system_prompt"] = o.SystemPrompt
			}
			if o.Model != "" {
				m["model"] = o.Model
			}
			if o.Provider != "" {
				m["provider"] = o.Provider
			}
			if o.Endpoint != "" {
				m["endpoint"] = o.Endpoint
			}
			if o.MaxIter > 0 {
				m["max_iter"] = o.MaxIter
			}
			if o.MaxTokens > 0 {
				m["max_tokens"] = o.MaxTokens
			}
			if o.Temperature > 0 {
				m["temperature"] = o.Temperature
			}
			if o.Tools != nil {
				m["tools"] = *o.Tools
			}
			if len(o.Skills) > 0 {
				m["skills"] = o.Skills
			}
		}
		out = append(out, m)
	}
	for _, c := range extras {
		out = append(out, map[string]interface{}{
			"id":            c.ID,
			"role":          c.Title,
			"title":         c.Title,
			"description":   c.Description,
			"tools":         c.ToolsEnabled(),
			"max_iter":      c.MaxIter,
			"temperature":   c.Temperature,
			"max_tokens":    c.MaxTokens,
			"skills":        c.Skills,
			"model":         c.Model,
			"provider":      c.Provider,
			"endpoint":      c.Endpoint,
			"system_prompt": c.SystemPrompt,
			"path":          c.Path,
			"custom":        true,
			"builtin":       false,
			"override":      false,
		})
	}
	return out
}

// AgentDetail returns the full Studio-editable view for one agent, including
// the built-in system prompt (list endpoints intentionally omit prompts).
func AgentDetail(id string, custom []CustomSpec) map[string]interface{} {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return nil
	}
	for _, a := range PublicSpecsWithCustom(custom) {
		aid, _ := a["id"].(string)
		if aid != id {
			continue
		}
		// Clone so callers can mutate without touching shared maps.
		out := make(map[string]interface{}, len(a)+2)
		for k, v := range a {
			out[k] = v
		}
		if sp, _ := out["system_prompt"].(string); strings.TrimSpace(sp) == "" {
			if spec := FindSpec(id); spec != nil && strings.TrimSpace(spec.SystemPrompt) != "" {
				out["system_prompt"] = spec.SystemPrompt
			}
		}
		if desc, _ := out["description"].(string); strings.TrimSpace(desc) == "" {
			if spec := FindSpec(id); spec != nil && strings.TrimSpace(spec.Description) != "" {
				out["description"] = spec.Description
			}
		}
		return out
	}
	return nil
}

// FindSpec returns a built-in RoleSpec by id, or nil.
func FindSpec(id string) *RoleSpec {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, s := range Specs() {
		if s.ID == id {
			cp := s
			return &cp
		}
	}
	return nil
}

// Factory builds GoLangGraph agents for the harness.
type Factory struct {
	LLM        *llm.ProviderManager
	Tools      *tools.ToolRegistry
	Model      string
	Provider   string
	CustomDirs []string
}

// NewFactory constructs a specialist factory.
func NewFactory(llmManager *llm.ProviderManager, toolReg *tools.ToolRegistry, model, provider string) *Factory {
	return &Factory{LLM: llmManager, Tools: toolReg, Model: model, Provider: provider}
}

// AllSpecs returns built-in + custom role specs (with builtin overrides merged).
func (f *Factory) AllSpecs() []RoleSpec {
	coding := workspace.ToolNames()
	out := append([]RoleSpec{}, Specs()...)
	custom, _ := LoadCustomSpecs(f.CustomDirs...)
	index := map[string]int{}
	for i, s := range out {
		index[s.ID] = i
	}
	for _, c := range custom {
		if i, ok := index[c.ID]; ok {
			ApplyOverride(&out[i], c, coding)
			out[i].Override = true
			continue
		}
		out = append(out, c.ToRoleSpec(coding))
		index[c.ID] = len(out) - 1
	}
	return out
}

// ProviderNeed is a per-agent LLM backend hint for ProviderManager registration.
type ProviderNeed struct {
	Provider string
	Model    string
	Endpoint string
}

// ProviderOverrides lists per-agent provider/model/endpoint settings that need
// registration in the LLM ProviderManager at rebuild time.
func (f *Factory) ProviderOverrides() []ProviderNeed {
	var out []ProviderNeed
	for _, s := range f.AllSpecs() {
		if s.Provider == "" && s.Endpoint == "" {
			continue
		}
		out = append(out, ProviderNeed{
			Provider: s.Provider,
			Model:    s.Model,
			Endpoint: s.Endpoint,
		})
	}
	return out
}

// BuildRegistry registers all specialist definitions for SubAgentExecutor.
func (f *Factory) BuildRegistry() (*agent.AgentRegistry, error) {
	reg := agent.NewAgentRegistry()
	for _, spec := range f.AllSpecs() {
		def := f.definition(spec)
		if err := reg.RegisterDefinition(spec.ID, def); err != nil {
			return nil, fmt.Errorf("register %s: %w", spec.ID, err)
		}
	}
	return reg, nil
}

// Create returns a live agent for a role id.
func (f *Factory) Create(roleID string) (agent.Agent, error) {
	for _, spec := range f.AllSpecs() {
		if spec.ID == roleID {
			return f.definition(spec).CreateAgent()
		}
	}
	return nil, fmt.Errorf("unknown role: %s", roleID)
}

func (f *Factory) definition(spec RoleSpec) *agent.BaseAgentDefinition {
	cfg := agent.DefaultAgentConfig()
	cfg.Name = spec.ID
	cfg.Type = agent.AgentTypeChat
	if len(spec.Tools) > 0 {
		cfg.Type = agent.AgentTypeReAct
	}
	cfg.Model = f.Model
	if spec.Model != "" {
		cfg.Model = spec.Model
	}
	// Friendly YAML/UI names stay on RoleSpec; AgentConfig.Provider is the unique
	// registry key when endpoint differs (openai@http://host:port/v1).
	cfg.Provider = backends.ResolveAgentProviderKey(f.Provider, spec.Provider, spec.Endpoint, "")
	cfg.SystemPrompt = spec.SystemPrompt
	cfg.Tools = spec.Tools
	cfg.Temperature = spec.Temperature
	cfg.MaxTokens = spec.MaxTokens
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 2048
	}
	cfg.MaxIterations = spec.MaxIter
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 8
	}
	// Token-stream early-exit: cancel remaining decode once a complete JSON /
	// tool-call is formed. Critical for slow local SLMs (oMLX / Ollama).
	cfg.EnableStreaming = true
	cfg.StreamingMode = llm.StreamModeForced
	cfg.EarlyExit = llm.DefaultEarlyExit

	def := agent.NewBaseAgentDefinition(cfg)
	def.Initialize(f.LLM, f.Tools)
	return def
}
