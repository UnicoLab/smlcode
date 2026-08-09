package compact

import (
	"context"
	"fmt"
	"strings"
)

// Summarizer produces a shorter CONTEXT body (LLM or mock).
type Summarizer func(ctx context.Context, body string, maxBytes int) (string, error)

// Summarize chooses engine: heuristic | llm | auto.
// auto tries LLM then falls back to heuristic on error/empty.
func Summarize(ctx context.Context, engine string, body string, maxBytes int, llm Summarizer) Result {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		engine = "heuristic"
	}
	body = strings.TrimSpace(body)
	before := len(body)
	if maxBytes <= 0 {
		maxBytes = 16 * 1024
	}
	if before <= maxBytes {
		return Result{BeforeBytes: before, AfterBytes: before, Compacted: false, Summary: body}
	}

	switch engine {
	case "llm":
		if llm == nil {
			return HeuristicSummarize(body, maxBytes)
		}
		out, err := llm(ctx, body, maxBytes)
		if err != nil || strings.TrimSpace(out) == "" {
			return HeuristicSummarize(body, maxBytes)
		}
		out = strings.TrimSpace(out)
		if len(out) > maxBytes {
			out = truncateBytes(out, maxBytes)
		}
		return Result{BeforeBytes: before, AfterBytes: len(out), Compacted: true, Summary: out}
	case "auto":
		if llm != nil {
			out, err := llm(ctx, body, maxBytes)
			if err == nil && strings.TrimSpace(out) != "" {
				out = strings.TrimSpace(out)
				if len(out) > maxBytes {
					out = truncateBytes(out, maxBytes)
				}
				// Reject nonsensical expansions.
				if len(out) < before {
					return Result{BeforeBytes: before, AfterBytes: len(out), Compacted: true, Summary: out}
				}
			}
		}
		return HeuristicSummarize(body, maxBytes)
	default:
		return HeuristicSummarize(body, maxBytes)
	}
}

// BuildLLMCompactPrompt is the system-style user prompt for CONTEXT compaction.
func BuildLLMCompactPrompt(body string, maxBytes int) string {
	return fmt.Sprintf(`Compress the following project CONTEXT.md for a coding agent.
Keep: Locked PRD, Active focus, Constraints, Open questions, file paths still relevant.
Drop: redundant wave chatter, repeated status, verbose tool dumps.
Target under %d bytes. Output markdown only — no preamble.

---
%s
`, maxBytes, body)
}

// IsContextOverflow reports whether an LLM/provider error looks like context overflow.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	needles := []string{
		"context length",
		"context_length",
		"maximum context",
		"max context",
		"token limit",
		"too many tokens",
		"context window",
		"prompt is too long",
		"exceeds the model",
		"exceeds maximum",
		"context_overflow",
		"string_above_max_length",
	}
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
