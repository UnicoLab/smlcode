package contextstore

import (
	"strings"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// TokenCounter converts prompt text to a token count.
type TokenCounter func(string) int

// Reserve defaults, in tokens. The pack budget is the model's context window
// MINUS everything else that shares it, which the packer does not see:
//
//	system prompt + tool schemas + response max_tokens + slack
//
// Budgeting in BYTES (the historical behaviour) is what starved a 32K Qwen
// down to ~3.2K tokens of context while the compaction watchdog believed it
// was at 80% capacity.
const (
	DefaultReserveSystemTokens   = 500  // specialist system prompt
	DefaultReserveToolTokens     = 900  // ws_* JSON schemas
	DefaultReserveResponseTokens = 2048 // the model still has to answer
	DefaultSlackPercent          = 10   // tokenizer disagreement + chat scaffolding

	// FallbackCharsPerToken is the heuristic used when tiktoken is unavailable.
	FallbackCharsPerToken = 4

	// MinPackTokens is the floor a pack budget is never clamped below —
	// under this a specialist cannot see anything useful at all.
	MinPackTokens = 512
)

// DefaultTokenCounter counts tokens with tiktoken (cl100k_base) via
// llm.EstimateTokens, falling back to a chars/4 heuristic when the tokenizer
// is unavailable or returns nothing for non-empty text.
func DefaultTokenCounter(s string) int {
	if s == "" {
		return 0
	}
	if n := llm.EstimateTokens(s); n > 0 {
		return n
	}
	return HeuristicTokenCounter(s)
}

// HeuristicTokenCounter is the dependency-free chars/4 estimate.
func HeuristicTokenCounter(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + FallbackCharsPerToken - 1) / FallbackCharsPerToken
}

// Budget derives the token budget available to a context pack.
type Budget struct {
	// ContextLimitTokens is the model's real window
	// (config.ModelProfile.ContextLimit: 8192 / 16384 / 32768).
	ContextLimitTokens int
	// ReserveSystemTokens, ReserveToolTokens, ReserveResponseTokens and
	// SlackPercent are subtracted from the window before the pack gets a share.
	ReserveSystemTokens   int
	ReserveToolTokens     int
	ReserveResponseTokens int
	SlackPercent          int
}

// DefaultBudget returns the standard reserves for a model window.
func DefaultBudget(contextLimitTokens int) Budget {
	return Budget{
		ContextLimitTokens:    contextLimitTokens,
		ReserveSystemTokens:   DefaultReserveSystemTokens,
		ReserveToolTokens:     DefaultReserveToolTokens,
		ReserveResponseTokens: DefaultReserveResponseTokens,
		SlackPercent:          DefaultSlackPercent,
	}
}

// Available returns the tokens a pack may consume for a role.
func (b Budget) Available(role string) int {
	window := b.ContextLimitTokens
	if window <= 0 {
		window = TokensFromKB(DefaultMaxContextKB)
	}
	reserved := b.ReserveSystemTokens + b.ReserveToolTokens + b.ReserveResponseTokens
	if reserved <= 0 {
		reserved = DefaultReserveSystemTokens + DefaultReserveToolTokens + DefaultReserveResponseTokens
	}
	avail := window - reserved
	slack := b.SlackPercent
	if slack <= 0 || slack >= 100 {
		slack = DefaultSlackPercent
	}
	avail = avail * (100 - slack) / 100
	avail = avail * RoleBudgetPercent(role) / 100
	if avail < MinPackTokens {
		avail = MinPackTokens
	}
	return avail
}

// DefaultMaxContextKB mirrors config.DefaultMaxContextKB without importing it
// (pkg/config depends on this package's siblings).
const DefaultMaxContextKB = 16

// TokensFromKB converts the legacy prompt-byte budget into tokens. This is the
// compatibility path used by NewPacker when no model context limit is supplied.
func TokensFromKB(kb int) int {
	if kb <= 0 {
		kb = DefaultMaxContextKB
	}
	return kb * 1024 / FallbackCharsPerToken
}

// RoleBudgetPercent is the share of the available window a role may use.
//
// Implementation roles get the most: a worker that cannot see the function it
// must edit cannot produce an exact old_str match. Exploratory and summarizing
// roles run on identifiers and docs and need far less, and giving a small model
// less irrelevant text measurably improves its instruction-following.
func RoleBudgetPercent(role string) int {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "worker", "corrector", "deep", "placeholder":
		return 100
	case "reviewer", "tester":
		return 85
	case "architect", "planner", "splitter":
		return 70
	case "explorer", "context", "docs":
		return 60
	case "coordinator", "memory":
		return 50
	default:
		return 75
	}
}
