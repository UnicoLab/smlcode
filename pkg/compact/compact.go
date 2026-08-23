package compact

import (
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
)

// Result of a context compaction pass.
type Result struct {
	BeforeBytes int
	AfterBytes  int
	Compacted   bool
	Summary     string

	// Original is the pre-compaction body. Callers that overwrite a document
	// on disk MUST snapshot this (or keep it in memory) so a bad compaction —
	// an LLM that ate CONTEXT.md — is recoverable.
	Original string
	// Rejected names the acceptance gate an LLM candidate failed, when the
	// result fell back to the heuristic engine. Empty on success.
	Rejected GateFailure
	// Engine is "llm"/"auto" when the LLM output was accepted, else empty.
	Engine string
}

// CompactHysteresisPercent is how far below the soft trigger a compaction must
// land. Compacting exactly TO the trigger means the very next append
// re-triggers a full (possibly LLM) compaction — a pathological loop.
const CompactHysteresisPercent = 70

// NeedsCompact reports whether body exceeds the soft budget (or the hard
// ceiling when one is set).
func NeedsCompact(body string, softKB, hardKB int) bool {
	soft, hard := normalizeBudgets(softKB, hardKB)
	n := len(body)
	return n > soft || (hard > 0 && n > hard)
}

// CompactTargetBytes is the size a compaction should aim FOR — ~70% of the
// soft trigger, so the post-compaction state has real headroom.
func CompactTargetBytes(softKB, hardKB int) int {
	soft, _ := normalizeBudgets(softKB, hardKB)
	target := soft * CompactHysteresisPercent / 100
	if target < 1024 {
		target = 1024
	}
	return target
}

// ForceHeuristic reports whether the body is past the hard ceiling, in which
// case an LLM round-trip must NOT be attempted: the document is too big to
// send safely and a local small model's failure mode there is data loss.
func ForceHeuristic(body string, softKB, hardKB int) bool {
	_, hard := normalizeBudgets(softKB, hardKB)
	return hard > 0 && len(body) > hard
}

// EngineFor picks the engine to actually run given the configured preference
// and the body size: past the hard ceiling it always downgrades to heuristic.
func EngineFor(preferred, body string, softKB, hardKB int) string {
	if ForceHeuristic(body, softKB, hardKB) {
		return "heuristic"
	}
	if strings.TrimSpace(preferred) == "" {
		return "heuristic"
	}
	return preferred
}

func normalizeBudgets(softKB, hardKB int) (softBytes, hardBytes int) {
	if hardKB <= 0 {
		hardKB = 32
	}
	if softKB <= 0 {
		softKB = hardKB * 3 / 4
	}
	if softKB > hardKB {
		hardKB = softKB
	}
	return softKB * 1024, hardKB * 1024
}

// HeuristicSummarize compresses markdown context without an LLM.
// Keeps Locked PRD / headings / recent sections; drops older wave noise.
func HeuristicSummarize(body string, maxBytes int) Result {
	body = strings.TrimSpace(body)
	before := len(body)
	if maxBytes <= 0 {
		maxBytes = 16 * 1024
	}
	if before <= maxBytes {
		return Result{BeforeBytes: before, AfterBytes: before, Compacted: false, Summary: body}
	}

	sections := splitSections(body)
	var keep []string
	var older []string
	for _, sec := range sections {
		title := strings.ToLower(sec.title)
		if strings.Contains(title, "locked prd") ||
			strings.Contains(title, "active focus") ||
			strings.Contains(title, "constraints") ||
			strings.Contains(title, "open questions") ||
			strings.Contains(title, "spec clarification") ||
			sec.title == "" && len(keep) == 0 {
			keep = append(keep, sec.raw)
			continue
		}
		if strings.Contains(title, "wave update") || strings.Contains(title, "wave ") {
			older = append(older, "- "+firstLine(sec.title+": "+sec.body))
			continue
		}
		// Keep recent non-wave sections; summarize the rest.
		if len(strings.Join(keep, "\n")) < maxBytes/2 {
			keep = append(keep, sec.raw)
		} else {
			older = append(older, "- "+firstLine(sec.title))
		}
	}

	var b strings.Builder
	b.WriteString("# CONTEXT (compacted)\n\n")
	b.WriteString("_Older wave noise summarized for SLM context budget._\n\n")
	for _, k := range keep {
		b.WriteString(strings.TrimSpace(k))
		b.WriteString("\n\n")
	}
	if len(older) > 0 {
		b.WriteString("## Compacted history\n\n")
		max := 24
		if len(older) < max {
			max = len(older)
		}
		// Keep the most recent older bullets.
		start := len(older) - max
		if start < 0 {
			start = 0
		}
		for _, line := range older[start:] {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > maxBytes {
		out = truncateBytes(out, maxBytes)
	}
	return Result{
		BeforeBytes: before,
		AfterBytes:  len(out),
		Compacted:   true,
		Summary:     out,
	}
}

// CompactMessages shrinks a chat/ReAct transcript to the last N turns
// plus a one-line digest of dropped prefix.
func CompactMessages(messages []string, keepLast int) ([]string, bool) {
	if keepLast <= 0 {
		keepLast = 8
	}
	if len(messages) <= keepLast {
		return messages, false
	}
	dropped := messages[:len(messages)-keepLast]
	digest := fmt.Sprintf("[compacted %d earlier messages] %s",
		len(dropped), firstLine(strings.Join(dropped, " | ")))
	out := append([]string{digest}, messages[len(messages)-keepLast:]...)
	return out, true
}

type section struct {
	title string
	body  string
	raw   string
}

func splitSections(body string) []section {
	lines := strings.Split(body, "\n")
	var secs []section
	var cur strings.Builder
	title := ""
	flush := func() {
		raw := cur.String()
		if strings.TrimSpace(raw) == "" {
			return
		}
		secs = append(secs, section{title: title, body: raw, raw: raw})
		cur.Reset()
		title = ""
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			title = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			cur.WriteString(line)
			cur.WriteByte('\n')
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
	}
	flush()
	return secs
}

func firstLine(s string) string { return textutil.FirstLine(s, 160) }

func truncateBytes(s string, n int) string {
	return textutil.Truncate(s, n, "\n…[truncated by compact]")
}
