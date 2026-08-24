package autoresearch

import (
	"sort"
	"strings"
)

// The mutable surface is an ALLOW-LIST, not a deny-list.
//
// An experiment that can reach `api_key`, `permission`, `shell_allow`,
// `hooks_enabled`, `mcp_servers` or `root` is not an experiment, it is a
// privilege-escalation primitive with a research paper stapled to it. So the
// rule here is inverted from the usual: a knob is immutable unless this file
// names it, gives it a domain to move in, and says why moving it is safe.
//
// Every entry below is a behavioral tuning knob whose worst case is a slower or
// dumber harness — never a wider one. Nothing on this list can change who the
// harness talks to, what it is allowed to run, where it may write, or what it
// may read.

// AgentField is one mutable field of an agent YAML.
type AgentField struct {
	// Name is the YAML key, in the agent spec mapping.
	Name string
	// Domain bounds what a proposer may put there.
	Domain Domain
	// Why records the reason this field is safe to move.
	Why string
}

// agentFields is the complete set of agent YAML fields an experiment may
// touch. Declaration order is the surface order — never a map range.
var agentFields = []AgentField{
	{
		Name:   "temperature",
		Domain: Domain{Kind: KnobFloat, Min: 0, Max: 1, Step: 0.05},
		Why:    "sampling temperature for this role only; cannot widen access",
	},
	{
		Name:   "max_tokens",
		Domain: Domain{Kind: KnobInt, Min: 512, Max: 8192, Step: 512},
		Why:    "per-response completion budget for this role; bounded above",
	},
	{
		Name:   "max_iter",
		Domain: Domain{Kind: KnobInt, Min: 1, Max: 24, Step: 1},
		Why:    "ReAct iteration cap for this role; bounded above",
	},
	{
		Name:   "system_prompt",
		Domain: Domain{Kind: KnobText, MaxLen: MaxPromptLen},
		Why:    "instructions only — the tool layer and permission system are not prompt-reachable",
	},
}

// AgentFields returns the mutable agent YAML fields, in surface order.
func AgentFields() []AgentField {
	out := make([]AgentField, len(agentFields))
	copy(out, agentFields)
	return out
}

// IsAgentField reports whether an agent YAML key may be mutated.
func IsAgentField(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, f := range agentFields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// AgentFieldDomain returns the domain for a mutable agent field.
func AgentFieldDomain(name string) (Domain, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, f := range agentFields {
		if f.Name == name {
			return f.Domain, true
		}
	}
	return Domain{}, false
}

// ConfigKnob is one whitelisted .slmcode/config.yaml key.
type ConfigKnob struct {
	// Key is the config key, spelled exactly as config.yaml spells it.
	Key string
	// Domain bounds what a proposer may put there.
	Domain Domain
	// Why records the reason this key is safe to move.
	Why string
}

// configWhitelist is the complete set of config keys an experiment may touch.
//
// Sorted by key so the surface order is stable without a sort at call time, and
// so a reviewer can scan it against config.yaml. Adding a line here is a
// security decision: it must be a knob whose worst outcome is worse *code*, not
// wider *reach*.
var configWhitelist = []ConfigKnob{
	{
		Key:    "context_slack_percent",
		Domain: Domain{Kind: KnobInt, Min: 5, Max: 25, Step: 5},
		Why:    "headroom for tokenizer disagreement; only shrinks or grows a context pack",
	},
	{
		Key:    "excerpt_window_lines",
		Domain: Domain{Kind: KnobInt, Min: 5, Max: 60, Step: 5},
		Why:    "± lines kept around a relevance match; reads no file it could not already read",
	},
	{
		Key:    "max_retries",
		Domain: Domain{Kind: KnobInt, Min: 0, Max: 6, Step: 1},
		Why:    "review/correct retries per task; bounded above",
	},
	{
		Key:    "max_task_calls",
		Domain: Domain{Kind: KnobInt, Min: 4, Max: 16, Step: 2},
		Why:    "per-task LLM call budget; bounded above",
	},
	{
		Key:    "max_tokens",
		Domain: Domain{Kind: KnobInt, Min: 1024, Max: 8192, Step: 512},
		Why:    "per-response completion budget; bounded above",
	},
	{
		Key:    "memory_tokens",
		Domain: Domain{Kind: KnobInt, Min: 100, Max: 800, Step: 50},
		Why:    "token budget for the injected memory block",
	},
	{
		Key:    "react_compact_at_percent",
		Domain: Domain{Kind: KnobInt, Min: 50, Max: 95, Step: 5},
		Why:    "when ReAct compaction triggers, as a share of the context window",
	},
	{
		Key:    "repo_map_tokens",
		Domain: Domain{Kind: KnobInt, Min: 0, Max: 2000, Step: 100},
		Why:    "the repo map's share of a context pack; 0 disables the map",
	},
	{
		Key:    "skill_disclosure",
		Domain: Domain{Kind: KnobEnum, Values: []string{"auto", "cards", "full"}},
		Why:    "how much of a matched skill is inlined; all three values are shipped modes",
	},
	{
		Key:    "skill_max_expanded",
		Domain: Domain{Kind: KnobInt, Min: 0, Max: 4, Step: 1},
		Why:    "how many skill bodies may be inlined at once",
	},
	{
		Key:    "structured_decoding",
		Domain: Domain{Kind: KnobEnum, Values: []string{"auto", "off"}},
		Why:    "constrained-decoding policy; both values are shipped modes",
	},
	{
		Key:    "temperature",
		Domain: Domain{Kind: KnobFloat, Min: 0, Max: 1, Step: 0.05},
		Why:    "sampling temperature for every role",
	},
	{
		Key:    "think_passes",
		Domain: Domain{Kind: KnobInt, Min: 1, Max: 3, Step: 1},
		Why:    "multi-pass deliberation loops per role; bounded above",
	},
	{
		Key:    "worker_critique",
		Domain: Domain{Kind: KnobBool, Values: []string{"false", "true"}},
		Why:    "whether a weak worker answer gets a self-fix pass; a quality check, not a gate",
	},
}

// ConfigWhitelist returns every mutable config knob, in surface order.
func ConfigWhitelist() []ConfigKnob {
	out := make([]ConfigKnob, len(configWhitelist))
	copy(out, configWhitelist)
	return out
}

// IsWhitelisted reports whether a config key may be mutated by an experiment.
// Everything not named in configWhitelist is immutable — including keys that do
// not exist, so a typo can never be mistaken for permission.
func IsWhitelisted(key string) bool {
	_, ok := configKnob(key)
	return ok
}

// configKnob looks up a whitelisted config key.
func configKnob(key string) (ConfigKnob, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, k := range configWhitelist {
		if k.Key == key {
			return k, true
		}
	}
	return ConfigKnob{}, false
}

// securitySensitiveKeys names config keys that must NEVER become mutable, with
// the reason each one is disqualifying.
//
// The allow-list above already excludes them by construction — this list exists
// so the exclusion is asserted rather than assumed (see the whitelist tests),
// and so a future contributor who adds one of these to configWhitelist gets a
// failing test with the reason attached instead of a merged pull request.
var securitySensitiveKeys = map[string]string{
	"api_key":             "provider credential",
	"embedding_api_key":   "provider credential",
	"provider":            "chooses who the harness talks to",
	"endpoint":            "chooses who the harness talks to",
	"embedding_endpoint":  "chooses who the harness talks to",
	"model":               "changes the measured system, not a knob on it",
	"fast_model":          "changes the measured system, not a knob on it",
	"backend":             "swaps the execution engine wholesale",
	"claude_code_bin":     "names an executable the harness will run",
	"permission":          "the write policy",
	"shell_permission":    "the shell policy",
	"shell_whitelist":     "the shell command allow-list gate",
	"shell_allow":         "extends the shell command allow-list",
	"hooks_enabled":       "runs repository-supplied shell commands on every tool call",
	"mcp_servers":         "spawns subprocesses and opens network connections",
	"skills_dirs":         "extra filesystem roots read into prompts",
	"retrieval_cache_dir": "a filesystem path the harness writes to",
	"listen":              "the address the server binds",
	"auto_approve":        "skips human approval gates",
	"dry_run":             "disables writes; flipping it is not a tuning decision",
	"write_guard":         "a workspace safety invariant",
	"read_before_edit":    "a workspace safety invariant",
	"shell_write_guard":   "a workspace safety invariant",
}

// SecuritySensitiveKeys lists the config keys that are disqualified from the
// mutable surface, sorted, with the reason each one is disqualified.
func SecuritySensitiveKeys() map[string]string {
	out := make(map[string]string, len(securitySensitiveKeys))
	for k, v := range securitySensitiveKeys {
		out[k] = v
	}
	return out
}

// SecuritySensitiveKeyList is SecuritySensitiveKeys as a sorted slice of keys,
// for anything that has to render them in a stable order.
func SecuritySensitiveKeyList() []string {
	out := make([]string, 0, len(securitySensitiveKeys))
	for k := range securitySensitiveKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
