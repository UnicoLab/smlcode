package compact

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Result of a context compaction pass.
type Result struct {
	BeforeBytes int
	AfterBytes  int
	Compacted   bool
	Summary     string
}

// NeedsCompact reports whether body exceeds soft/hard budgets.
func NeedsCompact(body string, softKB, hardKB int) bool {
	if hardKB <= 0 {
		hardKB = 48
	}
	if softKB <= 0 {
		softKB = hardKB * 3 / 4
	}
	n := len(body)
	return n > softKB*1024
}

// HeuristicSummarize compresses markdown context without an LLM.
// Keeps Locked PRD / headings / recent sections; drops older wave noise.
func HeuristicSummarize(body string, maxBytes int) Result {
	body = strings.TrimSpace(body)
	before := len(body)
	if maxBytes <= 0 {
		maxBytes = 24 * 1024
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

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if utf8.RuneCountInString(s) > 160 {
		r := []rune(s)
		return string(r[:160]) + "…"
	}
	return s
}

func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…[truncated by compact]"
}
