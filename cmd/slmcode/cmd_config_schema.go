package main

import (
	"sort"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// config.Schema() only describes the fields the Studio settings page renders —
// roughly a third of config.Patch. The old `config set` covered the gap with a
// hand-written switch that used fmt.Sscanf and reported success regardless of
// whether the value parsed. This table closes the gap properly: every remaining
// patchable key gets a declared type (and enum where one exists), so a bad
// value is rejected instead of silently ignored.
var cliExtraSchema = []config.FieldSchema{
	{Key: "api_key", Type: "string", Label: "API key", Group: "model", Patchable: true},
	{Key: "backend", Type: "string", Label: "Backend", Group: "model", Patchable: true,
		Enum: []string{"slmcode", "claude-code"}},
	{Key: "mode", Type: "string", Label: "Mode", Group: "pipeline", Patchable: true,
		Enum: []string{"full", "specialist"}},
	{Key: "specialist", Type: "string", Label: "Specialist", Group: "pipeline", Patchable: true},
	{Key: "pinned_skills", Type: "string[]", Label: "Pinned skills", Group: "pipeline", Patchable: true},
	{Key: "think_passes", Type: "int", Label: "Think passes", Group: "pipeline", Patchable: true},
	{Key: "max_parallel", Type: "int", Label: "Max parallel", Group: "pipeline", Patchable: true},
	{Key: "max_retries", Type: "int", Label: "Max retries", Group: "pipeline", Patchable: true},
	{Key: "max_context_kb", Type: "int", Label: "Max context KB", Group: "harness", Patchable: true},
	{Key: "qa_gate", Type: "bool", Label: "QA gate", Group: "quality", Patchable: true},
	{Key: "qa_gate_command", Type: "string", Label: "QA gate command", Group: "quality", Patchable: true},
	{Key: "qa_gate_max_rounds", Type: "int", Label: "QA gate max rounds", Group: "quality", Patchable: true},
	{Key: "post_worker_smoke", Type: "bool", Label: "Post-worker smoke", Group: "quality", Patchable: true},
	{Key: "scope_judge", Type: "bool", Label: "Scope judge", Group: "quality", Patchable: true},
	{Key: "placeholder_pass", Type: "bool", Label: "Placeholder pass", Group: "quality", Patchable: true},
	{Key: "worker_critique", Type: "bool", Label: "Worker critique", Group: "quality", Patchable: true},
	{Key: "quality_monitor", Type: "bool", Label: "Quality monitor", Group: "quality", Patchable: true},
	{Key: "static_quality", Type: "bool", Label: "Static quality", Group: "quality", Patchable: true},
	{Key: "continue_ask", Type: "string", Label: "Continue ask", Group: "hitl", Patchable: true,
		Enum: []string{"off", "auto", "ask"}},
	{Key: "escalate_ask", Type: "string", Label: "Escalate ask", Group: "hitl", Patchable: true,
		Enum: []string{"off", "auto", "ask"}},
	{Key: "escalate_timeout_agent", Type: "string", Label: "Escalate timeout agent", Group: "hitl", Patchable: true},
	{Key: "auto_approve", Type: "bool", Label: "Auto approve", Group: "hitl", Patchable: true},
	{Key: "permission", Type: "string", Label: "Permission", Group: "safety", Patchable: true,
		Enum: []string{"auto", "dry-run", "review"}},
	{Key: "shell_permission", Type: "string", Label: "Shell permission", Group: "safety", Patchable: true,
		Enum: []string{"allow", "ask", "deny"}},
	{Key: "dry_run", Type: "bool", Label: "Dry run", Group: "safety", Patchable: true},
	{Key: "write_guard", Type: "bool", Label: "Write guard", Group: "safety", Patchable: true},
	{Key: "read_before_edit", Type: "bool", Label: "Read before edit", Group: "safety", Patchable: true},
	{Key: "wave_snapshots", Type: "bool", Label: "Wave snapshots", Group: "safety", Patchable: true},
	{Key: "hooks_enabled", Type: "bool", Label: "Hooks enabled", Group: "harness", Patchable: true},
	{Key: "tool_guidance", Type: "bool", Label: "Tool guidance", Group: "harness", Patchable: true},
	{Key: "knowledge_inject", Type: "bool", Label: "Knowledge inject", Group: "harness", Patchable: true},
	{Key: "thinking_budget", Type: "bool", Label: "Thinking budget", Group: "harness", Patchable: true},
	{Key: "compact_mode", Type: "bool", Label: "Compact stream", Group: "ui", Patchable: true},
	{Key: "verbose", Type: "bool", Label: "Verbose", Group: "ui", Patchable: true},
	{Key: "listen", Type: "string", Label: "Studio listen address", Group: "ui", Patchable: true},
	{Key: "embedding_enabled", Type: "bool", Label: "Embeddings", Group: "retrieval", Patchable: true},
	{Key: "embedding_endpoint", Type: "string", Label: "Embedding endpoint", Group: "retrieval", Patchable: true},
	{Key: "embedding_model", Type: "string", Label: "Embedding model", Group: "retrieval", Patchable: true},
	{Key: "embedding_api_key", Type: "string", Label: "Embedding API key", Group: "retrieval", Patchable: true},
	{Key: "embedding_top_k", Type: "int", Label: "Embedding top K", Group: "retrieval", Patchable: true},
	{Key: "price_prompt_per_mtok", Type: "float", Label: "Prompt price /MTok", Group: "cost", Patchable: true},
	{Key: "price_completion_per_mtok", Type: "float", Label: "Completion price /MTok", Group: "cost", Patchable: true},
}

// mergedSchema is config.Schema() plus cliExtraSchema, deduplicated by key with
// the upstream schema winning (it carries richer labels and enums).
func mergedSchema() []config.FieldSchema {
	seen := map[string]bool{}
	var out []config.FieldSchema
	for _, f := range config.Schema() {
		if seen[f.Key] {
			continue
		}
		seen[f.Key] = true
		out = append(out, f)
	}
	for _, f := range cliExtraSchema {
		if seen[f.Key] {
			continue
		}
		seen[f.Key] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
