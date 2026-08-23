package agents

import (
	"fmt"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/schema"
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

	// JSONOnly marks a role whose entire output is one JSON document. Factory
	// attaches constrained decoding (response_format / guided_json / GBNF) for
	// these, negotiated per endpoint — see pkg/backends.
	JSONOnly bool `json:"json_only,omitempty"`
	// SchemaRole names the pkg/schema contract this role normally emits. It is
	// a default: the contract is re-detected per request from the prompt, so a
	// role re-tasked with another contract (planner running the clarify
	// interview) is still constrained correctly.
	SchemaRole string `json:"schema_role,omitempty"`
	// SerialTools caps the assistant message at one tool call per turn.
	// GoLangGraph's ReAct loop executes EVERY ToolCall in an assistant message
	// with nothing capping it, so three malformed ws_edit calls all run.
	SerialTools bool `json:"serial_tools,omitempty"`
	// StopSequences end generation before the trailing prose tail that
	// pkg/repair currently has to strip is ever produced.
	StopSequences []string `json:"stop_sequences,omitempty"`
}

// JSONTailStop ends a JSON-only completion the moment the model starts a
// markdown section after its object.
var JSONTailStop = []string{"\n## ", "\n```\n\n", "\nNote:"}

// Directives renders the decoding contract this role needs at the provider.
func (s RoleSpec) Directives() backends.Directives {
	toolChoice := ""
	if len(s.Tools) > 0 {
		toolChoice = "auto"
	}
	return backends.Directives{
		Role:          s.ID,
		SchemaRole:    s.SchemaRole,
		JSONOnly:      s.JSONOnly,
		SerialTools:   s.SerialTools,
		StopSequences: s.StopSequences,
		ToolChoice:    toolChoice,
	}
}

// NormalizeDecoding fills JSONOnly / SchemaRole / SerialTools / StopSequences
// for a spec that did not set them — built-in, custom YAML, or block-defined.
// A tool-less role whose id maps to a known schema contract becomes JSON-only;
// a role with tools gets one-call-per-turn.
func NormalizeDecoding(s *RoleSpec) {
	if s == nil || strings.TrimSpace(s.ID) == "" {
		return
	}
	id := strings.ToLower(strings.TrimSpace(s.ID))
	if s.SchemaRole == "" {
		if spec, ok := schema.For(id); ok {
			s.SchemaRole = spec.Name
		} else if spec, ok := schema.For(genericRole(id)); ok {
			s.SchemaRole = spec.Name
		}
	}
	if len(s.Tools) > 0 {
		s.SerialTools = true
		s.JSONOnly = false
		return
	}
	// Free-text roles: their output is markdown, never a JSON document.
	switch genericRole(id) {
	case plan.RoleContext, "memory", "describer":
		s.JSONOnly = false
		s.SchemaRole = ""
		return
	}
	if s.SchemaRole != "" {
		s.JSONOnly = true
		if len(s.StopSequences) == 0 {
			s.StopSequences = append([]string(nil), JSONTailStop...)
		}
	}
}

// genericRole maps a language-specialised id (go-worker, python-tester) back to
// its generic role so schema/decoding defaults apply to block-defined agents.
func genericRole(id string) string {
	for _, suffix := range []string{
		"worker", "tester", "reviewer", "corrector", "explorer",
		"planner", "splitter", "architect", "editor", "describer",
	} {
		if id == suffix || strings.HasSuffix(id, "-"+suffix) {
			return suffix
		}
	}
	return id
}

// Specs returns the built-in specialist roster (Claude Code / Antigravity inspired).
//
// Every entry's decoding contract (JSONOnly / SchemaRole / SerialTools /
// StopSequences) is filled by NormalizeDecoding, so a new role only has to
// declare its tools and — when its id does not match a pkg/schema contract —
// its SchemaRole.
func Specs() []RoleSpec {
	coding := append(workspace.ToolNames(), workspace.SpecialistToolNames()...)
	out := specs(coding)
	for i := range out {
		NormalizeDecoding(&out[i])
	}
	return out
}

func specs(coding []string) []RoleSpec {
	return []RoleSpec{
		{ID: "coordinator", Title: "Coordinate board & specialists", Description: "Supervises the kanban board; does not implement code.", SystemPrompt: PromptCoordinator, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 512},
		{ID: "orchestrator", Title: "High-level orchestration", Description: "Coordinates specialists with short structured decisions.", SystemPrompt: PromptOrchestrator, Tools: nil, MaxIter: 4, Temperature: 0.2, MaxTokens: 512},
		{ID: plan.RoleContext, Title: "Maintain CONTEXT.md", Description: "Keeps a short living context for the active query.", SystemPrompt: PromptContext, Tools: nil, MaxIter: 2, Temperature: 0.3, MaxTokens: 1024},
		{ID: plan.RoleExplorer, Title: "Codebase explorer", Description: "Maps the smallest relevant file set with tools.", SystemPrompt: PromptExplorer, Tools: coding, MaxIter: 10, Temperature: 0.1, MaxTokens: 1536},
		{ID: "docs", Title: "Documentation explorer", Description: "Reads README/docs for conventions and APIs.", SystemPrompt: PromptDocsExplorer, Tools: coding, MaxIter: 8, Temperature: 0.1, MaxTokens: 1536},
		{ID: "architect", Title: "Minimal design / approach", Description: "Proposes a minimal approach without full implementations.", SystemPrompt: PromptArchitect, Tools: nil, MaxIter: 2, Temperature: 0.25, MaxTokens: 1024},
		{ID: plan.RolePlanner, Title: "High-level plan", Description: "Writes a concise structured plan for this query only.", SystemPrompt: PromptPlanner, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 1024},
		{ID: "splitter", Title: "Atomic task split", Description: "Splits the plan into tiny SLM-sized tasks.", SystemPrompt: PromptTaskSplitter, Tools: nil, MaxIter: 2, Temperature: 0.2, MaxTokens: 1536},
		{ID: plan.RoleWorker, Title: "Implement scoped change", Description: "Implements one atomic task with coding tools.", SystemPrompt: PromptWorker, Tools: coding, MaxIter: 16, Temperature: 0.12, MaxTokens: 3072},
		{ID: "deep", Title: "Deep multi-step worker", Description: "Handles deeper multi-step implementation within scope.", SystemPrompt: PromptDeepWorker, Tools: coding, MaxIter: 20, Temperature: 0.12, MaxTokens: 3072},
		{ID: plan.RoleReviewer, Title: "Self-critic / approve", Description: "Approves or rejects one task from disk evidence.", SystemPrompt: PromptReviewer, Tools: nil, MaxIter: 2, Temperature: 0.05, MaxTokens: 768},
		{ID: plan.RoleCorrector, Title: "Fix review issues", Description: "Patches reviewer issues inside HARD SCOPE.", SystemPrompt: PromptCorrector, Tools: coding, MaxIter: 12, Temperature: 0.12, MaxTokens: 3072},
		{ID: plan.RoleTester, Title: "Verify / run tests", Description: "Runs real shell checks in the project's language before pass.", SystemPrompt: PromptTester, Tools: coding, MaxIter: 12, Temperature: 0.08, MaxTokens: 2048},
		{ID: plan.RolePlaceholder, Title: "Fill placeholders / flag gaps", Description: "Detects stub code, fills real implementations, or flags precise gaps for HITL.", SystemPrompt: PromptPlaceholder, Tools: coding, MaxIter: 14, Temperature: 0.1, MaxTokens: 3072},
		{ID: plan.RoleEscalate, Title: "Escalate arbitrator", Description: "Decides retry/re-scope/abort/mark_done when human escalate HITL times out.", SystemPrompt: PromptEscalate, Tools: nil, MaxIter: 1, Temperature: 0.1, MaxTokens: 384},
		{ID: "memory", Title: "Distill MEMORY.md", Description: "Distills durable project lessons into MEMORY.md.", SystemPrompt: PromptMemory, Tools: nil, MaxIter: 2, Temperature: 0.3, MaxTokens: 768},
		{ID: "composer", Title: "Dynamic pipeline composer", Description: "Assembles the right team, tools, and skills into a task-specific pipeline.", SystemPrompt: PromptComposer, Tools: nil, MaxIter: 3, Temperature: 0.2, MaxTokens: 2048, SchemaRole: schema.RoleComposition},

		// reviewer-strict is the second reviewer the speculative review race in
		// pkg/loop has always asked for. Until it was registered here,
		// SubAgentExecutor answered "subagent 'reviewer-strict' not found" and
		// the documented second opinion never ran.
		{ID: RoleReviewerStrict, Title: "Strict second reviewer", Description: "Second opinion on a task: approves only on complete, demonstrated evidence.", SystemPrompt: PromptReviewerStrict, Tools: nil, MaxIter: 2, Temperature: 0.0, MaxTokens: 768, SchemaRole: schema.RoleReview},

		// Architect/editor pair (Aider's measured decomposition win). The
		// describer reasons with no format constraints and no tools; the editor
		// only formats, with constrained decoding and tools. Their models are
		// independently selectable, so a 32B can reason and a 7B can format.
		{ID: RoleDescriber, Title: "Change describer (architect half)", Description: "Describes the change in prose for the editor to apply. No tools, no format constraints.", SystemPrompt: PromptDescriber, Tools: nil, MaxIter: 2, Temperature: 0.3, MaxTokens: 1536},
		{ID: RoleEditor, Title: "Edit applier (editor half)", Description: "Applies a described change with the edit tools. Minimal reasoning, strict format.", SystemPrompt: PromptEditor, Tools: coding, MaxIter: 12, Temperature: 0.05, MaxTokens: 3072, SchemaRole: schema.RoleWorker},
	}
}

// Built-in role ids added alongside the original 17-specialist roster.
const (
	// RoleReviewerStrict is the second reviewer used by the speculative review
	// race in pkg/loop when max_parallel >= 3.
	RoleReviewerStrict = "reviewer-strict"
	// RoleDescriber is the prose half of the architect/editor pair.
	RoleDescriber = "describer"
	// RoleEditor is the formatting half of the architect/editor pair.
	RoleEditor = "editor"
)

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

// EnrichPublicSpecs attaches effective_* inheritance fields using global LLM defaults.
func EnrichPublicSpecs(list []map[string]interface{}, globalProvider, globalModel, activeStack string) []map[string]interface{} {
	gp := strings.TrimSpace(globalProvider)
	gm := strings.TrimSpace(globalModel)
	for _, m := range list {
		model, _ := m["model"].(string)
		provider, _ := m["provider"].(string)
		effModel := strings.TrimSpace(model)
		if effModel == "" {
			effModel = gm
		}
		effProvider := strings.TrimSpace(provider)
		if effProvider == "" {
			effProvider = gp
		}
		m["effective_model"] = effModel
		m["effective_provider"] = effProvider
		m["inherits_model"] = strings.TrimSpace(model) == ""
		m["inherits_provider"] = strings.TrimSpace(provider) == ""
		m["active_stack"] = activeStack
	}
	return list
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
	// ExtraCustoms are agent definitions merged after CustomDirs (on-disk files
	// win on id clashes). Used to register block-defined specialists such as
	// go-tester / go-worker straight from the blocks registry.
	ExtraCustoms []CustomSpec
	FastModel    string // optional faster model for lightweight agents
	// preferFast holds PER-ROLE overrides of the fast-model decision, set by
	// SetPreferFast. Without one, EffectiveModel falls back to the built-in
	// isLightAgent classification — which is a decision about a whole CLASS of
	// agents and therefore forced the orchestrator's DecRoleModel bandit arm to
	// be a per-RUN choice over the entire light set. With one, the arm can be
	// pulled per role.
	preferFast map[string]bool
	// mu guards preferFast only. Everything else on Factory is written once at
	// construction; the per-role overrides are written between waves.
	mu sync.RWMutex
	// ModelProfiles resolves caps against each agent's effective model
	// (per-agent override ?? global stack/config model).
	ModelProfiles map[string]config.ModelProfile
	// Optional global fallback caps when ModelProfiles is empty.
	ProfileMaxTokens int
	ProfileMaxTurns  int
	ProfileTemp      float64
}

// NewFactory constructs a specialist factory.
func NewFactory(llmManager *llm.ProviderManager, toolReg *tools.ToolRegistry, model, provider string) *Factory {
	return &Factory{LLM: llmManager, Tools: toolReg, Model: model, Provider: provider}
}

// EffectiveModel returns the model an agent will use:
// per-agent override → per-ROLE fast preference → fast model for light agents
// → global.
func (f *Factory) EffectiveModel(spec RoleSpec) string {
	if strings.TrimSpace(spec.Model) != "" {
		return strings.TrimSpace(spec.Model)
	}
	if f == nil {
		return ""
	}
	if fast, ok := f.PreferFast(spec.ID); ok {
		// An explicit per-role decision wins over the class heuristic in BOTH
		// directions: fast=false pins a light agent to the main model, and
		// fast=true puts a heavy one on the fast model.
		if fast && f.FastModel != "" {
			return f.FastModel
		}
		return f.Model
	}
	// Default: the fast model for lightweight agents that don't need deep reasoning.
	if f.FastModel != "" && isLightAgent(spec.ID) {
		return f.FastModel
	}
	return f.Model
}

// SetFastModel sets the model for lightweight agents.
func (f *Factory) SetFastModel(m string) { f.FastModel = m }

// SetPreferFast records a PER-ROLE decision about the fast model, overriding
// the built-in light/heavy classification for that role alone.
//
// The orchestrator's DecRoleModel bandit used to be able to express only
// "every light agent on the fast model this run" or "none of them", because
// the only lever was Factory.FastModel — a single shared field whose meaning
// was decided by isLightAgent(spec.ID). Per-role overrides make the arm a real
// per-role choice while leaving every unset role on the previous behavior.
//
// Roles are matched case-insensitively. Agents resolve their model at
// construction time, so set this BEFORE building the agents for a wave.
func (f *Factory) SetPreferFast(role string, fast bool) {
	if f == nil {
		return
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.preferFast == nil {
		f.preferFast = map[string]bool{}
	}
	f.preferFast[role] = fast
}

// ClearPreferFast drops one role's override (empty role drops all of them),
// restoring the default light/heavy classification.
func (f *Factory) ClearPreferFast(role string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		f.preferFast = nil
		return
	}
	delete(f.preferFast, role)
}

// PreferFast reports a role's override and whether one is set.
func (f *Factory) PreferFast(role string) (fast, ok bool) {
	if f == nil {
		return false, false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if len(f.preferFast) == 0 {
		return false, false
	}
	fast, ok = f.preferFast[strings.ToLower(strings.TrimSpace(role))]
	return fast, ok
}

var lightAgents = map[string]bool{
	"reviewer": true, "coordinator": true, "splitter": true, "planner": true,
	"context": true, "architect": true, "clarifier": true, "interviewer": true,
	"orchestrator": true, "memory": true, "docs": true, "escalate": true,
	"composer": true,
}

func isLightAgent(id string) bool { return lightAgents[strings.ToLower(id)] }

// EffectiveProvider returns the friendly provider name (not registry key).
func (f *Factory) EffectiveProvider(spec RoleSpec) string {
	if strings.TrimSpace(spec.Provider) != "" {
		return config.NormalizeProvider(spec.Provider)
	}
	if f == nil {
		return ""
	}
	return config.NormalizeProvider(f.Provider)
}

// AllSpecs returns built-in + custom role specs (with builtin overrides merged).
func (f *Factory) AllSpecs() []RoleSpec {
	coding := append(workspace.ToolNames(), workspace.SpecialistToolNames()...)
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
	// Block-defined agents (e.g. go-tester) — on-disk custom files win on clash.
	for _, c := range f.ExtraCustoms {
		if _, ok := index[c.ID]; ok {
			continue
		}
		out = append(out, c.ToRoleSpec(coding))
		index[c.ID] = len(out) - 1
	}
	// Custom YAML and block-defined agents get the same decoding contract as
	// built-ins: a tool-less go-reviewer is JSON-only, a go-worker is serial.
	for i := range out {
		NormalizeDecoding(&out[i])
	}
	return out
}

// IsKnownRole reports whether id names a built-in specialist. Wire-up code that
// names a slot role (pkg/loop's speculative review race, pipeline phase
// bindings) should assert with this so a typo fails loudly at configuration
// time instead of silently at runtime, the way "reviewer-strict" did for as
// long as it went unregistered.
//
// It covers built-ins only; use Factory.HasRole for a roster that includes
// custom and block-defined agents.
func IsKnownRole(id string) bool {
	return BuiltinIDs()[strings.ToLower(strings.TrimSpace(id))]
}

// HasRole reports whether this factory can create id (built-in, custom YAML, or
// block-defined).
func (f *Factory) HasRole(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return false
	}
	if f == nil {
		return IsKnownRole(id)
	}
	for _, s := range f.AllSpecs() {
		if s.ID == id {
			return true
		}
	}
	return false
}

// ArchitectEditorPair returns the role ids of the describer/editor decomposition.
//
// The pair exists because one model that must simultaneously solve the problem
// and conform to an edit format divides its attention between the two; Aider
// measured every model tested scoring substantially higher paired than solo.
// Each half's model is selectable independently through the usual per-agent
// `model:` override, so a 32B can reason while a 7B formats.
func ArchitectEditorPair() (describer, editor string) {
	return RoleDescriber, RoleEditor
}

// EditorInput builds the editor's input from the describer's prose. The editor
// prompt tells it to apply the description and nothing else, so the task text
// is included only as context.
func EditorInput(task, description string) string {
	var b strings.Builder
	b.WriteString("## Task\n")
	b.WriteString(strings.TrimSpace(task))
	b.WriteString("\n\n## Change to apply (from the architect)\n")
	b.WriteString(strings.TrimSpace(description))
	b.WriteString("\n\nApply exactly this change. Do not redesign it.")
	return b.String()
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
	cfg.Model = f.EffectiveModel(spec)
	// Friendly YAML/UI names stay on RoleSpec; AgentConfig.Provider is the unique
	// registry key when endpoint differs (openai@http://host:port/v1).
	cfg.Provider = backends.ResolveAgentProviderKey(f.Provider, spec.Provider, spec.Endpoint, "")
	// Attach this role's decoding contract by binding a role-scoped provider.
	// GoLangGraph builds llm.CompletionRequest itself and never sets
	// response_format, stop, or tool_choice — but it does resolve the provider
	// by the name set here, which is the one hook the read-only dependency
	// leaves open. Everything downstream (orchestrator, loop) gets it for free.
	cfg.Provider = backends.BindRole(f.LLM, cfg.Provider, spec.Directives())
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
	// Resolve model_profiles against this agent's effective model so a worker on
	// qwen:7b is not capped by a frontier global profile (and vice versa).
	profMaxTok, profMaxTurns, profTemp := f.ProfileMaxTokens, f.ProfileMaxTurns, f.ProfileTemp
	if len(f.ModelProfiles) > 0 {
		prof := config.ResolveModelProfile(f.ModelProfiles, cfg.Model)
		if prof.MaxTokens > 0 {
			profMaxTok = prof.MaxTokens
		}
		if prof.MaxTurns > 0 {
			profMaxTurns = prof.MaxTurns
		}
		if prof.Temperature > 0 {
			profTemp = prof.Temperature
		}
	}
	if profTemp > 0 && isCodingRole(spec.ID) {
		cfg.Temperature = profTemp
	}
	if profMaxTok > 0 && isCodingRole(spec.ID) && cfg.MaxTokens > profMaxTok {
		cfg.MaxTokens = profMaxTok
	}
	if profMaxTurns > 0 && isCodingRole(spec.ID) && cfg.MaxIterations > profMaxTurns {
		cfg.MaxIterations = profMaxTurns
	}
	// Token-stream early-exit: cancel remaining decode once a complete JSON /
	// tool-call is formed. Critical for slow local SLMs (oMLX / Ollama).
	cfg.EnableStreaming = true
	cfg.StreamingMode = llm.StreamModeForced
	cfg.EarlyExit = llm.DefaultEarlyExit

	def := agent.NewBaseAgentDefinition(cfg)
	// Initialize only stores the manager and registry; it cannot fail today,
	// and CreateAgent re-checks both for nil before building an agent.
	_ = def.Initialize(f.LLM, f.Tools)
	return def
}

func isCodingRole(id string) bool {
	switch id {
	case plan.RoleWorker, "deep", plan.RoleCorrector, plan.RoleTester,
		plan.RolePlaceholder, plan.RoleExplorer, "docs":
		return true
	default:
		return false
	}
}
