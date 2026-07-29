package agents

import (
	"fmt"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// RoleSpec describes a specialist sub-agent.
type RoleSpec struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	SystemPrompt string `json:"-"`
	Tools        []string `json:"tools,omitempty"`
	MaxIter      int    `json:"max_iter"`
	Temperature  float64 `json:"temperature"`
	MaxTokens    int    `json:"max_tokens"`
}

// Specs returns the specialist roster (Claude Code / Antigravity inspired).
func Specs() []RoleSpec {
	coding := workspace.ToolNames()
	return []RoleSpec{
		{ID: "coordinator", Title: "Coordinate board & specialists", SystemPrompt: PromptCoordinator, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 1024},
		{ID: "orchestrator", Title: "High-level orchestration", SystemPrompt: PromptOrchestrator, Tools: nil, MaxIter: 4, Temperature: 0.2, MaxTokens: 1024},
		{ID: plan.RoleContext, Title: "Maintain CONTEXT.md", SystemPrompt: PromptContext, Tools: nil, MaxIter: 2, Temperature: 0.3, MaxTokens: 2048},
		{ID: plan.RoleExplorer, Title: "Codebase explorer", SystemPrompt: PromptExplorer, Tools: coding, MaxIter: 12, Temperature: 0.1, MaxTokens: 2048},
		{ID: "docs", Title: "Documentation explorer", SystemPrompt: PromptDocsExplorer, Tools: coding, MaxIter: 10, Temperature: 0.1, MaxTokens: 2048},
		{ID: "architect", Title: "Minimal design / approach", SystemPrompt: PromptArchitect, Tools: nil, MaxIter: 2, Temperature: 0.25, MaxTokens: 2048},
		{ID: plan.RolePlanner, Title: "High-level plan", SystemPrompt: PromptPlanner, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 2048},
		{ID: "splitter", Title: "Atomic task split", SystemPrompt: PromptTaskSplitter, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 3072},
		{ID: plan.RoleWorker, Title: "Implement scoped change", SystemPrompt: PromptWorker, Tools: coding, MaxIter: 16, Temperature: 0.15, MaxTokens: 4096},
		{ID: "deep", Title: "Deep multi-step worker", SystemPrompt: PromptDeepWorker, Tools: coding, MaxIter: 20, Temperature: 0.15, MaxTokens: 4096},
		{ID: plan.RoleReviewer, Title: "Self-critic / approve", SystemPrompt: PromptReviewer, Tools: nil, MaxIter: 2, Temperature: 0.1, MaxTokens: 1024},
		{ID: plan.RoleCorrector, Title: "Fix review issues", SystemPrompt: PromptCorrector, Tools: coding, MaxIter: 12, Temperature: 0.15, MaxTokens: 4096},
		{ID: plan.RoleTester, Title: "Verify / run tests", SystemPrompt: PromptTester, Tools: coding, MaxIter: 10, Temperature: 0.1, MaxTokens: 2048},
		{ID: "memory", Title: "Distill MEMORY.md", SystemPrompt: PromptMemory, Tools: nil, MaxIter: 2, Temperature: 0.3, MaxTokens: 1024},
	}
}

// PublicSpecs strips prompts for API/UI.
func PublicSpecs() []map[string]interface{} {
	var out []map[string]interface{}
	for _, s := range Specs() {
		out = append(out, map[string]interface{}{
			"id":     s.ID,
			"role":   s.Title,
			"title":  s.Title,
			"tools":  len(s.Tools) > 0,
			"max_iter": s.MaxIter,
		})
	}
	return out
}

// Factory builds GoLangGraph agents for the harness.
type Factory struct {
	LLM      *llm.ProviderManager
	Tools    *tools.ToolRegistry
	Model    string
	Provider string
}

// NewFactory constructs a specialist factory.
func NewFactory(llmManager *llm.ProviderManager, toolReg *tools.ToolRegistry, model, provider string) *Factory {
	return &Factory{LLM: llmManager, Tools: toolReg, Model: model, Provider: provider}
}

// BuildRegistry registers all specialist definitions for SubAgentExecutor.
func (f *Factory) BuildRegistry() (*agent.AgentRegistry, error) {
	reg := agent.NewAgentRegistry()
	for _, spec := range Specs() {
		def := f.definition(spec)
		if err := reg.RegisterDefinition(spec.ID, def); err != nil {
			return nil, fmt.Errorf("register %s: %w", spec.ID, err)
		}
	}
	return reg, nil
}

// Create returns a live agent for a role id.
func (f *Factory) Create(roleID string) (agent.Agent, error) {
	for _, spec := range Specs() {
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
	cfg.Provider = f.Provider
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

	def := agent.NewBaseAgentDefinition(cfg)
	def.Initialize(f.LLM, f.Tools)
	return def
}
