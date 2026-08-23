package compact

import (
	"context"
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// Summarizer produces a shorter CONTEXT body (LLM or mock).
type Summarizer func(ctx context.Context, body string, maxBytes int) (string, error)

// Summarize chooses engine: heuristic | llm | auto.
//
// Both the llm and the auto engines now run every candidate through
// AcceptCompaction before it is allowed to replace the input; a failed gate
// falls back to HeuristicSummarize. Result.Original always carries the
// pre-compaction body so a caller that overwrites a document on disk can
// snapshot or restore it.
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
		return Result{BeforeBytes: before, AfterBytes: before, Compacted: false, Summary: body, Original: body}
	}

	switch engine {
	case "llm", "auto":
		if llm == nil {
			res := HeuristicSummarize(body, maxBytes)
			res.Original = body
			res.Rejected = GateNoSummarize
			return res
		}
		out, err := llm(ctx, body, maxBytes)
		if err == nil {
			out = StripPreamble(out)
			if len(out) > maxBytes {
				out = truncateBytes(out, maxBytes)
			}
			gate := AcceptCompaction(body, out)
			if gate == GateOK {
				return Result{
					BeforeBytes: before, AfterBytes: len(out), Compacted: true,
					Summary: out, Original: body, Engine: engine,
				}
			}
			res := HeuristicSummarize(body, maxBytes)
			res.Original = body
			res.Rejected = gate
			return res
		}
		res := HeuristicSummarize(body, maxBytes)
		res.Original = body
		return res
	default:
		res := HeuristicSummarize(body, maxBytes)
		res.Original = body
		return res
	}
}

// BuildLLMCompactPrompt is the system-style user prompt for CONTEXT compaction.
func BuildLLMCompactPrompt(body string, maxBytes int) string {
	return fmt.Sprintf(`Compress the following project CONTEXT.md for a coding agent.
Keep: Locked PRD, Active focus, Constraints, Open questions, every `+"`file/path.ext`"+` still relevant.
Drop: redundant wave chatter, repeated status, verbose tool dumps.
Rules:
- Output markdown only. No preamble, no "Sure, here is".
- Keep at least one "## " heading.
- Keep every backticked file path that appears in the input.
Target under %d bytes.

---
%s
`, maxBytes, textutil.Clip(body, 400*1024))
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
