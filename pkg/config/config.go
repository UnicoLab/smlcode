package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/authstore"
	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/permissions"
	"gopkg.in/yaml.v3"
)

const (
	DirName = ".slmcode"

	DefaultProvider = "omlx"
	DefaultEndpoint = "http://127.0.0.1:8000/v1"
	DefaultModel    = "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"

	DefaultMaxRetries   = 4
	DefaultMaxParallel  = 4
	DefaultThinkPasses  = 1
	DefaultMaxContextKB = 16
	DefaultTaskTimeout  = 12 * time.Minute
	// DefaultQAGateRounds is 3 because the QA gate's repair loop compares
	// `round == max` on entry: at 1 the diagnose/fix pass is unreachable and
	// the gate degrades into a single test run.
	DefaultQAGateRounds = 3

	DefaultClarifyTimeout     = 2 * time.Minute
	DefaultPlanApproveTimeout = 2 * time.Minute
	DefaultContinueAskTimeout = 2 * time.Minute
	// DefaultEscalateAskTimeout is 5 minutes: a human has to notice the
	// prompt, read the task and choose, and every expiry costs an extra LLM
	// call in the timeout arbitrator. 30s was far too short for a mode named
	// "ask".
	DefaultEscalateAskTimeout = 5 * time.Minute
	// DefaultEscalateMaxRetries mirrors plan.DefaultMaxGateRetries — kept as a
	// literal here because pkg/plan must not depend on pkg/config, and pinned
	// to it by TestEscalateRetryCapDefaultsAgree in pkg/orchestrator.
	DefaultEscalateMaxRetries = 2
	DefaultShellAskTimeout    = 2 * time.Minute

	// Self-improvement / budget defaults.
	DefaultMemoryTokens = 300
	// DefaultMaxTaskCalls is the per-task LLM call budget that actually reaches
	// the loop (loop.DefaultMaxTaskCalls is only the fallback for a zero-valued
	// Runner). It is DERIVED from MaxRetries, not picked: a task's honest floor
	// is worker + self-critique + MaxRetries × (review + correct), which at the
	// default MaxRetries=4 is 1 + 1 + 8 = 10. At the old value of 6 a task got
	// exactly TWO correction rounds no matter what max_retries said, and
	// escalated to a human with half its retries unspent. Keep in step with
	// MaxRetries — loop.MaxTaskCallsFor is the relationship, and buildRunner
	// warns when a configured budget caps the configured retries.
	DefaultMaxTaskCalls = 10

	// Context-engineering defaults. These mirror the package-level defaults in
	// pkg/context, pkg/repomap and pkg/skills so a zero config behaves exactly
	// as those packages do on their own.
	DefaultRepoMapTokens        = 900
	DefaultExcerptWindowLines   = 25
	DefaultSkillMaxExpanded     = 2
	DefaultReserveSystemTokens  = 500
	DefaultReserveToolTokens    = 900
	DefaultReserveResponseTok   = 2048
	DefaultContextSlackPercent  = 10
	DefaultStructuredDecoding   = "auto"
	DefaultQABootstrap          = "ask"
	DefaultSkillDisclosure      = "auto"
	DefaultPlanApproveOnTimeout = PlanTimeoutAuto
)

// PlanApproveOnTimeout values.
const (
	// PlanTimeoutApprove auto-approves when nobody answers (legacy default).
	PlanTimeoutApprove = "approve"
	// PlanTimeoutReject stops before execute when nobody answers.
	PlanTimeoutReject = "reject"
	// PlanTimeoutAuto approves only when NO event subscriber was attached —
	// i.e. when there was no UI that could have answered.
	PlanTimeoutAuto = "auto"
)

// StructuredDecoding values.
const (
	// DecodingAuto negotiates the strongest mechanism the endpoint supports.
	DecodingAuto = "auto"
	// DecodingOff forces prompt-only JSON.
	DecodingOff = "off"
)

// QABootstrap values — whether dependency installs may run unattended.
const (
	QABootstrapOff  = "off"
	QABootstrapAsk  = "ask"
	QABootstrapAuto = "auto"
)

// SkillDisclosure values.
const (
	// SkillDisclosureAuto is progressive disclosure (cards + earned bodies).
	SkillDisclosureAuto = "auto"
	// SkillDisclosureCards never inlines a body.
	SkillDisclosureCards = "cards"
	// SkillDisclosureFull inlines every matched skill body.
	SkillDisclosureFull = "full"
)

// MCPServerConfig is a thin read-only MCP server entry.
type MCPServerConfig struct {
	Name     string            `yaml:"name" json:"name"`
	Command  string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args     []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env      map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	URL      string            `yaml:"url,omitempty" json:"url,omitempty"`
	ReadOnly *bool             `yaml:"read_only,omitempty" json:"read_only,omitempty"`
}

// NormalizeProvider canonicalizes provider aliases.
// Unknown names are kept (treated as OpenAI-compatible gateways).
func NormalizeProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case "", "default":
		return DefaultProvider
	case "mlx":
		return "omlx"
	case "openai-compatible", "openai_compat", "openai-compat", "openai_compatible":
		return "openai"
	case "lm-studio", "lm_studio":
		return "lmstudio"
	case "google":
		// Alias — stack YAMLs and Gemini API docs use "gemini".
		return "gemini"
	default:
		return p
	}
}

// IsOllama reports whether the provider uses the native Ollama API.
func IsOllama(p string) bool {
	return NormalizeProvider(p) == "ollama"
}

// IsOpenAICompat reports whether the provider speaks OpenAI Chat Completions.
// Everything except Ollama is treated as OpenAI-compatible.
func IsOpenAICompat(p string) bool {
	return !IsOllama(p)
}

// DefaultEndpointFor returns a sensible base URL for well-known providers.
func DefaultEndpointFor(provider string) string {
	switch NormalizeProvider(provider) {
	case "ollama":
		return "http://127.0.0.1:11434"
	case "lmstudio":
		return "http://127.0.0.1:1234/v1"
	case "openai":
		return "https://api.openai.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "together":
		return "https://api.together.xyz/v1"
	case "deepseek":
		return "https://api.deepseek.com"
	case "fireworks":
		return "https://api.fireworks.ai/inference/v1"
	case "mistral":
		return "https://api.mistral.ai/v1"
	case "gemini", "google":
		return "https://generativelanguage.googleapis.com/v1beta/openai/"
	case "anthropic":
		// Prefer an OpenAI-compatible proxy; Anthropic native API needs a gateway.
		return "https://openrouter.ai/api/v1"
	case "vllm", "litellm", "custom":
		return "http://127.0.0.1:8000/v1"
	default: // omlx and unknown local gateways
		return DefaultEndpoint
	}
}

// Backend selects the execution engine.
const (
	BackendSLMCode    = "slmcode"
	BackendClaudeCode = "claude-code"
)

// Mode selects full pipeline vs single specialist.
const (
	ModeFull       = "full"
	ModeSpecialist = "specialist"
)

// Config is persisted in .slmcode/config.yaml and overridable via flags/env.
//
// Precedence, lowest first: built-in defaults → user file → project file →
// SLMCODE_* environment → command-line flags. `Save` writes only the fields
// that differ from the inherited baseline, so the file records intent rather
// than a snapshot of every default (see save.go).
type Config struct {
	// Root is the project directory. It is deliberately NOT persisted: a
	// config.yaml that embeds an absolute path is not portable between
	// machines or checkouts, and Load would honor the stale value.
	Root string `yaml:"-" json:"root"`

	// ConfigVersion is the schema generation this file was written by. Load
	// migrates older files forward (see migrate.go).
	ConfigVersion int `yaml:"config_version,omitempty" json:"config_version,omitempty"`

	// prov records which layer supplied each key, for `config show --origin`.
	// Unexported, so neither YAML nor JSON ever sees it.
	prov *Provenance

	// lastSavedKeys is what SaveInitial last wrote, so `slmcode init` can
	// report the file's real contents instead of a differently-computed count.
	lastSavedKeys []string

	// Provider selects the LLM backend. Built-ins: omlx | ollama | openai |
	// lmstudio | openrouter | vllm | litellm | together | groq | deepseek | …
	// Any other name is treated as an OpenAI-compatible gateway (set endpoint).
	Provider string `yaml:"provider" json:"provider"`
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	APIKey   string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	Model    string `yaml:"model" json:"model"`
	// FastModel optionally specifies a smaller/faster model for lightweight agents
	// (reviewer, coordinator, splitter, planner, context, architect, clarifier).
	// Empty = use the main Model for all agents.
	FastModel string `yaml:"fast_model" json:"fast_model"`

	Backend string `yaml:"backend" json:"backend"` // slmcode | claude-code

	// Mode: full (default pipeline) | specialist (single role)
	Mode string `yaml:"mode" json:"mode"`
	// Specialist role id when Mode=specialist (worker, explorer, …)
	Specialist string `yaml:"specialist" json:"specialist"`
	// DynamicPipeline runs the composer specialist first to assemble a
	// task-specific pipeline (phases, team, tools, skills) before executing.
	DynamicPipeline bool `yaml:"dynamic_pipeline" json:"dynamic_pipeline"`
	// PinnedSkills are always loaded (in addition to @skill: refs / matching).
	PinnedSkills []string `yaml:"pinned_skills" json:"pinned_skills"`

	Temperature  float64       `yaml:"temperature" json:"temperature"`
	MaxTokens    int           `yaml:"max_tokens" json:"max_tokens"`
	MaxRetries   int           `yaml:"max_retries" json:"max_retries"`
	MaxParallel  int           `yaml:"max_parallel" json:"max_parallel"`
	MaxContextKB int           `yaml:"max_context_kb" json:"max_context_kb"`
	ThinkPasses  int           `yaml:"think_passes" json:"think_passes"`
	TaskTimeout  time.Duration `yaml:"task_timeout" json:"task_timeout"`

	// EnabledModels optionally scopes the selectable catalog (empty = all).
	EnabledModels []string `yaml:"enabled_models" json:"enabled_models"`
	// LLMRetryCount / LLMRetryDelayMS are provider HTTP retries (≠ MaxRetries board loop).
	LLMRetryCount   int `yaml:"llm_retry_count" json:"llm_retry_count"`
	LLMRetryDelayMS int `yaml:"llm_retry_delay_ms" json:"llm_retry_delay_ms"`

	// QAGate runs an iterate-until-green test command after the board finishes.
	QAGate          bool   `yaml:"qa_gate" json:"qa_gate"`
	QAGateCommand   string `yaml:"qa_gate_command" json:"qa_gate_command"` // empty = auto-detect
	QAGateMaxRounds int    `yaml:"qa_gate_max_rounds" json:"qa_gate_max_rounds"`
	// PostWorkerSmoke runs deterministic py_compile/go test after each worker
	// before review can approve (prevents broken-on-disk auto-approve).
	PostWorkerSmoke bool `yaml:"post_worker_smoke" json:"post_worker_smoke"`
	// QABootstrap decides whether the QA gate may run dependency installers
	// (`pip install`, `npm install`, `go mod tidy`) against agent-authored
	// manifests: off | ask | auto. "ask" is the default because an agent that
	// invented a requirements.txt should not get an unattended network install.
	QABootstrap string `yaml:"qa_bootstrap" json:"qa_bootstrap"`
	// RegressionChecks replays stored regression checks around the QA gate.
	RegressionChecks bool `yaml:"regression_checks" json:"regression_checks"`
	// DisableSyntaxCheck turns off post-edit syntax verification.
	DisableSyntaxCheck bool `yaml:"disable_syntax_check" json:"disable_syntax_check"`

	// ── Self-improvement ──
	// Evolve enables the self-improvement engine (memory, repair rules,
	// bandit policy, regression checks).
	Evolve bool `yaml:"evolve" json:"evolve"`
	// Deterministic makes the bandit greedy and disables exploration, for CI
	// and reproducible runs. DryRun implies it.
	Deterministic bool `yaml:"deterministic" json:"deterministic"`
	// MemoryTokens is the token budget for the injected memory block.
	MemoryTokens int `yaml:"memory_tokens" json:"memory_tokens"`
	// MaxTaskCalls is the per-task LLM call budget handed to the inner loop.
	MaxTaskCalls int `yaml:"max_task_calls" json:"max_task_calls"`
	// ArchitectEditor enables the describer→editor role pair. Off by default:
	// it doubles the LLM calls per task and only pays off when the two halves
	// point at different models.
	ArchitectEditor bool `yaml:"architect_editor" json:"architect_editor"`

	// ── Tool layer ──
	// ReadWindowLines is the ws_read window size (0 = the tool's own default).
	ReadWindowLines int `yaml:"read_window_lines" json:"read_window_lines"`
	// MaxToolChars is a hard cap on tool result size (0 = tool default).
	MaxToolChars int `yaml:"max_tool_chars" json:"max_tool_chars"`
	// ShellTimeout is the ws_shell per-command timeout (0 = tool default).
	ShellTimeout time.Duration `yaml:"shell_timeout" json:"shell_timeout"`
	// StructuredDecoding selects constrained decoding: auto negotiates the
	// strongest mechanism pkg/backends can confirm; off forces prompt-only JSON.
	StructuredDecoding string `yaml:"structured_decoding" json:"structured_decoding"`

	// ClarifyMode: auto (apply recommended) | ask (pause for user) | off.
	// Inspired by Claude Code AskUserQuestion + pi-clarify.
	ClarifyMode string `yaml:"clarify_mode" json:"clarify_mode"`
	// ClarifyTimeout is how long ask mode waits before applying recommended.
	ClarifyTimeout time.Duration `yaml:"clarify_timeout" json:"clarify_timeout"`
	// ScopeJudge runs a post-split PRD completeness check (heuristic + optional LLM).
	ScopeJudge bool `yaml:"scope_judge" json:"scope_judge"`
	// PlanApprove: off | auto | ask — Claude Code Plan Mode gate before execute.
	PlanApprove string `yaml:"plan_approve" json:"plan_approve"`
	// PlanApproveTimeout for ask mode.
	PlanApproveTimeout time.Duration `yaml:"plan_approve_timeout" json:"plan_approve_timeout"`
	// PlaceholderPass runs a post-execute stub scan + fill/flag specialist.
	PlaceholderPass bool `yaml:"placeholder_pass" json:"placeholder_pass"`
	// ContinueAsk: ask | auto | off — prompt user when retries/QA exhausted.
	ContinueAsk string `yaml:"continue_ask" json:"continue_ask"`
	// ContinueAskTimeout for ask mode (timeout → stop, keep precise flags).
	ContinueAskTimeout time.Duration `yaml:"continue_ask_timeout" json:"continue_ask_timeout"`
	// EscalateAsk: ask | auto | off — pause on max-retry escalate for HITL.
	EscalateAsk string `yaml:"escalate_ask" json:"escalate_ask"`
	// EscalateAskTimeout for ask mode (timeout → @escalate SLM decides).
	// The default is DefaultEscalateAskTimeout (5 minutes), not the 30s this
	// comment used to claim — a human has to notice the prompt, read the task
	// and choose, and every expiry costs an extra LLM call in the arbitrator.
	EscalateAskTimeout time.Duration `yaml:"escalate_ask_timeout" json:"escalate_ask_timeout"`
	// EscalateMaxRetries caps how many times ONE task may be reopened by
	// answering "retry" at the escalate gate before retry is refused and
	// downgraded to re_scope.
	//
	// Without a cap, "retry" is a loop rather than a decision: the reopened
	// task re-enters the same ladder, fails on the same evidence, escalates
	// again, and the timeout arbitrator's own heuristic default is retry. Each
	// granted retry costs a full ladder (up to MaxTaskCalls), so the ceiling
	// one task can spend is (1 + escalate_max_retries) × max_task_calls.
	EscalateMaxRetries int `yaml:"escalate_max_retries" json:"escalate_max_retries"`
	// EscalateTimeoutAgent is the specialist that decides on HITL timeout
	// (empty = auto: escalate → reviewer → coordinator).
	EscalateTimeoutAgent string `yaml:"escalate_timeout_agent" json:"escalate_timeout_agent"`
	// PlanApproveOnTimeout is what an unanswered plan gate does:
	// approve | reject | auto. "auto" approves only when no event subscriber
	// was attached — i.e. when there was no UI that could have answered.
	PlanApproveOnTimeout string `yaml:"plan_approve_on_timeout" json:"plan_approve_on_timeout"`

	DryRun  bool `yaml:"dry_run" json:"dry_run"`
	Verbose bool `yaml:"verbose" json:"verbose"`
	// AutoApprove skips plan/shell/clarify HITL waits (forces recommended/allow).
	AutoApprove bool `yaml:"auto_approve" json:"auto_approve"`

	// Permission: auto | dry-run | review (Claude Code–style write policy)
	Permission string `yaml:"permission" json:"permission"`
	// ShellPermission: allow | ask | deny (ws_shell policy; independent of file writes)
	ShellPermission string `yaml:"shell_permission" json:"shell_permission"`
	// ShellWhitelist enforces SAFE_PREFIXES on ws_shell (little-coder permission-gate).
	ShellWhitelist bool `yaml:"shell_whitelist" json:"shell_whitelist"`
	// ShellAllow adds extra SAFE_PREFIXES (also SLMCODE_BASH_ALLOW env).
	ShellAllow []string `yaml:"shell_allow" json:"shell_allow"`
	// ShellAskTimeout for interactive shell approval when shell_permission=ask.
	ShellAskTimeout time.Duration `yaml:"shell_ask_timeout" json:"shell_ask_timeout"`
	// CompactMode trims live event verbosity in TUI/CLI.
	CompactMode bool `yaml:"compact_mode" json:"compact_mode"`
	// ContextCompact enables mid-run CONTEXT.md summarization when oversized.
	ContextCompact bool `yaml:"context_compact" json:"context_compact"`
	// ContextCompactEngine: heuristic | llm | auto (LLM with heuristic fallback).
	ContextCompactEngine string `yaml:"context_compact_engine" json:"context_compact_engine"`
	// ReactCompact enables ReAct conversation compaction at CHECKPOINT and
	// RESUME — it does NOT compact a live agent call between its iterations.
	// loop.LiveReactCompactionWired is the constant of record (it is false, and
	// nothing an operator sets changes it); loop.ReactCompactionStatus renders
	// the honest one-line claim. This setting says what was asked for, not what
	// the harness does.
	ReactCompact bool `yaml:"react_compact" json:"react_compact"`
	// ReactCompactAtPercent triggers ReAct compaction at this % of MaxContextKB
	// (little-coder default 80). <=0 or >=100 disables.
	ReactCompactAtPercent int `yaml:"react_compact_at_percent" json:"react_compact_at_percent"`
	// SessionEventLog writes .slmcode/queries/<id>/events.jsonl during runs.
	SessionEventLog bool `yaml:"session_event_log" json:"session_event_log"`
	// AutoRefine appends refine notes from wave lessons into CONTEXT.
	AutoRefine bool `yaml:"auto_refine" json:"auto_refine"`
	// AutoRefineMaxRounds caps refine passes per run (default 2).
	AutoRefineMaxRounds int `yaml:"auto_refine_max_rounds" json:"auto_refine_max_rounds"`
	// WaveSnapshots stores per-wave file rewind points under .slmcode/waves/.
	WaveSnapshots bool `yaml:"wave_snapshots" json:"wave_snapshots"`
	// FileCheckpoints snapshots each file before first write/edit (first-write-wins).
	FileCheckpoints bool `yaml:"file_checkpoints" json:"file_checkpoints"`
	// HooksEnabled loads .slmcode/hooks.json Pre/PostToolUse.
	//
	// Defaults to FALSE. hooks.json is a repository-supplied file that makes
	// the harness run shell commands on every tool call; pkg/hooks.Load now
	// fails closed on an unapproved file, so a `true` default was harmless in
	// practice, but it advertised the wrong intent in every generated config.
	// Opting in is the operator's decision, and it takes an explicit
	// `hooks_enabled: true` plus `slmcode hooks trust`.
	HooksEnabled bool `yaml:"hooks_enabled" json:"hooks_enabled"`

	// SLM harness invariants (little-coder ports). Defaults ON.
	WriteGuard      bool `yaml:"write_guard" json:"write_guard"`             // ws_write refuses existing files
	ReadBeforeEdit  bool `yaml:"read_before_edit" json:"read_before_edit"`   // edit/patch require prior read
	ShellWriteGuard bool `yaml:"shell_write_guard" json:"shell_write_guard"` // block cat>/tee clobber
	ToolGuidance    bool `yaml:"tool_guidance" json:"tool_guidance"`         // per-turn tool skill cards
	KnowledgeInject bool `yaml:"knowledge_inject" json:"knowledge_inject"`   // keyword knowledge cards
	QualityMonitor  bool `yaml:"quality_monitor" json:"quality_monitor"`     // empty/loop/hallucinated tool nudge
	StaticQuality   bool `yaml:"static_quality" json:"static_quality"`       // reject stub/placeholder code
	ThinkingBudget  bool `yaml:"thinking_budget" json:"thinking_budget"`     // commit-to-implementation nudge
	// ThinkingBudgetTokens hard-abort threshold for over-long deliberation (0=4096).
	ThinkingBudgetTokens int  `yaml:"thinking_budget_tokens" json:"thinking_budget_tokens"`
	FinalizeWarn         bool `yaml:"finalize_warn" json:"finalize_warn"`     // warn before MaxIter exhaustion
	RequireSmoke         bool `yaml:"require_smoke" json:"require_smoke"`     // coding tasks need smoke for approve
	ClaimsGate           bool `yaml:"claims_gate" json:"claims_gate"`         // reject hallucinated files_changed
	WorkerCritique       bool `yaml:"worker_critique" json:"worker_critique"` // auto self-fix pass on weak worker output
	OverEditGuard        bool `yaml:"over_edit_guard" json:"over_edit_guard"` // refuse whole-file-style edits
	ReadHeadLines        int  `yaml:"read_head_lines" json:"read_head_lines"` // auto-trim read head (default 80)
	// AutoTextTools strengthens corrector recovery for prose-embedded tool JSON.
	AutoTextTools bool `yaml:"auto_text_tools" json:"auto_text_tools"`

	// ModelProfiles overrides skill/knowledge/thinking budgets by model id.
	ModelProfiles map[string]ModelProfile `yaml:"model_profiles" json:"model_profiles"`

	// ActiveStack is the last applied stacks/<id>.yaml preset (UI highlight + docs).
	// Empty means the user configured provider/model manually.
	ActiveStack string `yaml:"active_stack,omitempty" json:"active_stack,omitempty"`

	// ActivePack is the last applied language/domain building-block pack (go|python|react|…).
	ActivePack string `yaml:"active_pack,omitempty" json:"active_pack,omitempty"`
	// ActivePipeline is the last applied named pipeline block id (may match pack pipeline).
	ActivePipeline string `yaml:"active_pipeline,omitempty" json:"active_pipeline,omitempty"`

	// MCPServers are thin MCP connections (stdio or HTTP). They are read-only
	// unless a server sets `read_only: false`, which the orchestrator now
	// honors (see mcpServerConfigs) — it used to be inert.
	MCPServers []MCPServerConfig `yaml:"mcp_servers" json:"mcp_servers"`

	// ── Context engineering ──
	// RepoMapTokens is the ranked repo-symbol map's share of a context pack.
	// 0 disables the map entirely.
	RepoMapTokens int `yaml:"repo_map_tokens" json:"repo_map_tokens"`
	// ExcerptWindowLines is the ± lines kept around each relevance match when
	// a file is excerpted instead of inlined whole.
	ExcerptWindowLines int `yaml:"excerpt_window_lines" json:"excerpt_window_lines"`
	// SkillDisclosure selects progressive skill disclosure:
	// auto (cards + earned bodies) | cards (never inline a body) | full.
	SkillDisclosure string `yaml:"skill_disclosure" json:"skill_disclosure"`
	// SkillMaxExpanded caps how many skill bodies are inlined at once.
	SkillMaxExpanded int `yaml:"skill_max_expanded" json:"skill_max_expanded"`
	// ContextReserveSystemTokens / ContextReserveToolTokens /
	// ContextReserveResponseTokens are subtracted from the model window before
	// a context pack gets its share; ContextSlackPercent covers tokenizer
	// disagreement and chat scaffolding.
	ContextReserveSystemTokens   int `yaml:"context_reserve_system_tokens" json:"context_reserve_system_tokens"`
	ContextReserveToolTokens     int `yaml:"context_reserve_tool_tokens" json:"context_reserve_tool_tokens"`
	ContextReserveResponseTokens int `yaml:"context_reserve_response_tokens" json:"context_reserve_response_tokens"`
	ContextSlackPercent          int `yaml:"context_slack_percent" json:"context_slack_percent"`
	// ContextRoleBudget overrides the per-role share of the available window,
	// as whole percentages keyed by role id ("worker: 100", "explorer: 60").
	// Empty uses the built-in table.
	ContextRoleBudget map[string]int `yaml:"context_role_budget,omitempty" json:"context_role_budget,omitempty"`
	// RetrievalMinScore overrides the calibrated similarity floor for
	// retrieved knowledge. 0 keeps the measured per-embedder default; raising
	// it injects less but more relevant material.
	RetrievalMinScore float64 `yaml:"retrieval_min_score" json:"retrieval_min_score"`
	// RetrievalCacheDir holds the on-disk embedding cache. Empty = .slmcode.
	RetrievalCacheDir string `yaml:"retrieval_cache_dir,omitempty" json:"retrieval_cache_dir,omitempty"`

	// Embedding retrieval for CONTEXT injection (OpenAI-compat /v1/embeddings).
	// When disabled or unreachable, lexical TF-IDF ranking is used.
	EmbeddingEnabled  bool   `yaml:"embedding_enabled" json:"embedding_enabled"`
	EmbeddingEndpoint string `yaml:"embedding_endpoint" json:"embedding_endpoint"`
	EmbeddingModel    string `yaml:"embedding_model" json:"embedding_model"`
	EmbeddingAPIKey   string `yaml:"embedding_api_key,omitempty" json:"embedding_api_key,omitempty"`
	EmbeddingTopK     int    `yaml:"embedding_top_k" json:"embedding_top_k"`

	// Optional $/MTok rates for estimated cost in /stats (omit to report tokens only).
	// price_preset: ""|off|local|omlx|openai|anthropic|openrouter — ballpark rates;
	// explicit price_*_per_mtok always win. Unknown models never invent $ without preset/config.
	PricePreset            string  `yaml:"price_preset" json:"price_preset"`
	PricePromptPerMTok     float64 `yaml:"price_prompt_per_mtok" json:"price_prompt_per_mtok"`
	PriceCompletionPerMTok float64 `yaml:"price_completion_per_mtok" json:"price_completion_per_mtok"`

	// SkillsDirs are extra skill roots (in addition to bundled + .slmcode/skills).
	SkillsDirs []string `yaml:"skills_dirs" json:"skills_dirs"`

	// Server
	Listen string `yaml:"listen" json:"listen"`

	// ClaudeCodeBin when backend=claude-code
	ClaudeCodeBin string `yaml:"claude_code_bin" json:"claude_code_bin"`
}

func Default(root string) *Config {
	if root == "" {
		root, _ = os.Getwd()
	}
	return &Config{
		Root:          root,
		ConfigVersion: CurrentConfigVersion,
		Provider:      DefaultProvider,
		Endpoint:      DefaultEndpoint,
		Model:         DefaultModel,
		Backend:       BackendSLMCode,
		Mode:          ModeFull,
		// Dynamic pipeline is on by default: the composer assembles a
		// task-specific pipeline (phases, team, tools, skills) per run. Disable
		// via config `dynamic_pipeline: false` or `slmcode run --no-dynamic`.
		DynamicPipeline:      true,
		Temperature:          0.2,
		MaxTokens:            4096,
		MaxRetries:           DefaultMaxRetries,
		MaxParallel:          DefaultMaxParallel,
		MaxContextKB:         DefaultMaxContextKB,
		ThinkPasses:          DefaultThinkPasses,
		TaskTimeout:          DefaultTaskTimeout,
		LLMRetryCount:        3,
		LLMRetryDelayMS:      1000,
		ContextCompactEngine: "heuristic",
		SessionEventLog:      true,
		AutoRefine:           false,
		AutoRefineMaxRounds:  2,
		QAGate:               true,
		QAGateMaxRounds:      DefaultQAGateRounds,
		PostWorkerSmoke:      true,
		ClarifyMode:          "ask",
		ClarifyTimeout:       DefaultClarifyTimeout,
		ScopeJudge:           true,
		PlanApprove:          "ask",
		PlanApproveTimeout:   DefaultPlanApproveTimeout,
		PlaceholderPass:      true,
		ContinueAsk:          "ask",
		ContinueAskTimeout:   DefaultContinueAskTimeout,
		EscalateAsk:          "ask",
		EscalateAskTimeout:   DefaultEscalateAskTimeout,
		EscalateMaxRetries:   DefaultEscalateMaxRetries,
		EscalateTimeoutAgent: "", // auto-pick @escalate
		PlanApproveOnTimeout: DefaultPlanApproveOnTimeout,
		QABootstrap:          DefaultQABootstrap,
		RegressionChecks:     true,
		DisableSyntaxCheck:   false,
		Evolve:               true,
		Deterministic:        false,
		MemoryTokens:         DefaultMemoryTokens,
		MaxTaskCalls:         DefaultMaxTaskCalls,
		ArchitectEditor:      false,
		ReadWindowLines:      0,
		MaxToolChars:         0,
		ShellTimeout:         0,
		StructuredDecoding:   DefaultStructuredDecoding,

		RepoMapTokens:                DefaultRepoMapTokens,
		ExcerptWindowLines:           DefaultExcerptWindowLines,
		SkillDisclosure:              DefaultSkillDisclosure,
		SkillMaxExpanded:             DefaultSkillMaxExpanded,
		ContextReserveSystemTokens:   DefaultReserveSystemTokens,
		ContextReserveToolTokens:     DefaultReserveToolTokens,
		ContextReserveResponseTokens: DefaultReserveResponseTok,
		ContextSlackPercent:          DefaultContextSlackPercent,
		RetrievalMinScore:            0,

		Listen:                "127.0.0.1:7420",
		ClaudeCodeBin:         "claude",
		Permission:            "auto",
		ShellPermission:       "allow",
		ShellWhitelist:        true,
		ShellAskTimeout:       DefaultShellAskTimeout,
		CompactMode:           true,
		ContextCompact:        true,
		ReactCompact:          true,
		ReactCompactAtPercent: 80,
		WaveSnapshots:         true,
		FileCheckpoints:       true,
		WriteGuard:            true,
		ReadBeforeEdit:        true,
		ShellWriteGuard:       true,
		ToolGuidance:          true,
		KnowledgeInject:       true,
		QualityMonitor:        true,
		StaticQuality:         true,
		ThinkingBudget:        true,
		ThinkingBudgetTokens:  4096,
		FinalizeWarn:          true,
		RequireSmoke:          true,
		ClaimsGate:            true,
		WorkerCritique:        true,
		OverEditGuard:         true,
		ReadHeadLines:         80,
		EmbeddingTopK:         5,
		ModelProfiles:         DefaultModelProfiles(),
	}
}

func (c *Config) SlmDir() string     { return filepath.Join(c.Root, DirName) }
func (c *Config) ConfigPath() string { return filepath.Join(c.SlmDir(), "config.yaml") }
func (c *Config) SkillsDir() string  { return filepath.Join(c.SlmDir(), "skills") }
func (c *Config) AgentsDir() string  { return filepath.Join(c.SlmDir(), "agents") }

// ResolveAPIKey fills API key from env or provider-specific stores.
func (c *Config) ResolveAPIKey() {
	if c.APIKey != "" {
		return
	}
	if v := os.Getenv("SLMCODE_API_KEY"); v != "" {
		c.APIKey = v
		return
	}
	// .slmcode/auth.json (prime-agent auth.json style) — before provider env.
	if key, ok := authstore.Get(c.SlmDir(), c.Provider); ok {
		c.APIKey = key
		return
	}
	p := NormalizeProvider(c.Provider)
	switch p {
	case "omlx":
		if v := os.Getenv("OMLX_API_KEY"); v != "" {
			c.APIKey = v
			return
		}
		if k := readOmlxAPIKey(); k != "" {
			c.APIKey = k
		}
	case "openai":
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "openrouter":
		if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "groq":
		if v := os.Getenv("GROQ_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "together":
		if v := os.Getenv("TOGETHER_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "deepseek":
		if v := os.Getenv("DEEPSEEK_API_KEY"); v != "" {
			c.APIKey = v
		}
	case "gemini", "google":
		if v := os.Getenv("GOOGLE_API_KEY"); v != "" {
			c.APIKey = v
			return
		}
		if v := os.Getenv("GEMINI_API_KEY"); v != "" {
			c.APIKey = v
		}
	default:
		// Generic OpenAI-compat gateways often reuse OPENAI_API_KEY.
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			c.APIKey = v
			return
		}
		if v := os.Getenv("OMLX_API_KEY"); v != "" {
			c.APIKey = v
		}
	}
}

// EnvVarFor returns the environment variable that overrides a config key.
// Every key gets one, mechanically: SLMCODE_ + the upper-cased key.
func EnvVarFor(key string) string {
	return "SLMCODE_" + strings.ToUpper(strings.ReplaceAll(CanonicalKey(key), "-", "_"))
}

// ApplyEnv overlays SLMCODE_* / OPENAI_BASE_URL env vars onto the config.
// Call after Load (or Default) and before flag overrides.
//
// Every scalar config key is overridable as SLMCODE_<KEY>, so a knob that
// exists in config.yaml is also reachable from a CI job or a one-off shell
// without editing a file. Invalid values are ignored with a warning rather
// than failing the run: an environment is not something the user is editing
// with the schema in front of them.
func (c *Config) ApplyEnv() {
	prov := c.Provenance()
	for _, key := range Keys() {
		if key == "config_version" || key == "api_key" {
			continue // api_key has its own resolution chain below
		}
		switch t, _ := KindOf(key); t {
		case "map", "list", "object":
			continue // not expressible as a single env value
		}
		name := EnvVarFor(key)
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			continue
		}
		if err := c.Set(key, v); err != nil {
			prov.Warnings = append(prov.Warnings, name+": "+err.Error())
			continue
		}
		prov.Mark(key, LayerEnv, name)
	}
	// Legacy / cross-tool spellings that do not follow the mechanical rule.
	if os.Getenv("SLMCODE_ENDPOINT") == "" {
		if v := os.Getenv("OPENAI_BASE_URL"); v != "" && IsOpenAICompat(c.Provider) {
			c.Endpoint = v
			prov.Mark("endpoint", LayerEnv, "OPENAI_BASE_URL")
		}
	}
	// Naming an embedding endpoint or model implies enabling retrieval — the
	// generic loop above cannot express that side effect.
	if v := os.Getenv("SLMCODE_EMBEDDING_ENDPOINT"); v != "" {
		c.EmbeddingEnabled = true
		prov.Mark("embedding_enabled", LayerEnv, "SLMCODE_EMBEDDING_ENDPOINT")
	}
	if v := os.Getenv("SLMCODE_EMBEDDING_MODEL"); v != "" {
		c.EmbeddingEnabled = true
		prov.Mark("embedding_enabled", LayerEnv, "SLMCODE_EMBEDDING_MODEL")
	}
	normalize(c)
	c.ResolveAPIKey()
}

func readOmlxAPIKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".omlx", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// Support common key spellings across oMLX versions.
	var s struct {
		APIKey string `json:"api_key"`
		Auth   struct {
			APIKey    string `json:"api_key"`
			APIKeyAlt string `json:"apiKey"`
		} `json:"auth"`
	}
	if json.Unmarshal(data, &s) != nil {
		return ""
	}
	if k := strings.TrimSpace(s.Auth.APIKey); k != "" {
		return k
	}
	if k := strings.TrimSpace(s.Auth.APIKeyAlt); k != "" {
		return k
	}
	return strings.TrimSpace(s.APIKey)
}

// Load resolves the full precedence chain for a project root:
//
//	built-in defaults → user file → project file → SLMCODE_* environment
//
// Flags are the caller's job (see Config.MarkFlag). Every layer's contribution
// is recorded in Provenance so `slmcode config show --origin` can attribute
// each effective value, and the project file is migrated forward on the way in.
func Load(root string) (*Config, error) {
	cfg := Default(root)
	prov := cfg.Provenance()

	// ── user layer ──
	cfg.LoadUserLayer()
	normalize(cfg)
	// Everything up to here is what a project file inherits; Save diffs
	// against it so an inherited value is never copied into a project.
	baseline := cfg.clone()
	// Snapshot the user layer's MCP servers: they are the ONLY ones that may
	// be spawned. See mcptrust.go.
	userMCP := append([]MCPServerConfig(nil), cfg.MCPServers...)

	// ── project layer ──
	raw, fromBackup, err := readDocument(cfg.ConfigPath())
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		cfg.Root = root
		normalize(cfg)
		cfg.ApplyEnv()
		prov.baseline = baseline
		return cfg, nil
	}
	if raw == nil {
		if fromBackup {
			return nil, fmt.Errorf("parse config backup: %s", cfg.ConfigPath())
		}
		return nil, fmt.Errorf("parse config: %s", cfg.ConfigPath())
	}
	prov.ProjectPath = cfg.ConfigPath()
	prov.FromVersion = migrate(raw)
	prov.Migrated = prov.FromVersion < CurrentConfigVersion
	applied, errs := cfg.ApplyValues(raw)
	for _, e := range errs {
		// A single bad key must not make the workspace unopenable — the CLI
		// surfaces these as warnings and the rest of the file still applies.
		prov.Warnings = append(prov.Warnings, prov.ProjectPath+": "+e.Error())
	}
	for _, k := range applied {
		prov.Mark(k, LayerProject, "")
	}
	// A repository must not be able to make the harness spawn processes.
	if w := dropProjectMCPServers(cfg, userMCP, raw, prov.ProjectPath); w != "" {
		prov.Warnings = append(prov.Warnings, w)
		prov.clearProjectMark("mcp_servers")
	}

	cfg.Root = root
	normalize(cfg)
	cfg.ApplyEnv()
	prov.baseline = baseline
	return cfg, nil
}

// clone returns a deep-enough copy for baseline comparison: every scalar plus
// fresh copies of the composite fields Save inspects.
func (c *Config) clone() *Config {
	if c == nil {
		return nil
	}
	out := *c
	out.prov = nil
	if c.ModelProfiles != nil {
		out.ModelProfiles = make(map[string]ModelProfile, len(c.ModelProfiles))
		for k, v := range c.ModelProfiles {
			out.ModelProfiles[k] = v
		}
	}
	if c.ContextRoleBudget != nil {
		out.ContextRoleBudget = make(map[string]int, len(c.ContextRoleBudget))
		for k, v := range c.ContextRoleBudget {
			out.ContextRoleBudget[k] = v
		}
	}
	out.PinnedSkills = append([]string(nil), c.PinnedSkills...)
	out.EnabledModels = append([]string(nil), c.EnabledModels...)
	out.SkillsDirs = append([]string(nil), c.SkillsDirs...)
	out.ShellAllow = append([]string(nil), c.ShellAllow...)
	out.MCPServers = append([]MCPServerConfig(nil), c.MCPServers...)
	return &out
}

func readConfigBytes(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var probe Config
		if yaml.Unmarshal(data, &probe) == nil {
			return data, false, nil
		}
		if backup, bakErr := os.ReadFile(atomicfile.BackupPath(path)); bakErr == nil {
			return backup, true, nil
		}
		return data, false, nil
	}
	return nil, false, err
}

func normalize(c *Config) {
	c.Provider = NormalizeProvider(c.Provider)
	if c.Endpoint == "" {
		c.Endpoint = DefaultEndpointFor(c.Provider)
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.Backend == "" {
		c.Backend = BackendSLMCode
	}
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case "", ModeFull:
		c.Mode = ModeFull
	case ModeSpecialist, "single", "agent":
		c.Mode = ModeSpecialist
	default:
		c.Mode = ModeFull
	}
	c.Specialist = strings.TrimSpace(c.Specialist)
	// MaxRetries=0 means no review retries (one attempt). Only negative is invalid.
	if c.MaxRetries < 0 {
		c.MaxRetries = DefaultMaxRetries
	}
	if c.MaxParallel <= 0 {
		c.MaxParallel = DefaultMaxParallel
	}
	if c.ThinkPasses <= 0 {
		c.ThinkPasses = DefaultThinkPasses
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = 4096
	}
	if c.MaxContextKB <= 0 {
		c.MaxContextKB = DefaultMaxContextKB
	}
	if c.TaskTimeout <= 0 {
		c.TaskTimeout = DefaultTaskTimeout
	}
	if c.QAGateMaxRounds <= 0 {
		c.QAGateMaxRounds = DefaultQAGateRounds
	}
	c.ClarifyMode = NormalizeClarifyMode(c.ClarifyMode)
	if c.ClarifyTimeout <= 0 {
		c.ClarifyTimeout = DefaultClarifyTimeout
	}
	c.PlanApprove = NormalizePlanApprove(c.PlanApprove)
	if c.PlanApproveTimeout <= 0 {
		c.PlanApproveTimeout = DefaultPlanApproveTimeout
	}
	c.ContinueAsk = NormalizeContinueAsk(c.ContinueAsk)
	if c.ContinueAskTimeout <= 0 {
		c.ContinueAskTimeout = DefaultContinueAskTimeout
	}
	c.EscalateAsk = NormalizeEscalateAsk(c.EscalateAsk)
	if c.EscalateAskTimeout <= 0 {
		c.EscalateAskTimeout = DefaultEscalateAskTimeout
	}
	if c.EscalateMaxRetries < 0 {
		c.EscalateMaxRetries = 0
	}
	if c.EscalateMaxRetries == 0 {
		c.EscalateMaxRetries = DefaultEscalateMaxRetries
	}
	if c.ShellAskTimeout <= 0 {
		c.ShellAskTimeout = DefaultShellAskTimeout
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:7420"
	}
	if c.ClaudeCodeBin == "" {
		c.ClaudeCodeBin = "claude"
	}
	c.Permission = permissions.Normalize(c.Permission)
	if c.DryRun {
		c.Permission = permissions.ModeDryRun
	} else if c.Permission == permissions.ModeDryRun {
		c.DryRun = true
	}
	c.ShellPermission = permissions.NormalizeShell(c.ShellPermission)
	if c.EmbeddingTopK <= 0 {
		c.EmbeddingTopK = 5
	}
	if c.ReadHeadLines <= 0 {
		c.ReadHeadLines = 80
	}
	if c.ReactCompactAtPercent < 0 {
		c.ReactCompactAtPercent = 0
	}
	if c.ReactCompactAtPercent > 100 {
		c.ReactCompactAtPercent = 100
	}
	switch strings.ToLower(strings.TrimSpace(c.ContextCompactEngine)) {
	case "llm", "auto", "heuristic":
		c.ContextCompactEngine = strings.ToLower(strings.TrimSpace(c.ContextCompactEngine))
	default:
		c.ContextCompactEngine = "heuristic"
	}
	if c.LLMRetryCount < 0 {
		c.LLMRetryCount = 0
	}
	// LLMRetryCount/LLMRetryDelayMS of 0 mean "no provider retries" and are
	// preserved as written; Default() sets 3/1000.
	if c.LLMRetryDelayMS < 0 {
		c.LLMRetryDelayMS = 0
	}
	if c.AutoRefineMaxRounds <= 0 {
		c.AutoRefineMaxRounds = 2
	}
	if c.ThinkingBudgetTokens < 0 {
		c.ThinkingBudgetTokens = 0
	}
	if c.ModelProfiles == nil {
		c.ModelProfiles = DefaultModelProfiles()
	} else {
		// Ensure default bucket exists for merges.
		defs := DefaultModelProfiles()
		if _, ok := c.ModelProfiles["default"]; !ok {
			c.ModelProfiles["default"] = defs["default"]
		}
	}
	c.PlanApproveOnTimeout = NormalizePlanApproveOnTimeout(c.PlanApproveOnTimeout)
	c.QABootstrap = NormalizeQABootstrap(c.QABootstrap)
	c.StructuredDecoding = NormalizeStructuredDecoding(c.StructuredDecoding)
	c.SkillDisclosure = NormalizeSkillDisclosure(c.SkillDisclosure)
	if c.MemoryTokens <= 0 {
		c.MemoryTokens = DefaultMemoryTokens
	}
	if c.MaxTaskCalls <= 0 {
		c.MaxTaskCalls = DefaultMaxTaskCalls
	}
	// 0 means "use the tool's own default" for these three, so only a
	// nonsensical negative is corrected.
	if c.ReadWindowLines < 0 {
		c.ReadWindowLines = 0
	}
	if c.MaxToolChars < 0 {
		c.MaxToolChars = 0
	}
	if c.ShellTimeout < 0 {
		c.ShellTimeout = 0
	}
	// 0 disables the repo map deliberately; negative is meaningless.
	if c.RepoMapTokens < 0 {
		c.RepoMapTokens = 0
	}
	if c.ExcerptWindowLines <= 0 {
		c.ExcerptWindowLines = DefaultExcerptWindowLines
	}
	if c.SkillMaxExpanded < 0 {
		c.SkillMaxExpanded = 0
	}
	if c.ContextReserveSystemTokens <= 0 {
		c.ContextReserveSystemTokens = DefaultReserveSystemTokens
	}
	if c.ContextReserveToolTokens <= 0 {
		c.ContextReserveToolTokens = DefaultReserveToolTokens
	}
	if c.ContextReserveResponseTokens <= 0 {
		c.ContextReserveResponseTokens = DefaultReserveResponseTok
	}
	if c.ContextSlackPercent <= 0 || c.ContextSlackPercent >= 100 {
		c.ContextSlackPercent = DefaultContextSlackPercent
	}
	for role, pct := range c.ContextRoleBudget {
		switch {
		case pct <= 0:
			delete(c.ContextRoleBudget, role)
		case pct > 100:
			c.ContextRoleBudget[role] = 100
		}
	}
	if c.RetrievalMinScore < 0 {
		c.RetrievalMinScore = 0
	} else if c.RetrievalMinScore > 1 {
		c.RetrievalMinScore = 1
	}
	// Default embedding endpoint to chat endpoint when enabled without explicit URL.
	if c.EmbeddingEnabled && c.EmbeddingEndpoint == "" {
		c.EmbeddingEndpoint = c.Endpoint
	}
	if c.EmbeddingAPIKey == "" {
		c.EmbeddingAPIKey = c.APIKey
	}
}

// Patch is a partial config update. Nil fields are left unchanged.
type Patch struct {
	Model                  *string                  `json:"model,omitempty"`
	FastModel              *string                  `json:"fast_model,omitempty"`
	Provider               *string                  `json:"provider,omitempty"`
	Endpoint               *string                  `json:"endpoint,omitempty"`
	APIKey                 *string                  `json:"api_key,omitempty"`
	Backend                *string                  `json:"backend,omitempty"`
	Mode                   *string                  `json:"mode,omitempty"`
	Specialist             *string                  `json:"specialist,omitempty"`
	DynamicPipeline        *bool                    `json:"dynamic_pipeline,omitempty"`
	PinnedSkills           *[]string                `json:"pinned_skills,omitempty"`
	Temperature            *float64                 `json:"temperature,omitempty"`
	MaxTokens              *int                     `json:"max_tokens,omitempty"`
	ThinkPasses            *int                     `json:"think_passes,omitempty"`
	MaxParallel            *int                     `json:"max_parallel,omitempty"`
	MaxRetries             *int                     `json:"max_retries,omitempty"`
	MaxContextKB           *int                     `json:"max_context_kb,omitempty"`
	ActiveStack            *string                  `json:"active_stack,omitempty"`
	ActivePack             *string                  `json:"active_pack,omitempty"`
	ActivePipeline         *string                  `json:"active_pipeline,omitempty"`
	ModelProfiles          *map[string]ModelProfile `json:"model_profiles,omitempty"`
	QAGate                 *bool                    `json:"qa_gate,omitempty"`
	QAGateCommand          *string                  `json:"qa_gate_command,omitempty"`
	QAGateMaxRounds        *int                     `json:"qa_gate_max_rounds,omitempty"`
	PostWorkerSmoke        *bool                    `json:"post_worker_smoke,omitempty"`
	ClarifyMode            *string                  `json:"clarify_mode,omitempty"`
	ClarifyTimeoutSec      *int                     `json:"clarify_timeout_sec,omitempty"`
	ScopeJudge             *bool                    `json:"scope_judge,omitempty"`
	PlanApprove            *string                  `json:"plan_approve,omitempty"`
	PlanApproveTimeoutSec  *int                     `json:"plan_approve_timeout_sec,omitempty"`
	PlaceholderPass        *bool                    `json:"placeholder_pass,omitempty"`
	ContinueAsk            *string                  `json:"continue_ask,omitempty"`
	ContinueAskTimeoutSec  *int                     `json:"continue_ask_timeout_sec,omitempty"`
	EscalateAsk            *string                  `json:"escalate_ask,omitempty"`
	EscalateAskTimeoutSec  *int                     `json:"escalate_ask_timeout_sec,omitempty"`
	EscalateTimeoutAgent   *string                  `json:"escalate_timeout_agent,omitempty"`
	EscalateMaxRetries     *int                     `json:"escalate_max_retries,omitempty"`
	AutoApprove            *bool                    `json:"auto_approve,omitempty"`
	ShellPermission        *string                  `json:"shell_permission,omitempty"`
	ShellWhitelist         *bool                    `json:"shell_whitelist,omitempty"`
	ShellAskTimeoutSec     *int                     `json:"shell_ask_timeout_sec,omitempty"`
	ContextCompact         *bool                    `json:"context_compact,omitempty"`
	WaveSnapshots          *bool                    `json:"wave_snapshots,omitempty"`
	FileCheckpoints        *bool                    `json:"file_checkpoints,omitempty"`
	HooksEnabled           *bool                    `json:"hooks_enabled,omitempty"`
	WriteGuard             *bool                    `json:"write_guard,omitempty"`
	ReadBeforeEdit         *bool                    `json:"read_before_edit,omitempty"`
	ShellWriteGuard        *bool                    `json:"shell_write_guard,omitempty"`
	ToolGuidance           *bool                    `json:"tool_guidance,omitempty"`
	KnowledgeInject        *bool                    `json:"knowledge_inject,omitempty"`
	QualityMonitor         *bool                    `json:"quality_monitor,omitempty"`
	StaticQuality          *bool                    `json:"static_quality,omitempty"`
	ThinkingBudget         *bool                    `json:"thinking_budget,omitempty"`
	ThinkingBudgetTokens   *int                     `json:"thinking_budget_tokens,omitempty"`
	FinalizeWarn           *bool                    `json:"finalize_warn,omitempty"`
	RequireSmoke           *bool                    `json:"require_smoke,omitempty"`
	ClaimsGate             *bool                    `json:"claims_gate,omitempty"`
	WorkerCritique         *bool                    `json:"worker_critique,omitempty"`
	OverEditGuard          *bool                    `json:"over_edit_guard,omitempty"`
	ReadHeadLines          *int                     `json:"read_head_lines,omitempty"`
	AutoTextTools          *bool                    `json:"auto_text_tools,omitempty"`
	DryRun                 *bool                    `json:"dry_run,omitempty"`
	Verbose                *bool                    `json:"verbose,omitempty"`
	Permission             *string                  `json:"permission,omitempty"`
	CompactMode            *bool                    `json:"compact_mode,omitempty"`
	Listen                 *string                  `json:"listen,omitempty"`
	EmbeddingEnabled       *bool                    `json:"embedding_enabled,omitempty"`
	EmbeddingEndpoint      *string                  `json:"embedding_endpoint,omitempty"`
	EmbeddingModel         *string                  `json:"embedding_model,omitempty"`
	EmbeddingAPIKey        *string                  `json:"embedding_api_key,omitempty"`
	EmbeddingTopK          *int                     `json:"embedding_top_k,omitempty"`
	PricePreset            *string                  `json:"price_preset,omitempty"`
	PricePromptPerMTok     *float64                 `json:"price_prompt_per_mtok,omitempty"`
	PriceCompletionPerMTok *float64                 `json:"price_completion_per_mtok,omitempty"`
	EnabledModels          *[]string                `json:"enabled_models,omitempty"`
	LLMRetryCount          *int                     `json:"llm_retry_count,omitempty"`
	LLMRetryDelayMS        *int                     `json:"llm_retry_delay_ms,omitempty"`
	ContextCompactEngine   *string                  `json:"context_compact_engine,omitempty"`
	ReactCompact           *bool                    `json:"react_compact,omitempty"`
	ReactCompactAtPercent  *int                     `json:"react_compact_at_percent,omitempty"`
	SessionEventLog        *bool                    `json:"session_event_log,omitempty"`
	AutoRefine             *bool                    `json:"auto_refine,omitempty"`
	AutoRefineMaxRounds    *int                     `json:"auto_refine_max_rounds,omitempty"`

	// extra holds recognized config keys with no typed field above. It is
	// populated by Patch.UnmarshalJSON and applied by ApplyPatch, so a newly
	// added config field is reachable from Studio and the TUI immediately.
	extra map[string]any
}

// ApplyPatch merges a partial update and re-normalizes permission/dry-run.
func (c *Config) ApplyPatch(p Patch) {
	if p.Model != nil && *p.Model != "" {
		c.Model = *p.Model
	}
	if p.FastModel != nil {
		c.FastModel = strings.TrimSpace(*p.FastModel)
	}
	prevProvider := NormalizeProvider(c.Provider)
	prevDefaultEP := DefaultEndpointFor(prevProvider)
	providerChanged := false
	if p.Provider != nil && *p.Provider != "" {
		next := NormalizeProvider(*p.Provider)
		if next != prevProvider {
			providerChanged = true
		}
		c.Provider = next
	}
	if providerChanged {
		// Refresh endpoint unless the caller sent a non-default override.
		explicit := p.Endpoint != nil && *p.Endpoint != "" &&
			*p.Endpoint != prevDefaultEP && *p.Endpoint != c.Endpoint
		if explicit {
			c.Endpoint = *p.Endpoint
		} else {
			c.Endpoint = DefaultEndpointFor(c.Provider)
		}
	} else if p.Endpoint != nil && *p.Endpoint != "" {
		c.Endpoint = *p.Endpoint
	}
	if p.APIKey != nil {
		key := strings.TrimSpace(*p.APIKey)
		// Ignore redacted placeholder from Public() round-trips.
		if key != "" && key != "***" {
			c.APIKey = key
		}
	}
	if p.Backend != nil && *p.Backend != "" {
		c.Backend = *p.Backend
	}
	if p.Mode != nil && *p.Mode != "" {
		c.Mode = *p.Mode
	}
	if p.Specialist != nil {
		c.Specialist = strings.TrimSpace(*p.Specialist)
	}
	if p.DynamicPipeline != nil {
		c.DynamicPipeline = *p.DynamicPipeline
	}
	if p.PinnedSkills != nil {
		c.PinnedSkills = append([]string{}, (*p.PinnedSkills)...)
	}
	if p.Temperature != nil && *p.Temperature >= 0 {
		c.Temperature = *p.Temperature
	}
	if p.MaxTokens != nil && *p.MaxTokens > 0 {
		c.MaxTokens = *p.MaxTokens
	}
	if p.ThinkPasses != nil && *p.ThinkPasses > 0 {
		c.ThinkPasses = *p.ThinkPasses
	}
	if p.MaxParallel != nil && *p.MaxParallel > 0 {
		c.MaxParallel = *p.MaxParallel
	}
	if p.MaxRetries != nil && *p.MaxRetries >= 0 {
		c.MaxRetries = *p.MaxRetries
	}
	if p.MaxContextKB != nil && *p.MaxContextKB > 0 {
		c.MaxContextKB = *p.MaxContextKB
	}
	if p.ActiveStack != nil {
		c.ActiveStack = strings.TrimSpace(*p.ActiveStack)
	} else if providerChanged || (p.Model != nil && *p.Model != "") {
		// Manual provider/model edits leave stack highlight unless explicitly set.
		c.ActiveStack = ""
	}
	if p.ActivePack != nil {
		c.ActivePack = strings.TrimSpace(*p.ActivePack)
	}
	if p.ActivePipeline != nil {
		c.ActivePipeline = strings.TrimSpace(*p.ActivePipeline)
	}
	if p.ModelProfiles != nil && len(*p.ModelProfiles) > 0 {
		c.ModelProfiles = *p.ModelProfiles
	}
	if p.QAGate != nil {
		c.QAGate = *p.QAGate
	}
	if p.QAGateCommand != nil {
		c.QAGateCommand = strings.TrimSpace(*p.QAGateCommand)
	}
	if p.QAGateMaxRounds != nil && *p.QAGateMaxRounds > 0 {
		c.QAGateMaxRounds = *p.QAGateMaxRounds
	}
	if p.PostWorkerSmoke != nil {
		c.PostWorkerSmoke = *p.PostWorkerSmoke
	}
	if p.ClarifyMode != nil {
		c.ClarifyMode = strings.TrimSpace(*p.ClarifyMode)
	}
	if p.ClarifyTimeoutSec != nil && *p.ClarifyTimeoutSec > 0 {
		c.ClarifyTimeout = time.Duration(*p.ClarifyTimeoutSec) * time.Second
	}
	if p.ScopeJudge != nil {
		c.ScopeJudge = *p.ScopeJudge
	}
	if p.PlanApprove != nil {
		c.PlanApprove = strings.TrimSpace(*p.PlanApprove)
	}
	if p.PlanApproveTimeoutSec != nil && *p.PlanApproveTimeoutSec > 0 {
		c.PlanApproveTimeout = time.Duration(*p.PlanApproveTimeoutSec) * time.Second
	}
	if p.PlaceholderPass != nil {
		c.PlaceholderPass = *p.PlaceholderPass
	}
	if p.ContinueAsk != nil {
		c.ContinueAsk = strings.TrimSpace(*p.ContinueAsk)
	}
	if p.ContinueAskTimeoutSec != nil && *p.ContinueAskTimeoutSec > 0 {
		c.ContinueAskTimeout = time.Duration(*p.ContinueAskTimeoutSec) * time.Second
	}
	if p.EscalateAsk != nil {
		c.EscalateAsk = strings.TrimSpace(*p.EscalateAsk)
	}
	if p.EscalateMaxRetries != nil && *p.EscalateMaxRetries > 0 {
		c.EscalateMaxRetries = *p.EscalateMaxRetries
	}
	if p.EscalateAskTimeoutSec != nil && *p.EscalateAskTimeoutSec > 0 {
		c.EscalateAskTimeout = time.Duration(*p.EscalateAskTimeoutSec) * time.Second
	}
	if p.EscalateTimeoutAgent != nil {
		c.EscalateTimeoutAgent = strings.TrimSpace(*p.EscalateTimeoutAgent)
	}
	if p.AutoApprove != nil {
		c.AutoApprove = *p.AutoApprove
	}
	if p.ShellAskTimeoutSec != nil && *p.ShellAskTimeoutSec > 0 {
		c.ShellAskTimeout = time.Duration(*p.ShellAskTimeoutSec) * time.Second
	}
	if p.ContextCompact != nil {
		c.ContextCompact = *p.ContextCompact
	}
	if p.WaveSnapshots != nil {
		c.WaveSnapshots = *p.WaveSnapshots
	}
	if p.FileCheckpoints != nil {
		c.FileCheckpoints = *p.FileCheckpoints
	}
	if p.HooksEnabled != nil {
		c.HooksEnabled = *p.HooksEnabled
	}
	if p.WriteGuard != nil {
		c.WriteGuard = *p.WriteGuard
	}
	if p.ReadBeforeEdit != nil {
		c.ReadBeforeEdit = *p.ReadBeforeEdit
	}
	if p.ShellWriteGuard != nil {
		c.ShellWriteGuard = *p.ShellWriteGuard
	}
	if p.ToolGuidance != nil {
		c.ToolGuidance = *p.ToolGuidance
	}
	if p.KnowledgeInject != nil {
		c.KnowledgeInject = *p.KnowledgeInject
	}
	if p.QualityMonitor != nil {
		c.QualityMonitor = *p.QualityMonitor
	}
	if p.StaticQuality != nil {
		c.StaticQuality = *p.StaticQuality
	}
	if p.ThinkingBudget != nil {
		c.ThinkingBudget = *p.ThinkingBudget
	}
	if p.ThinkingBudgetTokens != nil && *p.ThinkingBudgetTokens >= 0 {
		c.ThinkingBudgetTokens = *p.ThinkingBudgetTokens
	}
	if p.FinalizeWarn != nil {
		c.FinalizeWarn = *p.FinalizeWarn
	}
	if p.RequireSmoke != nil {
		c.RequireSmoke = *p.RequireSmoke
	}
	if p.ClaimsGate != nil {
		c.ClaimsGate = *p.ClaimsGate
	}
	if p.WorkerCritique != nil {
		c.WorkerCritique = *p.WorkerCritique
	}
	if p.OverEditGuard != nil {
		c.OverEditGuard = *p.OverEditGuard
	}
	if p.ReadHeadLines != nil && *p.ReadHeadLines > 0 {
		c.ReadHeadLines = *p.ReadHeadLines
	}
	if p.AutoTextTools != nil {
		c.AutoTextTools = *p.AutoTextTools
	}
	if p.Verbose != nil {
		c.Verbose = *p.Verbose
	}
	if p.Listen != nil && *p.Listen != "" {
		c.Listen = *p.Listen
	}
	// Permission and dry-run stay in sync. Explicit permission wins when both set.
	if p.Permission != nil && *p.Permission != "" {
		c.Permission = permissions.Normalize(*p.Permission)
		c.DryRun = c.Permission == permissions.ModeDryRun
	} else if p.DryRun != nil {
		c.DryRun = *p.DryRun
		if c.DryRun {
			c.Permission = permissions.ModeDryRun
		} else if c.Permission == permissions.ModeDryRun {
			c.Permission = permissions.ModeAuto
		}
	}
	if p.ShellPermission != nil && *p.ShellPermission != "" {
		c.ShellPermission = permissions.NormalizeShell(*p.ShellPermission)
	}
	if p.ShellWhitelist != nil {
		c.ShellWhitelist = *p.ShellWhitelist
	}
	if p.CompactMode != nil {
		c.CompactMode = *p.CompactMode
	}
	if p.EmbeddingEnabled != nil {
		c.EmbeddingEnabled = *p.EmbeddingEnabled
	}
	if p.EmbeddingEndpoint != nil && *p.EmbeddingEndpoint != "" {
		c.EmbeddingEndpoint = *p.EmbeddingEndpoint
	}
	if p.EmbeddingModel != nil && *p.EmbeddingModel != "" {
		c.EmbeddingModel = *p.EmbeddingModel
	}
	if p.EmbeddingAPIKey != nil {
		key := strings.TrimSpace(*p.EmbeddingAPIKey)
		if key != "" && key != "***" {
			c.EmbeddingAPIKey = key
		}
	}
	if p.EmbeddingTopK != nil && *p.EmbeddingTopK > 0 {
		c.EmbeddingTopK = *p.EmbeddingTopK
	}
	if p.PricePreset != nil {
		c.PricePreset = strings.ToLower(strings.TrimSpace(*p.PricePreset))
	}
	if p.PricePromptPerMTok != nil {
		c.PricePromptPerMTok = *p.PricePromptPerMTok
	}
	if p.PriceCompletionPerMTok != nil {
		c.PriceCompletionPerMTok = *p.PriceCompletionPerMTok
	}
	if p.EnabledModels != nil {
		c.EnabledModels = append([]string{}, (*p.EnabledModels)...)
	}
	if p.LLMRetryCount != nil && *p.LLMRetryCount >= 0 {
		c.LLMRetryCount = *p.LLMRetryCount
	}
	if p.LLMRetryDelayMS != nil && *p.LLMRetryDelayMS >= 0 {
		c.LLMRetryDelayMS = *p.LLMRetryDelayMS
	}
	if p.ContextCompactEngine != nil && strings.TrimSpace(*p.ContextCompactEngine) != "" {
		c.ContextCompactEngine = strings.TrimSpace(*p.ContextCompactEngine)
	}
	if p.ReactCompact != nil {
		c.ReactCompact = *p.ReactCompact
	}
	if p.ReactCompactAtPercent != nil && *p.ReactCompactAtPercent >= 0 {
		c.ReactCompactAtPercent = *p.ReactCompactAtPercent
	}
	if p.SessionEventLog != nil {
		c.SessionEventLog = *p.SessionEventLog
	}
	if p.AutoRefine != nil {
		c.AutoRefine = *p.AutoRefine
	}
	if p.AutoRefineMaxRounds != nil && *p.AutoRefineMaxRounds > 0 {
		c.AutoRefineMaxRounds = *p.AutoRefineMaxRounds
	}
	// Keys with no typed field above (every knob added since Patch was written)
	// are applied reflectively — see patch.go.
	c.applyExtra(p)
	normalize(c)
}

// Public returns a safe copy for the UI (redacts api key).
func (c *Config) Public() Config {
	p := *c
	if p.APIKey != "" {
		p.APIKey = "***"
	}
	if p.EmbeddingAPIKey != "" {
		p.EmbeddingAPIKey = "***"
	}
	return p
}

// RetrievalConfig maps embedding settings for pkg/retrieval.
func (c *Config) RetrievalConfig() (enabled bool, endpoint, model, apiKey string, topK int) {
	if c == nil {
		return false, "", "", "", 5
	}
	return c.EmbeddingEnabled, c.EmbeddingEndpoint, c.EmbeddingModel, c.EmbeddingAPIKey, c.EmbeddingTopK
}

// PriceRates returns effective $/MTok rates. Explicit price_* win; else optional
// price_preset ballparks. Returns ok=false when cost display should stay tokens-only
// (no fake $ for unknown models without preset/config).
func (c *Config) PriceRates() (prompt, completion float64, ok bool) {
	if c == nil {
		return 0, 0, false
	}
	if c.PricePromptPerMTok > 0 || c.PriceCompletionPerMTok > 0 {
		return c.PricePromptPerMTok, c.PriceCompletionPerMTok, true
	}
	pin, cout, ok := PricePresetRates(c.PricePreset, c.Provider)
	return pin, cout, ok
}

// PricePresetRates maps preset/provider-family names to ballpark $/MTok.
// local/omlx/ollama/lmstudio → $0 (configured, free). openai/anthropic/openrouter
// use public ballparks. Unknown/off/empty → not configured.
func PricePresetRates(preset, provider string) (prompt, completion float64, ok bool) {
	name := strings.ToLower(strings.TrimSpace(preset))
	if name == "" || name == "off" || name == "none" || name == "disable" || name == "disabled" {
		return 0, 0, false
	}
	if name == "auto" {
		name = NormalizeProvider(provider)
	}
	switch name {
	case "local", "omlx", "ollama", "lmstudio", "vllm", "mlx":
		return 0, 0, true // explicitly free / local
	case "openai", "gpt":
		// Ballpark GPT-4o-mini class ($/MTok) — not model-perfect; override with price_*.
		return 0.15, 0.60, true
	case "anthropic", "claude":
		return 3.0, 15.0, true
	case "openrouter":
		return 0.50, 1.50, true
	default:
		return 0, 0, false
	}
}

// NormalizeClarifyMode maps clarify_mode aliases (auto|ask|off).
func NormalizeClarifyMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", "auto", "defaults", "recommend":
		return "auto"
	case "ask", "interview", "hitl":
		return "ask"
	case "off", "skip", "none", "false":
		return "off"
	default:
		return "auto"
	}
}

// NormalizePlanApprove maps plan_approve aliases (auto|ask|off).
func NormalizePlanApprove(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", "auto", "skip", "continue":
		return "auto"
	case "ask", "hitl", "approve", "gate":
		return "ask"
	case "off", "none", "false":
		return "off"
	default:
		return "auto"
	}
}

// NormalizeContinueAsk maps continue_ask aliases (ask|auto|off).
func NormalizeContinueAsk(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", "ask", "hitl", "prompt":
		return "ask"
	case "auto", "once", "retry":
		return "auto"
	case "off", "skip", "none", "false":
		return "off"
	default:
		return "ask"
	}
}

// NormalizeEscalateAsk maps escalate_ask aliases (ask|auto|off).
func NormalizeEscalateAsk(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", "ask", "hitl", "prompt", "pause":
		return "ask"
	case "auto", "once", "retry":
		return "auto"
	case "off", "skip", "none", "false":
		return "off"
	default:
		return "ask"
	}
}

// NormalizePlanApproveOnTimeout maps plan_approve_on_timeout aliases.
func NormalizePlanApproveOnTimeout(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case PlanTimeoutApprove, "yes", "allow", "continue":
		return PlanTimeoutApprove
	case PlanTimeoutReject, "no", "deny", "stop":
		return PlanTimeoutReject
	default:
		return PlanTimeoutAuto
	}
}

// NormalizeQABootstrap maps qa_bootstrap aliases (off|ask|auto).
func NormalizeQABootstrap(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case QABootstrapOff, "none", "never", "false", "0":
		return QABootstrapOff
	case QABootstrapAuto, "always", "true", "1", "yes":
		return QABootstrapAuto
	default:
		return QABootstrapAsk
	}
}

// NormalizeStructuredDecoding maps structured_decoding aliases (auto|off).
func NormalizeStructuredDecoding(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case DecodingOff, "none", "false", "0", "prompt", "prompt-only":
		return DecodingOff
	default:
		return DecodingAuto
	}
}

// NormalizeSkillDisclosure maps skill_disclosure aliases (auto|cards|full).
func NormalizeSkillDisclosure(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case SkillDisclosureCards, "card", "cards-only", "minimal":
		return SkillDisclosureCards
	case SkillDisclosureFull, "all", "expand", "bodies":
		return SkillDisclosureFull
	default:
		return SkillDisclosureAuto
	}
}

// ExploreEnabled reports whether the bandit may explore. DryRun forces greedy:
// a dry run must never move the learned policy.
func (c *Config) ExploreEnabled() bool {
	if c == nil {
		return true
	}
	return !c.Deterministic && !c.DryRun
}

// RoleContextPercent returns the configured share of the available context
// window for a role, or 0 when the built-in table should decide.
func (c *Config) RoleContextPercent(role string) int {
	if c == nil || len(c.ContextRoleBudget) == 0 {
		return 0
	}
	return c.ContextRoleBudget[strings.ToLower(strings.TrimSpace(role))]
}
