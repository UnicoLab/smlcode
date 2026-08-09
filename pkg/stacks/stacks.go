// Package stacks loads named provider/model presets (stacks/*.yaml) and merges
// them into config.Config. Stacks set the global LLM defaults; per-agent
// provider/model/endpoint overrides in .slmcode/agents/ still win at runtime.
package stacks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/config"
	"gopkg.in/yaml.v3"
)

// AgentDefault is an optional per-role LLM pin declared inside a stack YAML.
// Empty fields are ignored. Applied only when ApplyOptions.ApplyAgentDefaults.
type AgentDefault struct {
	Model    string `yaml:"model,omitempty" json:"model,omitempty"`
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
}

// Stack is a named preset: global LLM defaults + optional role defaults + UI meta.
type Stack struct {
	ID          string                  `json:"id"`
	Label       string                  `json:"label"`
	Description string                  `json:"description,omitempty"`
	Icon        string                  `json:"icon,omitempty"`
	Color       string                  `json:"color,omitempty"`
	EnvKey      string                  `json:"env_key,omitempty"`
	Path        string                  `json:"path,omitempty"`
	Provider    string                  `json:"provider"`
	Endpoint    string                  `json:"endpoint"`
	Model       string                  `json:"model"`
	Backend     string                  `json:"backend,omitempty"`
	// Agents is optional per-role LLM pins.
	Agents map[string]AgentDefault `json:"agents,omitempty"`
	// Pack optionally applies a language/domain building-block pack when the stack is applied.
	Pack string `json:"pack,omitempty"`
	// Raw holds the YAML map for precise key-aware merges.
	raw map[string]any
}

// ApplyOptions controls how a stack interacts with existing config and agents.
type ApplyOptions struct {
	// ApplyAgentDefaults writes stack.agents[*] into .slmcode/agents/ overrides.
	// Existing non-empty model/provider/endpoint on an agent are kept unless ForceAgents.
	ApplyAgentDefaults bool
	// ForceAgents overwrites existing per-agent LLM fields when applying defaults.
	ForceAgents bool
	// ClearAgentLLM clears model/provider/endpoint on all agent overrides so they
	// inherit the new stack globals. Other override fields (prompt, skills) stay.
	ClearAgentLLM bool
	// ApplyPack applies stack.pack (language pack) when set.
	ApplyPack bool
	// ForcePackAgents overwrites agent YAML when materializing pack agents.
	ForcePackAgents bool
}

// ApplyResult summarizes what changed.
type ApplyResult struct {
	StackID           string   `json:"stack_id"`
	Provider          string   `json:"provider"`
	Model             string   `json:"model"`
	Endpoint          string   `json:"endpoint"`
	AgentsUpdated     []string `json:"agents_updated,omitempty"`
	AgentsCleared     []string `json:"agents_cleared,omitempty"`
	ConflictingAgents []string `json:"conflicting_agents,omitempty"`
	PackID            string   `json:"pack_id,omitempty"`
	PipelineID        string   `json:"pipeline_id,omitempty"`
	QAGateCommand     string   `json:"qa_gate_command,omitempty"`
}

// FindDir locates the stacks/ directory (env, cwd walk, executable, source tree).
func FindDir() string {
	if v := strings.TrimSpace(os.Getenv("SLMCODE_STACKS")); v != "" {
		if st, err := os.Stat(v); err == nil && st.IsDir() {
			return v
		}
	}
	var candidates []string
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, walkUp(wd)...)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "stacks"))
		candidates = append(candidates, walkUp(filepath.Dir(exe))...)
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		// pkg/stacks → repo root
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
		candidates = append(candidates, filepath.Join(root, "stacks"))
	}
	for _, c := range candidates {
		if isStackPresetDir(c) {
			return c
		}
	}
	return ""
}

// isStackPresetDir requires a directory with at least one *.yaml preset so we
// never mistake the Go package path pkg/stacks for the YAML presets folder.
func isStackPresetDir(dir string) bool {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			return true
		}
	}
	return false
}

func walkUp(start string) []string {
	var out []string
	dir := start
	for i := 0; i < 8; i++ {
		out = append(out, filepath.Join(dir, "stacks"))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// List returns all stacks sorted by id.
func List() ([]Stack, error) {
	dir := FindDir()
	if dir == "" {
		return nil, fmt.Errorf("stacks directory not found (set SLMCODE_STACKS or run from repo root)")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Stack
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".yaml")
		s, err := Load(id)
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Load reads stacks/<id>.yaml.
func Load(id string) (*Stack, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return nil, fmt.Errorf("invalid stack id %q", id)
	}
	dir := FindDir()
	if dir == "" {
		return nil, fmt.Errorf("stacks directory not found")
	}
	path := filepath.Join(dir, id+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("stack %q: %w", id, err)
	}
	return Parse(id, path, data)
}

// Parse decodes stack YAML bytes.
func Parse(id, path string, data []byte) (*Stack, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse stack %q: %w", id, err)
	}
	s := &Stack{
		ID:   id,
		Path: path,
		raw:  raw,
	}
	s.Label = strKey(raw, "label")
	if s.Label == "" {
		s.Label = prettyLabel(id)
	}
	s.Description = strKey(raw, "description")
	s.Icon = strKey(raw, "icon")
	s.Color = strKey(raw, "color")
	s.EnvKey = strKey(raw, "env_key")
	s.Provider = config.NormalizeProvider(strKey(raw, "provider"))
	s.Endpoint = strKey(raw, "endpoint")
	s.Model = strKey(raw, "model")
	s.Backend = strKey(raw, "backend")
	if s.Backend == "" {
		s.Backend = config.BackendSLMCode
	}
	if s.Endpoint == "" && s.Provider != "" {
		s.Endpoint = config.DefaultEndpointFor(s.Provider)
	}
	if s.Icon == "" {
		s.Icon = defaultIcon(id)
	}
	if s.Color == "" {
		s.Color = defaultColor(id)
	}
	if s.EnvKey == "" {
		s.EnvKey = defaultEnvKey(s.Provider)
	}
	if s.Description == "" {
		s.Description = defaultDescription(id, s.Provider)
	}
	s.Pack = strKey(raw, "pack")
	if agentsRaw, ok := raw["agents"].(map[string]any); ok {
		s.Agents = map[string]AgentDefault{}
		for role, v := range agentsRaw {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			s.Agents[strings.ToLower(strings.TrimSpace(role))] = AgentDefault{
				Model:    strKey(m, "model"),
				Provider: strKey(m, "provider"),
				Endpoint: strKey(m, "endpoint"),
			}
		}
	}
	if s.Provider == "" || s.Model == "" {
		return nil, fmt.Errorf("stack %q: provider and model are required", id)
	}
	return s, nil
}

// Matches reports whether cfg currently looks like this stack (for UI active state).
func (s *Stack) Matches(cfg *config.Config) bool {
	if s == nil || cfg == nil {
		return false
	}
	if cfg.ActiveStack != "" {
		return cfg.ActiveStack == s.ID
	}
	return config.NormalizeProvider(cfg.Provider) == s.Provider &&
		strings.TrimSpace(cfg.Model) == s.Model
}

// Apply merges the stack into cfg (non-destructive for project-local fields) and
// optionally updates agent LLM overrides.
func Apply(cfg *config.Config, s *Stack, agentsDir string, opts ApplyOptions) (*ApplyResult, error) {
	if cfg == nil || s == nil {
		return nil, fmt.Errorf("config and stack required")
	}
	if s.raw == nil {
		return nil, fmt.Errorf("stack %q has no raw data", s.ID)
	}

	conflict := conflictingAgents(agentsDir)
	mergeStackIntoConfig(cfg, s)
	cfg.ActiveStack = s.ID
	normalizeStackCfg(cfg)

	res := &ApplyResult{
		StackID:           s.ID,
		Provider:          cfg.Provider,
		Model:             cfg.Model,
		Endpoint:          cfg.Endpoint,
		ConflictingAgents: conflict,
	}

	if opts.ClearAgentLLM && agentsDir != "" {
		cleared, err := clearAgentLLM(agentsDir)
		if err != nil {
			return nil, err
		}
		res.AgentsCleared = cleared
		res.ConflictingAgents = nil
	}

	if opts.ApplyAgentDefaults && len(s.Agents) > 0 && agentsDir != "" {
		updated, err := applyAgentDefaults(agentsDir, s.Agents, opts.ForceAgents)
		if err != nil {
			return nil, err
		}
		res.AgentsUpdated = updated
	}

	if opts.ApplyPack && strings.TrimSpace(s.Pack) != "" {
		reg, err := blocks.Load(cfg.Root)
		if err != nil {
			return nil, fmt.Errorf("load blocks: %w", err)
		}
		packRes, err := blocks.ApplyPack(cfg, reg, s.Pack, blocks.ApplyOptions{
			MaterializeAgents: true,
			ForceAgents:       opts.ForcePackAgents,
		})
		if err != nil {
			return nil, fmt.Errorf("apply pack %q: %w", s.Pack, err)
		}
		res.PackID = packRes.PackID
		res.PipelineID = packRes.PipelineID
		res.QAGateCommand = packRes.QAGateCommand
		if len(packRes.AgentsWritten) > 0 {
			res.AgentsUpdated = mergeUniqueSorted(res.AgentsUpdated, packRes.AgentsWritten)
		}
	}

	return res, nil
}

func mergeUniqueSorted(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func mergeStackIntoConfig(cfg *config.Config, s *Stack) {
	m := s.raw
	if v := strKey(m, "provider"); v != "" {
		cfg.Provider = config.NormalizeProvider(v)
	}
	if v := strKey(m, "endpoint"); v != "" {
		cfg.Endpoint = v
	} else if _, ok := m["provider"]; ok {
		cfg.Endpoint = config.DefaultEndpointFor(cfg.Provider)
	}
	if v := strKey(m, "model"); v != "" {
		cfg.Model = v
	}
	if v := strKey(m, "backend"); v != "" {
		cfg.Backend = v
	}
	if v, ok := asFloat(m["temperature"]); ok {
		cfg.Temperature = v
	}
	if v, ok := asInt(m["max_tokens"]); ok {
		cfg.MaxTokens = v
	}
	if v, ok := asInt(m["max_retries"]); ok {
		cfg.MaxRetries = v
	}
	if v, ok := asInt(m["max_parallel"]); ok {
		cfg.MaxParallel = v
	}
	if v, ok := asInt(m["max_context_kb"]); ok {
		cfg.MaxContextKB = v
	}
	if v, ok := asInt(m["think_passes"]); ok {
		cfg.ThinkPasses = v
	}
	if v, ok := asBool(m["qa_gate"]); ok {
		cfg.QAGate = v
	}
	if v, ok := asInt(m["qa_gate_max_rounds"]); ok {
		cfg.QAGateMaxRounds = v
	}
	if v, ok := asBool(m["post_worker_smoke"]); ok {
		cfg.PostWorkerSmoke = v
	}
	if v, ok := asBool(m["quality_monitor"]); ok {
		cfg.QualityMonitor = v
	}
	if v, ok := asBool(m["static_quality"]); ok {
		cfg.StaticQuality = v
	}
	if v, ok := asBool(m["thinking_budget"]); ok {
		cfg.ThinkingBudget = v
	}
	if v, ok := asBool(m["worker_critique"]); ok {
		cfg.WorkerCritique = v
	}
	if v, ok := asBool(m["write_guard"]); ok {
		cfg.WriteGuard = v
	}
	if v, ok := asBool(m["read_before_edit"]); ok {
		cfg.ReadBeforeEdit = v
	}
	if v, ok := asBool(m["tool_guidance"]); ok {
		cfg.ToolGuidance = v
	}
	if v, ok := asBool(m["knowledge_inject"]); ok {
		cfg.KnowledgeInject = v
	}
	if v, ok := asBool(m["context_compact"]); ok {
		cfg.ContextCompact = v
	}
	if v := strKey(m, "context_compact_engine"); v != "" {
		cfg.ContextCompactEngine = v
	}
	if v, ok := asBool(m["react_compact"]); ok {
		cfg.ReactCompact = v
	}
	if v, ok := asBool(m["session_event_log"]); ok {
		cfg.SessionEventLog = v
	}
	if v, ok := asBool(m["auto_refine"]); ok {
		cfg.AutoRefine = v
	}
	if v, ok := asInt(m["auto_refine_max_rounds"]); ok {
		cfg.AutoRefineMaxRounds = v
	}
	if v, ok := asInt(m["llm_retry_count"]); ok {
		cfg.LLMRetryCount = v
	}
	if v, ok := asInt(m["llm_retry_delay_ms"]); ok {
		cfg.LLMRetryDelayMS = v
	}
	if ss, ok := asStringSlice(m["enabled_models"]); ok {
		cfg.EnabledModels = ss
	}
	if v, ok := asBool(m["wave_snapshots"]); ok {
		cfg.WaveSnapshots = v
	}
	if v, ok := asBool(m["hooks_enabled"]); ok {
		cfg.HooksEnabled = v
	}
	if v := strKey(m, "clarify_mode"); v != "" {
		cfg.ClarifyMode = v
	}
	if v := strKey(m, "plan_approve"); v != "" {
		cfg.PlanApprove = v
	}
	if v := strKey(m, "escalate_ask"); v != "" {
		cfg.EscalateAsk = v
	}
	if v := strKey(m, "continue_ask"); v != "" {
		cfg.ContinueAsk = v
	}
	if v, ok := asBool(m["auto_approve"]); ok {
		cfg.AutoApprove = v
	}
	if v := strKey(m, "price_preset"); v != "" {
		cfg.PricePreset = v
	}
	if profiles, ok := m["model_profiles"].(map[string]any); ok && len(profiles) > 0 {
		raw, err := yaml.Marshal(map[string]any{"model_profiles": profiles})
		if err == nil {
			var wrap struct {
				ModelProfiles map[string]config.ModelProfile `yaml:"model_profiles"`
			}
			if yaml.Unmarshal(raw, &wrap) == nil && len(wrap.ModelProfiles) > 0 {
				cfg.ModelProfiles = wrap.ModelProfiles
			}
		}
	}
}

func normalizeStackCfg(cfg *config.Config) {
	cfg.Provider = config.NormalizeProvider(cfg.Provider)
	if cfg.Endpoint == "" {
		cfg.Endpoint = config.DefaultEndpointFor(cfg.Provider)
	}
	if cfg.Backend == "" {
		cfg.Backend = config.BackendSLMCode
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = config.DefaultMaxRetries
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = config.DefaultMaxParallel
	}
	if cfg.MaxContextKB <= 0 {
		cfg.MaxContextKB = config.DefaultMaxContextKB
	}
	if cfg.ThinkPasses <= 0 {
		cfg.ThinkPasses = config.DefaultThinkPasses
	}
}

func conflictingAgents(agentsDir string) []string {
	if agentsDir == "" {
		return nil
	}
	list, err := agents.LoadCustomSpecs(agentsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range list {
		if a.Model != "" || a.Provider != "" || a.Endpoint != "" {
			out = append(out, a.ID)
		}
	}
	sort.Strings(out)
	return out
}

func clearAgentLLM(agentsDir string) ([]string, error) {
	list, err := agents.LoadCustomSpecs(agentsDir)
	if err != nil {
		return nil, err
	}
	var cleared []string
	for _, a := range list {
		if a.Model == "" && a.Provider == "" && a.Endpoint == "" {
			continue
		}
		a.Model = ""
		a.Provider = ""
		a.Endpoint = ""
		if _, err := agents.WriteCustom(agentsDir, a); err != nil {
			return cleared, err
		}
		cleared = append(cleared, a.ID)
	}
	sort.Strings(cleared)
	return cleared, nil
}

func applyAgentDefaults(agentsDir string, defaults map[string]AgentDefault, force bool) ([]string, error) {
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return nil, err
	}
	existing, _ := agents.LoadCustomSpecs(agentsDir)
	byID := map[string]agents.CustomSpec{}
	for _, a := range existing {
		byID[a.ID] = a
	}
	var updated []string
	for role, d := range defaults {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" || (d.Model == "" && d.Provider == "" && d.Endpoint == "") {
			continue
		}
		cur, ok := byID[role]
		if !ok {
			cur = agents.CustomSpec{ID: role}
			if agents.BuiltinIDs()[role] {
				cur.Override = true
				cur.Builtin = true
			}
		}
		changed := false
		if d.Model != "" && (force || cur.Model == "") {
			cur.Model = d.Model
			changed = true
		}
		if d.Provider != "" && (force || cur.Provider == "") {
			cur.Provider = d.Provider
			changed = true
		}
		if d.Endpoint != "" && (force || cur.Endpoint == "") {
			cur.Endpoint = d.Endpoint
			changed = true
		}
		if !changed {
			continue
		}
		if err := agents.NormalizeCustom(&cur); err != nil {
			return updated, fmt.Errorf("agent %s: %w", role, err)
		}
		if _, err := agents.WriteCustom(agentsDir, cur); err != nil {
			return updated, err
		}
		updated = append(updated, role)
	}
	sort.Strings(updated)
	return updated, nil
}

func strKey(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	default:
		return 0, false
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case uint64:
		return int(t), true
	case float64:
		return int(t), true
	default:
		return 0, false
	}
}

func asBool(v any) (bool, bool) {
	t, ok := v.(bool)
	return t, ok
}

func asStringSlice(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		out := append([]string{}, t...)
		return out, true
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" && s != "<nil>" {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func prettyLabel(id string) string {
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func defaultIcon(id string) string {
	switch id {
	case "omlx-local":
		return "🍎"
	case "ollama-local":
		return "🦙"
	case "deepseek":
		return "🐋"
	case "qwen":
		return "🐉"
	case "google":
		return "💎"
	case "openai":
		return "⚡"
	case "openrouter":
		return "🌐"
	case "groq":
		return "⚙️"
	default:
		return "📦"
	}
}

func defaultColor(id string) string {
	switch id {
	case "omlx-local":
		return "from-violet-500 to-purple-600"
	case "ollama-local":
		return "from-amber-500 to-orange-600"
	case "deepseek":
		return "from-blue-500 to-cyan-600"
	case "qwen":
		return "from-teal-500 to-emerald-600"
	case "google":
		return "from-blue-600 to-indigo-700"
	case "openai":
		return "from-emerald-500 to-green-600"
	case "openrouter":
		return "from-rose-500 to-pink-600"
	case "groq":
		return "from-orange-500 to-red-600"
	default:
		return "from-gray-500 to-gray-700"
	}
}

func defaultEnvKey(provider string) string {
	switch config.NormalizeProvider(provider) {
	case "openai":
		return "OPENAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "gemini", "google":
		return "GOOGLE_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	default:
		return ""
	}
}

func defaultDescription(id, provider string) string {
	switch id {
	case "omlx-local":
		return "Apple MLX on Mac — ultra-light local SLM"
	case "ollama-local":
		return "Run models locally via Ollama"
	case "deepseek":
		return "DeepSeek V3/R1 — affordable reasoning"
	case "qwen":
		return "Qwen via OpenRouter"
	case "google":
		return "Gemini — long context"
	case "openai":
		return "GPT-4o — maximum capability"
	case "openrouter":
		return "Any model via OpenRouter proxy"
	case "groq":
		return "Fast inference via Groq LPU"
	default:
		return "Provider stack: " + provider
	}
}

// PresetView is the Studio-facing stack card (aligned with former StackPreset).
func (s *Stack) PresetView(active bool) map[string]any {
	temp, _ := asFloat(s.raw["temperature"])
	maxTok, _ := asInt(s.raw["max_tokens"])
	maxPar, _ := asInt(s.raw["max_parallel"])
	maxRet, _ := asInt(s.raw["max_retries"])
	maxCtx, _ := asInt(s.raw["max_context_kb"])
	think, _ := asInt(s.raw["think_passes"])
	return map[string]any{
		"id":             s.ID,
		"label":          s.Label,
		"description":    s.Description,
		"icon":           s.Icon,
		"color":          s.Color,
		"env_key":        s.EnvKey,
		"provider":       s.Provider,
		"endpoint":       s.Endpoint,
		"model":          s.Model,
		"backend":        s.Backend,
		"temperature":    temp,
		"max_tokens":     maxTok,
		"max_parallel":   maxPar,
		"max_retries":    maxRet,
		"max_context_kb": maxCtx,
		"think_passes":   think,
		"agents":         s.Agents,
		"pack":           s.Pack,
		"active":         active,
	}
}
