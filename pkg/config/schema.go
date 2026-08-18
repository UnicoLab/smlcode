package config

// FieldSchema describes one editable config key for Studio / TUI slash help.
type FieldSchema struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"` // string | int | float | bool | string[]
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Group       string   `json:"group"`
	Enum        []string `json:"enum,omitempty"`
	Patchable   bool     `json:"patchable"`
}

// Schema returns nested config field metadata (prime-agent nested config schemas).
func Schema() []FieldSchema {
	return []FieldSchema{
		{Key: "provider", Type: "string", Label: "Provider", Group: "model", Patchable: true,
			Description: "LLM provider id (omlx, openai, ollama, …)"},
		{Key: "model", Type: "string", Label: "Model", Group: "model", Patchable: true,
			Description: "Default model id"},
		{Key: "fast_model", Type: "string", Label: "Fast model", Group: "model", Patchable: true,
			Description: "Smaller model for lightweight agents (reviewer, planner, etc.). Empty = use main model."},
		{Key: "endpoint", Type: "string", Label: "Endpoint", Group: "model", Patchable: true,
			Description: "OpenAI-compatible base URL"},
		{Key: "enabled_models", Type: "string[]", Label: "Enabled models", Group: "model", Patchable: true,
			Description: "Optional allow-list of model ids (empty = all)"},
		{Key: "temperature", Type: "float", Label: "Temperature", Group: "model", Patchable: true},
		{Key: "max_tokens", Type: "int", Label: "Max tokens", Group: "model", Patchable: true},
		{Key: "llm_retry_count", Type: "int", Label: "LLM retry count", Group: "model", Patchable: true,
			Description: "Provider HTTP retries (separate from board MaxRetries)"},
		{Key: "llm_retry_delay_ms", Type: "int", Label: "LLM retry delay ms", Group: "model", Patchable: true},
		{Key: "context_compact", Type: "bool", Label: "CONTEXT compact", Group: "harness", Patchable: true},
		{Key: "context_compact_engine", Type: "string", Label: "Compact engine", Group: "harness", Patchable: true,
			Enum:        []string{"heuristic", "llm", "auto"},
			Description: "heuristic | llm | auto (LLM with heuristic fallback)"},
		{Key: "react_compact", Type: "bool", Label: "ReAct compact", Group: "harness", Patchable: true},
		{Key: "react_compact_at_percent", Type: "int", Label: "ReAct compact threshold", Group: "harness", Patchable: true,
			Description: "Compact agent conversations after this percentage of the context budget"},
		{Key: "thinking_budget_tokens", Type: "int", Label: "Thinking budget tokens", Group: "harness", Patchable: true,
			Description: "Hard threshold for excessive model deliberation before intervention"},
		{Key: "session_event_log", Type: "bool", Label: "Session event log", Group: "harness", Patchable: true,
			Description: "Write .slmcode/queries/<id>/events.jsonl"},
		{Key: "file_checkpoints", Type: "bool", Label: "File checkpoints", Group: "safety", Patchable: true},
		{Key: "shell_write_guard", Type: "bool", Label: "Shell write guard", Group: "safety", Patchable: true,
			Description: "Block shell redirection patterns that bypass file guards"},
		{Key: "shell_whitelist", Type: "bool", Label: "Shell whitelist", Group: "safety", Patchable: true,
			Description: "Restrict shell execution to safe command prefixes unless explicitly allowed"},
		{Key: "require_smoke", Type: "bool", Label: "Require smoke", Group: "quality", Patchable: true},
		{Key: "claims_gate", Type: "bool", Label: "Claims gate", Group: "quality", Patchable: true,
			Description: "Reject outputs that claim changes not visible on disk"},
		{Key: "over_edit_guard", Type: "bool", Label: "Over-edit guard", Group: "quality", Patchable: true,
			Description: "Refuse broad whole-file rewrites when a small edit is expected"},
		{Key: "finalize_warn", Type: "bool", Label: "Finalize warning", Group: "quality", Patchable: true},
		{Key: "auto_text_tools", Type: "bool", Label: "Auto text tools", Group: "quality", Patchable: true,
			Description: "Recover valid tool calls from prose-embedded JSON"},
		{Key: "read_head_lines", Type: "int", Label: "Read head lines", Group: "harness", Patchable: true,
			Description: "Default head size for automatic file reads"},
		{Key: "auto_refine", Type: "bool", Label: "Auto refine", Group: "learning", Patchable: true,
			Description: "Append refine notes from wave lessons into CONTEXT"},
		{Key: "auto_refine_max_rounds", Type: "int", Label: "Refine max rounds", Group: "learning", Patchable: true},
		{Key: "price_preset", Type: "string", Label: "Price preset", Group: "cost", Patchable: true,
			Enum: []string{"", "off", "local", "omlx", "openai", "anthropic", "openrouter"}},
		{Key: "active_stack", Type: "string", Label: "Active stack", Group: "stacks", Patchable: true},
		{Key: "active_pack", Type: "string", Label: "Active pack", Group: "blocks", Patchable: true,
			Description: "Language/domain building-block pack id (go, python, react, web, …)"},
		{Key: "active_pipeline", Type: "string", Label: "Active pipeline block", Group: "blocks", Patchable: true,
			Description: "Named pipeline preset id from the blocks catalog"},
		{Key: "dynamic_pipeline", Type: "bool", Label: "Dynamic pipeline", Group: "harness", Patchable: true,
			Description: "Run the composer specialist first to assemble a task-specific pipeline (phases, team, tools, skills). Default: on"},
		{Key: "clarify_mode", Type: "string", Label: "Clarify mode", Group: "hitl", Patchable: true,
			Enum:        []string{"ask", "auto", "off"},
			Description: "Whether specification questions pause for user validation. Default: ask"},
		{Key: "clarify_timeout_sec", Type: "int", Label: "Clarify timeout seconds", Group: "hitl", Patchable: true,
			Description: "Seconds before specification asks apply their recommended defaults"},
		{Key: "plan_approve", Type: "string", Label: "Plan approval", Group: "hitl", Patchable: true,
			Enum:        []string{"ask", "auto", "off"},
			Description: "Whether the execution plan pauses for user validation before build. Default: ask"},
		{Key: "plan_approve_timeout_sec", Type: "int", Label: "Plan timeout seconds", Group: "hitl", Patchable: true,
			Description: "Seconds before plan approval applies the default action"},
		{Key: "continue_ask_timeout_sec", Type: "int", Label: "Continue timeout seconds", Group: "hitl", Patchable: true,
			Description: "Seconds before retry/continue asks apply the default action"},
		{Key: "escalate_ask_timeout_sec", Type: "int", Label: "Escalate timeout seconds", Group: "hitl", Patchable: true,
			Description: "Seconds before escalation asks hand off to the configured arbitrator"},
		{Key: "shell_ask_timeout_sec", Type: "int", Label: "Shell timeout seconds", Group: "hitl", Patchable: true,
			Description: "Seconds before shell permission asks apply the configured fallback"},
	}
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
