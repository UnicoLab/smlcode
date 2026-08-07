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
		{Key: "session_event_log", Type: "bool", Label: "Session event log", Group: "harness", Patchable: true,
			Description: "Write .slmcode/queries/<id>/events.jsonl"},
		{Key: "auto_refine", Type: "bool", Label: "Auto refine", Group: "learning", Patchable: true,
			Description: "Append refine notes from wave lessons into CONTEXT"},
		{Key: "auto_refine_max_rounds", Type: "int", Label: "Refine max rounds", Group: "learning", Patchable: true},
		{Key: "price_preset", Type: "string", Label: "Price preset", Group: "cost", Patchable: true,
			Enum: []string{"", "off", "local", "omlx", "openai", "anthropic", "openrouter"}},
		{Key: "active_stack", Type: "string", Label: "Active stack", Group: "stacks", Patchable: true},
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
