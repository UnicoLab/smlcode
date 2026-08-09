package compact

import (
	"fmt"
	"strings"
)

// DefaultCompactAtPercent matches little-coder's mid-run context watchdog (#59).
const DefaultCompactAtPercent = 80

// MinProgressPct is the hysteresis band: a compaction must open at least this
// many percentage points of headroom or auto-compact pauses (#68).
const MinProgressPct = 5

// ResumeMessage is injected after mid-run conversation compaction so the agent
// continues instead of idling (little-coder RESUME_MESSAGE).
const ResumeMessage = "Your context was automatically compacted mid-task to stay " +
	"within the model's window. Continue the task from where you left off — the " +
	"summary above preserves the work done so far. Do not restart from scratch or " +
	"re-ask the user; just carry on. Prefer editing files you already know over " +
	"re-scanning the whole project."

// ChatMsg is a role/content digest used for conversation compaction.
type ChatMsg struct {
	Role    string
	Content string
}

// Watchdog decides when to compact a ReAct transcript mid-run.
type Watchdog struct {
	ThresholdPercent int
	paused           bool
}

// NewWatchdog returns a watchdog; percent <=0 or >=100 disables compaction.
func NewWatchdog(percent int) *Watchdog {
	if percent <= 0 {
		percent = 0
	}
	if percent >= 100 {
		percent = 0
	}
	if percent == 0 {
		return &Watchdog{ThresholdPercent: 0}
	}
	return &Watchdog{ThresholdPercent: percent}
}

// EstimateTokens approximates tokens from UTF-8 bytes (~4 chars/token).
func EstimateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

// MessagesBytes returns total content bytes in msgs.
func MessagesBytes(msgs []ChatMsg) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Role) + len(m.Content)
	}
	return n
}

// UsagePercent reports tokens as a percent of the context window.
func UsagePercent(tokens, windowTokens int) float64 {
	if windowTokens <= 0 || tokens < 0 {
		return 0
	}
	return float64(tokens) * 100 / float64(windowTokens)
}

// ShouldCompactNow is the pure decision (little-coder shouldCompactNow).
func ShouldCompactNow(usagePercent float64, thresholdPercent int, compacting, paused bool) bool {
	if thresholdPercent <= 0 || thresholdPercent >= 100 {
		return false
	}
	if compacting || paused {
		return false
	}
	return usagePercent >= float64(thresholdPercent)
}

// CompactionHelped reports whether post-compact usage opened enough headroom.
func CompactionHelped(postPercent float64, thresholdPercent int) bool {
	if thresholdPercent <= 0 {
		return true
	}
	return postPercent <= float64(thresholdPercent-MinProgressPct)
}

// ShouldCompact reports whether this watchdog should fire for the given usage.
func (w *Watchdog) ShouldCompact(usagePercent float64) bool {
	if w == nil {
		return false
	}
	return ShouldCompactNow(usagePercent, w.ThresholdPercent, false, w.paused)
}

// RecordPostCompact updates pause state after a compaction (#68).
func (w *Watchdog) RecordPostCompact(postPercent float64) {
	if w == nil {
		return
	}
	if !CompactionHelped(postPercent, w.ThresholdPercent) {
		w.paused = true
		return
	}
	w.paused = false
}

// MaybeRearm clears pause when usage drops below the hysteresis band.
func (w *Watchdog) MaybeRearm(usagePercent float64) {
	if w == nil || !w.paused {
		return
	}
	if usagePercent < float64(w.ThresholdPercent-MinProgressPct) {
		w.paused = false
	}
}

// CompactChatMessages keeps the last N messages plus a digest of the prefix.
func CompactChatMessages(msgs []ChatMsg, keepLast int) ([]ChatMsg, bool) {
	if keepLast <= 0 {
		keepLast = 8
	}
	if len(msgs) <= keepLast {
		return msgs, false
	}
	dropped := msgs[:len(msgs)-keepLast]
	var parts []string
	for _, m := range dropped {
		line := strings.TrimSpace(m.Role + ": " + firstLine(m.Content))
		if line != "" {
			parts = append(parts, line)
		}
	}
	digest := fmt.Sprintf("[compacted %d earlier messages] %s",
		len(dropped), firstLine(strings.Join(parts, " | ")))
	out := make([]ChatMsg, 0, keepLast+2)
	out = append(out, ChatMsg{Role: "system", Content: digest})
	out = append(out, msgs[len(msgs)-keepLast:]...)
	out = append(out, ChatMsg{Role: "user", Content: ResumeMessage})
	return out, true
}

// WindowTokensFromKB converts a context KB budget into an approximate token window.
func WindowTokensFromKB(kb int) int {
	if kb <= 0 {
		kb = 16
	}
	return EstimateTokens(kb * 1024)
}
