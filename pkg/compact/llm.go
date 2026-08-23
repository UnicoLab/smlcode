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
			// Gate the candidate the MODEL produced, then fit it to the budget.
			//
			// The order used to be the other way round, and it made the budget
			// an accuracy test: a good summary a few hundred bytes over target
			// was chopped mid-document, the chop took its tail with it — which
			// is where the last `path/like.go` mentions live — and GateLostPaths
			// then rejected the model's work for damage the harness had just
			// done to it. Every one of those runs silently fell back to the
			// heuristic.
			gate := AcceptCompaction(body, out)
			if gate == GateOK {
				out = fitToBudget(out, maxBytes)
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

// fitToBudget trims an ACCEPTED summary to maxBytes, cutting at the last
// section boundary that fits so a trim costs a whole section rather than half a
// sentence. It falls back to a rune-safe byte truncation when no boundary
// helps.
func fitToBudget(out string, maxBytes int) string {
	if maxBytes <= 0 || len(out) <= maxBytes {
		return out
	}
	if cut := strings.LastIndex(out[:maxBytes], "\n## "); cut > maxBytes/2 {
		return strings.TrimSpace(out[:cut])
	}
	if cut := strings.LastIndex(out[:maxBytes], "\n\n"); cut > maxBytes/2 {
		return strings.TrimSpace(out[:cut])
	}
	return truncateBytes(out, maxBytes)
}
