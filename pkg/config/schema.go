package config

import (
	"reflect"
	"sort"
	"strings"
	"sync"
)

// FieldSchema describes one config key for `slmcode config set` validation,
// `slmcode config show --origin`, the Studio settings page and the TUI's
// /schema help. One table, one source of truth: the CLI used to carry a second
// hand-written copy for the ~40 keys this one did not describe.
type FieldSchema struct {
	Key   string `json:"key"`
	Type  string `json:"type"` // string | int | float | bool | duration | string[] | map | list
	Label string `json:"label"`
	// Description is the one-line help shown next to the field.
	Description string `json:"description,omitempty"`
	// Group buckets the key in the settings UI and `config show --group`.
	Group string `json:"group"`
	// Enum is the closed set of allowed values, when there is one.
	Enum []string `json:"enum,omitempty"`
	// AllowEmpty permits clearing an enum-valued key.
	AllowEmpty bool `json:"allow_empty,omitempty"`
	// Default is the built-in default, rendered like the YAML file does.
	Default any `json:"default,omitempty"`
	// Env is the environment variable that overrides this key.
	Env string `json:"env"`
	// Patchable reports whether `config set` / the Studio API may write it.
	Patchable bool `json:"patchable"`
	// Secret marks a value that must be redacted when displayed.
	Secret bool `json:"secret,omitempty"`
	// Advanced hides the key from the default `config show`.
	Advanced bool `json:"advanced,omitempty"`
}

// Groups are the schema buckets, in display order.
var Groups = []string{
	"model", "pipeline", "context", "tools", "quality",
	"hitl", "safety", "memory", "learning", "retrieval",
	"cost", "blocks", "stacks", "ui", "server",
}

// schemaMeta is the hand-written half of the schema: everything that cannot be
// derived from the struct. Type and Default come from reflection so they can
// never drift from the field they describe.
type schemaMeta struct {
	Label       string
	Description string
	Group       string
	Enum        []string
	AllowEmpty  bool
	NotPatch    bool
	Secret      bool
	Advanced    bool
}

var schemaFields = map[string]schemaMeta{
	// ── model ──
	"provider": {"Provider", "LLM provider id — omlx, ollama, openai, lmstudio, openrouter, vllm, … (any other name is treated as an OpenAI-compatible gateway)", "model", nil, false, false, false, false},
	"endpoint": {"Endpoint", "OpenAI-compatible base URL for the provider", "model", nil, false, false, false, false},
	"api_key":  {"API key", "Provider API key. Prefer .slmcode/auth.json or SLMCODE_API_KEY — it is not written to config.yaml", "model", nil, false, false, true, false},
	"model":    {"Model", "Default model id served by the provider", "model", nil, false, false, false, false},
	"fast_model": {"Fast model", "Smaller model for lightweight roles (reviewer, coordinator, splitter, planner, context, architect, clarifier). Empty = use model",
		"model", nil, false, false, false, false},
	"backend":            {"Backend", "Execution engine", "model", []string{"slmcode", "claude-code"}, false, false, false, false},
	"enabled_models":     {"Enabled models", "Optional allow-list of selectable model ids (empty = every model the provider serves)", "model", nil, false, false, false, false},
	"temperature":        {"Temperature", "Sampling temperature for every role", "model", nil, false, false, false, false},
	"max_tokens":         {"Max tokens", "Per-response completion budget", "model", nil, false, false, false, false},
	"llm_retry_count":    {"LLM retry count", "Provider HTTP retries — separate from max_retries, which is the board's review loop", "model", nil, false, false, false, false},
	"llm_retry_delay_ms": {"LLM retry delay", "Milliseconds between provider HTTP retries", "model", nil, false, false, false, false},
	"model_profiles":     {"Model profiles", "Per-model overrides for context limit, thinking budget and skill/knowledge budgets", "model", nil, false, true, false, true},
	"claude_code_bin":    {"Claude Code binary", "Executable used when backend=claude-code", "model", nil, false, false, false, true},
	"structured_decoding": {"Structured decoding", "Constrained decoding: auto negotiates the strongest mechanism the endpoint supports (json_schema, guided_json, GBNF); off forces prompt-only JSON",
		"model", []string{DecodingAuto, DecodingOff}, false, false, false, false},

	// ── pipeline ──
	"mode":       {"Mode", "full runs the whole pipeline; specialist runs a single role", "pipeline", []string{ModeFull, ModeSpecialist}, false, false, false, false},
	"specialist": {"Specialist", "Role id run when mode=specialist (worker, explorer, …)", "pipeline", nil, false, false, false, false},
	"dynamic_pipeline": {"Dynamic pipeline", "Run the composer specialist first to assemble a task-specific pipeline (phases, team, tools, skills)",
		"pipeline", nil, false, false, false, false},
	"pinned_skills":    {"Pinned skills", "Skills always loaded, in addition to @skill: refs and matching", "pipeline", nil, false, false, false, false},
	"max_parallel":     {"Max parallel", "Concurrent workers on ready tasks", "pipeline", nil, false, false, false, false},
	"max_retries":      {"Max retries", "Review/correct retries per task before escalation (0 = one attempt)", "pipeline", nil, false, false, false, false},
	"think_passes":     {"Think passes", "Multi-pass deliberation loops per role", "pipeline", nil, false, false, false, false},
	"task_timeout":     {"Task timeout", "Wall-clock ceiling for a single task", "pipeline", nil, false, false, false, false},
	"max_task_calls":   {"Max task calls", "Per-task LLM call budget handed to the inner loop", "pipeline", nil, false, false, false, false},
	"architect_editor": {"Architect + editor", "Enable the describer→editor role pair. Doubles LLM calls per task; pays off when the halves use different models", "pipeline", nil, false, false, false, false},

	// ── context ──
	"max_context_kb":         {"Max context KB", "Legacy prompt budget in KB, used when a model profile declares no real context window", "context", nil, false, false, false, false},
	"context_compact":        {"CONTEXT compaction", "Summarize CONTEXT.md mid-run when it outgrows the budget", "context", nil, false, false, false, false},
	"context_compact_engine": {"Compaction engine", "How CONTEXT.md is compacted", "context", []string{"heuristic", "llm", "auto"}, false, false, false, false},
	"react_compact": {"ReAct compaction", "Compact the agent conversation at checkpoint and resume when it approaches the window. A single long agent call is NOT compacted mid-flight",
		"context", nil, false, false, false, false},
	"react_compact_at_percent": {"ReAct compaction threshold", "Percentage of the context budget at which the conversation is compacted (0 disables)",
		"context", nil, false, false, false, false},
	"repo_map_tokens":      {"Repo map tokens", "Token allowance for the ranked repo-symbol map inside a context pack. 0 disables the map", "context", nil, false, false, false, false},
	"excerpt_window_lines": {"Excerpt window", "± lines kept around each relevance match when a file is excerpted rather than inlined whole", "context", nil, false, false, false, false},
	"skill_disclosure": {"Skill disclosure", "Progressive disclosure tier: auto (cards, bodies only when earned) | cards (never inline a body) | full (inline every match)",
		"context", []string{SkillDisclosureAuto, SkillDisclosureCards, SkillDisclosureFull}, false, false, false, false},
	"skill_max_expanded":              {"Max expanded skills", "Cap on how many skill bodies are inlined at once", "context", nil, false, false, false, false},
	"context_reserve_system_tokens":   {"Reserve: system", "Tokens held back for the role's system prompt before the pack gets its share", "context", nil, false, false, false, true},
	"context_reserve_tool_tokens":     {"Reserve: tools", "Tokens held back for the ws_* tool schemas", "context", nil, false, false, false, true},
	"context_reserve_response_tokens": {"Reserve: response", "Tokens held back so the model can still answer", "context", nil, false, false, false, true},
	"context_slack_percent":           {"Context slack", "Percentage held back for tokenizer disagreement and chat scaffolding", "context", nil, false, false, false, true},
	"context_role_budget": {"Per-role context budget", "Percentage of the available window each role may use, keyed by role id (empty = built-in table)",
		"context", nil, false, true, false, true},
	"read_head_lines": {"Read head lines", "Default head size for automatic file reads", "context", nil, false, false, false, false},
	"skills_dirs":     {"Skill directories", "Extra skill roots, in addition to the bundled set and .slmcode/skills", "context", nil, false, true, false, true},

	// ── tools ──
	"read_window_lines":    {"Read window", "ws_read window size in lines (0 = the tool's own default)", "tools", nil, false, false, false, false},
	"max_tool_chars":       {"Max tool chars", "Hard cap on a tool result before it is trimmed (0 = the tool's own default)", "tools", nil, false, false, false, false},
	"shell_timeout":        {"Shell timeout", "ws_shell per-command timeout (0 = the tool's own default)", "tools", nil, false, false, false, false},
	"tool_guidance":        {"Tool guidance", "Inject per-turn tool skill cards", "tools", nil, false, false, false, false},
	"knowledge_inject":     {"Knowledge injection", "Inject keyword-matched knowledge cards", "tools", nil, false, false, false, false},
	"auto_text_tools":      {"Recover prose tool calls", "Recover valid tool calls from JSON embedded in prose", "tools", nil, false, false, false, false},
	"hooks_enabled":        {"Hooks", "Load .slmcode/hooks.json PreToolUse / PostToolUse. Off by default: hooks.json ships with the repository and runs shell commands; it also needs `slmcode hooks trust`", "tools", nil, false, false, false, false},
	"mcp_servers":          {"MCP servers", "Read-only MCP connections (stdio or HTTP). Honored ONLY from the user config — a project file cannot make the harness spawn processes", "tools", nil, false, true, false, true},
	"disable_syntax_check": {"Disable syntax check", "Turn off post-edit syntax verification", "tools", nil, false, false, false, false},

	// ── quality ──
	"qa_gate":            {"QA gate", "Run an iterate-until-green test command after the board finishes", "quality", nil, false, false, false, false},
	"qa_gate_command":    {"QA gate command", "Test command for the QA gate (empty = auto-detect)", "quality", nil, false, false, false, false},
	"qa_gate_max_rounds": {"QA gate rounds", "Diagnose/fix rounds the QA gate may take. Below 2 the repair loop is unreachable", "quality", nil, false, false, false, false},
	"qa_bootstrap": {"QA bootstrap", "Whether the QA gate may run dependency installers (pip install, npm install, go mod tidy) against agent-authored manifests",
		"quality", []string{QABootstrapOff, QABootstrapAsk, QABootstrapAuto}, false, false, false, false},
	"regression_checks":      {"Regression checks", "Replay stored regression checks around the QA gate", "quality", nil, false, false, false, false},
	"post_worker_smoke":      {"Post-worker smoke", "Run a deterministic compile/test after each worker before review may approve", "quality", nil, false, false, false, false},
	"scope_judge":            {"Scope judge", "Run a post-split PRD completeness check", "quality", nil, false, false, false, false},
	"placeholder_pass":       {"Placeholder pass", "Scan for stubs after execute and fill or flag them", "quality", nil, false, false, false, false},
	"worker_critique":        {"Worker critique", "Run an automatic self-fix pass on weak worker output", "quality", nil, false, false, false, false},
	"quality_monitor":        {"Quality monitor", "Nudge on empty output, loops and hallucinated tool names", "quality", nil, false, false, false, false},
	"static_quality":         {"Static quality", "Reject stub and placeholder code", "quality", nil, false, false, false, false},
	"require_smoke":          {"Require smoke", "Coding tasks need a passing smoke run before approval", "quality", nil, false, false, false, false},
	"claims_gate":            {"Claims gate", "Reject outputs claiming changes that are not on disk", "quality", nil, false, false, false, false},
	"finalize_warn":          {"Finalize warning", "Warn the model before it exhausts its iteration budget", "quality", nil, false, false, false, false},
	"thinking_budget":        {"Thinking budget", "Nudge the model to commit to an implementation", "quality", nil, false, false, false, false},
	"thinking_budget_tokens": {"Thinking budget tokens", "Hard abort threshold for over-long deliberation", "quality", nil, false, false, false, false},

	// ── hitl ──
	"clarify_mode":         {"Clarify mode", "Whether specification questions pause for the user", "hitl", []string{"ask", "auto", "off"}, false, false, false, false},
	"clarify_timeout":      {"Clarify timeout", "How long ask mode waits before applying the recommended answer", "hitl", nil, false, false, false, false},
	"plan_approve":         {"Plan approval", "Whether the execution plan pauses for approval before build", "hitl", []string{"ask", "auto", "off"}, false, false, false, false},
	"plan_approve_timeout": {"Plan approval timeout", "How long the plan gate waits for an answer", "hitl", nil, false, false, false, false},
	"plan_approve_on_timeout": {"Plan timeout policy", "What an unanswered plan gate does: approve | reject | auto (approve only when no event subscriber was attached, so no UI could have answered)",
		"hitl", []string{PlanTimeoutApprove, PlanTimeoutReject, PlanTimeoutAuto}, false, false, false, false},
	"continue_ask":           {"Continue ask", "Prompt the user when retries or the QA gate are exhausted", "hitl", []string{"ask", "auto", "off"}, false, false, false, false},
	"continue_ask_timeout":   {"Continue timeout", "How long the continue gate waits before stopping", "hitl", nil, false, false, false, false},
	"escalate_ask":           {"Escalate ask", "Pause for a human when a task hits max retries", "hitl", []string{"ask", "auto", "off"}, false, false, false, false},
	"escalate_ask_timeout":   {"Escalate timeout", "How long the escalate gate waits before the arbitrator decides. A human has to notice, read and choose", "hitl", nil, false, false, false, false},
	"escalate_timeout_agent": {"Escalate arbitrator", "Specialist that decides on escalate timeout (empty = escalate → reviewer → coordinator)", "hitl", nil, false, false, false, false},
	"escalate_max_retries": {"Escalate retry cap", "How many times one task may be reopened by answering retry at the escalate gate before retry is refused and the task is re-scoped",
		"hitl", nil, false, false, false, false},
	"auto_approve":      {"Auto approve", "Skip every HITL wait, taking the recommended answer", "hitl", nil, false, false, false, false},
	"shell_ask_timeout": {"Shell approval timeout", "How long an interactive shell approval waits when shell_permission=ask", "hitl", nil, false, false, false, false},

	// ── safety ──
	"permission":        {"Permission", "Write policy for agent file changes", "safety", []string{"auto", "dry-run", "review"}, false, false, false, false},
	"shell_permission":  {"Shell permission", "ws_shell policy, independent of file writes", "safety", []string{"allow", "ask", "deny"}, false, false, false, false},
	"shell_whitelist":   {"Shell whitelist", "Restrict ws_shell to safe command prefixes unless explicitly allowed", "safety", nil, false, false, false, false},
	"shell_allow":       {"Shell allow-list", "Extra command prefixes ws_shell may run (also SLMCODE_BASH_ALLOW)", "safety", nil, false, false, false, false},
	"dry_run":           {"Dry run", "Never write code files; stays in sync with permission=dry-run", "safety", nil, false, false, false, false},
	"write_guard":       {"Write guard", "ws_write refuses to clobber an existing file", "safety", nil, false, false, false, false},
	"read_before_edit":  {"Read before edit", "Edit and patch require a prior read of the file", "safety", nil, false, false, false, false},
	"shell_write_guard": {"Shell write guard", "Block shell redirection that would bypass the file guards", "safety", nil, false, false, false, false},
	"over_edit_guard":   {"Over-edit guard", "Refuse whole-file rewrites when a small edit was expected", "safety", nil, false, false, false, false},
	"wave_snapshots":    {"Wave snapshots", "Store per-wave file rewind points under .slmcode/waves", "safety", nil, false, false, false, false},
	"file_checkpoints":  {"File checkpoints", "Snapshot each file before its first write (first-write-wins)", "safety", nil, false, false, false, false},

	// ── memory & learning ──
	"evolve":        {"Evolve", "Enable the self-improvement engine: memory, learned repair rules, bandit policy and regression checks", "memory", nil, false, false, false, false},
	"memory_tokens": {"Memory tokens", "Token budget for the memory block injected into each role's prompt", "memory", nil, false, false, false, false},
	"deterministic": {"Deterministic", "Greedy bandit, no exploration — for CI and reproducible runs. dry_run implies it", "learning", nil, false, false, false, false},
	"autoresearch": {"Autoresearch", "Let `slmcode autoresearch` apply the changes it proposes to agent prompts and whitelisted config knobs. Off by default; without it the command only dry-runs",
		"learning", nil, false, false, false, false},
	"auto_refine":            {"Auto refine", "Append refine notes from wave lessons into CONTEXT", "learning", nil, false, false, false, false},
	"auto_refine_max_rounds": {"Refine max rounds", "Cap on refine passes per run", "learning", nil, false, false, false, false},
	"session_event_log":      {"Session event log", "Write .slmcode/queries/<id>/events.jsonl during runs", "learning", nil, false, false, false, false},

	// ── retrieval ──
	"embedding_enabled":   {"Embeddings", "Use an embedding endpoint for context retrieval (falls back to lexical TF-IDF)", "retrieval", nil, false, false, false, false},
	"embedding_endpoint":  {"Embedding endpoint", "OpenAI-compatible /v1/embeddings base URL (empty = the chat endpoint)", "retrieval", nil, false, false, false, false},
	"embedding_model":     {"Embedding model", "Model id used for embeddings", "retrieval", nil, false, false, false, false},
	"embedding_api_key":   {"Embedding API key", "Key for the embedding endpoint (defaults to api_key)", "retrieval", nil, false, false, true, false},
	"embedding_top_k":     {"Embedding top K", "How many chunks a retrieval query returns", "retrieval", nil, false, false, false, false},
	"retrieval_min_score": {"Retrieval floor", "Similarity floor for injected knowledge. 0 keeps the calibrated per-embedder default; raising it injects less but more relevant material", "retrieval", nil, false, false, false, false},
	"retrieval_cache_dir": {"Retrieval cache", "Directory holding the on-disk embedding cache (empty = .slmcode)", "retrieval", nil, false, false, false, true},

	// ── cost ──
	"price_preset": {"Price preset", "Ballpark $/MTok rates for the cost display; explicit price_* always win", "cost",
		[]string{"", "off", "local", "omlx", "openai", "anthropic", "openrouter"}, true, false, false, false},
	"price_prompt_per_mtok":     {"Prompt price /MTok", "Explicit prompt price in $ per million tokens", "cost", nil, false, false, false, false},
	"price_completion_per_mtok": {"Completion price /MTok", "Explicit completion price in $ per million tokens", "cost", nil, false, false, false, false},

	// ── blocks & stacks ──
	"active_pack":     {"Active pack", "Language/domain building-block pack id (go, python, react, web, …)", "blocks", nil, false, false, false, false},
	"active_pipeline": {"Active pipeline block", "Named pipeline preset id from the blocks catalog", "blocks", nil, false, false, false, false},
	"active_stack":    {"Active stack", "Last applied stacks/<id>.yaml preset (empty = provider and model were set manually)", "stacks", nil, false, false, false, false},

	// ── ui & server ──
	"compact_mode": {"Compact stream", "Trim live event verbosity in the TUI and CLI", "ui", nil, false, false, false, false},
	"verbose":      {"Verbose", "Verbose output (same as --log-level=info)", "ui", nil, false, false, false, false},
	"listen":       {"Studio listen address", "Address Studio binds, e.g. 127.0.0.1:7420", "server", nil, false, false, false, false},
}

var (
	schemaOnce  sync.Once
	schemaCache []FieldSchema
	schemaIndex map[string]FieldSchema
)

func buildSchema() {
	def := Default("")
	normalize(def)
	schemaIndex = map[string]FieldSchema{}
	for _, key := range Keys() {
		if key == "config_version" {
			continue
		}
		meta, ok := schemaFields[key]
		if !ok {
			// A field with no metadata is still real and still settable — it
			// just has no curated help. Failing to list it is what produced
			// the CLI's duplicate table in the first place.
			meta = schemaMeta{Label: titleize(key), Group: "advanced", Advanced: true}
		}
		typ, _ := KindOf(key)
		f := FieldSchema{
			Key:         key,
			Type:        typ,
			Label:       meta.Label,
			Description: meta.Description,
			Group:       meta.Group,
			Enum:        meta.Enum,
			AllowEmpty:  meta.AllowEmpty,
			Env:         EnvVarFor(key),
			Patchable:   !meta.NotPatch,
			Secret:      meta.Secret,
			Advanced:    meta.Advanced,
		}
		if v, ok := def.Get(key); ok && !meta.Secret {
			f.Default = v
		}
		schemaCache = append(schemaCache, f)
		schemaIndex[key] = f
	}
	sort.SliceStable(schemaCache, func(i, j int) bool {
		gi, gj := groupRank(schemaCache[i].Group), groupRank(schemaCache[j].Group)
		if gi != gj {
			return gi < gj
		}
		return schemaCache[i].Key < schemaCache[j].Key
	})
}

func groupRank(g string) int {
	for i, name := range Groups {
		if name == g {
			return i
		}
	}
	return len(Groups)
}

func titleize(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i == 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// Schema returns metadata for every config key, ordered by group.
func Schema() []FieldSchema {
	schemaOnce.Do(buildSchema)
	out := make([]FieldSchema, len(schemaCache))
	copy(out, schemaCache)
	return out
}

// Field looks up one key's schema entry, resolving aliases.
func Field(key string) (FieldSchema, bool) {
	schemaOnce.Do(buildSchema)
	f, ok := schemaIndex[CanonicalKey(key)]
	return f, ok
}

// PatchableField looks up a key that `config set` may write.
func PatchableField(key string) (FieldSchema, bool) {
	f, ok := Field(key)
	if !ok || !f.Patchable {
		return FieldSchema{}, false
	}
	return f, true
}

// Aliases maps historical short spellings onto real schema keys. They are
// honored by `config set`, by the SLMCODE_* overlay and by the file loader.
var Aliases = map[string]string{
	"parallel":   "max_parallel",
	"retries":    "max_retries",
	"think":      "think_passes",
	"context_kb": "max_context_kb",
	"qa_cmd":     "qa_gate_command",
	"qa_rounds":  "qa_gate_max_rounds",
	"perm":       "permission",
	"dry-run":    "dry_run",
	"agent":      "specialist",
	"skills":     "pinned_skills",
	"dynamic":    "dynamic_pipeline",
	"composer":   "dynamic_pipeline",
	"no-explore": "deterministic",
	"memory":     "memory_tokens",
}

// CanonicalKey resolves an alias or a loosely-typed key to its schema key.
func CanonicalKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	if real, ok := Aliases[k]; ok {
		return real
	}
	return k
}

// ZeroValueFor returns the literal `config unset` writes for a key: the
// built-in default, so unsetting restores inheritance rather than zeroing.
func ZeroValueFor(key string) (any, bool) {
	f, ok := Field(key)
	if !ok {
		return nil, false
	}
	return f.Default, true
}

// Unset restores a key to the value it would inherit from the layers below the
// project file — the user config when one supplies it, otherwise the default.
func (c *Config) Unset(key string) error {
	key = CanonicalKey(key)
	f, ok := fields()[key]
	if !ok {
		return &UnknownKeyError{Key: key}
	}
	base := c.saveBaseline()
	v := reflect.ValueOf(*base).Field(f.Index)
	reflect.ValueOf(c).Elem().Field(f.Index).Set(v)
	c.Provenance().clearProjectMark(key)
	normalize(c)
	return nil
}

// SlashHelp returns short TUI /help lines for new commands.
func SlashHelp() []string {
	return []string{
		"/models [query]     — search models (auth-aware)",
		"/mcp                — MCP connection status",
		"/schema             — list patchable config fields",
		"/auth set <key>     — save API key to .slmcode/auth.json",
		"/compact [engine]   — compact CONTEXT (heuristic|llm|auto)",
	}
}
