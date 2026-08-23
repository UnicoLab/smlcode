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
//
// ToolCalls / ToolCallID / Name were added so compaction can round-trip an
// OpenAI-style tool exchange. Flattening tool calls into text (the old
// behavior) made it impossible to restore a valid transcript, and the kept
// tail routinely began on a role:"tool" message — which every
// OpenAI-compatible server rejects with HTTP 400.
type ChatMsg struct {
	Role       string
	Content    string
	Name       string        `json:"name,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCallRef `json:"tool_calls,omitempty"`
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

// MessagesBytes returns total prompt bytes in msgs, including tool-call
// payloads. Tool arguments and tool-call ids are real prompt bytes; counting
// only Role+Content under-reports a tool-heavy transcript badly enough that the
// watchdog fires far too late.
func MessagesBytes(msgs []ChatMsg) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Role) + len(m.Content) + len(m.Name) + len(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			n += len(tc.ID) + len(tc.Type) + len(tc.Name) + len(tc.Arguments)
		}
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

// CompactChatMessages keeps a tool-pair-safe tail plus a STRUCTURED digest of
// the dropped prefix.
//
// Two default-on behaviors differ from the historical implementation:
//
//  1. The kept window is widened backwards until it starts on a boundary that
//     orphans no tool result (see SafeKeepStart), so the result is always a
//     transcript an OpenAI-compatible server accepts.
//  2. The prefix is summarized with the MustPreserve schema (files read/edited,
//     commands + exit status, failed tool calls, decisions) rather than a
//     160-rune first line of everything joined together.
func CompactChatMessages(msgs []ChatMsg, keepLast int) ([]ChatMsg, bool) {
	return CompactChatMessagesWithDigest(msgs, keepLast, DefaultDigestBytes)
}

// CompactChatMessagesWithDigest is CompactChatMessages with an explicit digest
// byte budget.
func CompactChatMessagesWithDigest(msgs []ChatMsg, keepLast, digestBytes int) ([]ChatMsg, bool) {
	if keepLast <= 0 {
		keepLast = 8
	}
	if len(msgs) <= keepLast {
		return msgs, false
	}
	start := SafeKeepStart(msgs, keepLast)
	if start <= 0 {
		// Nothing can be dropped without corrupting the transcript.
		return msgs, false
	}
	dropped := msgs[:start]
	out := make([]ChatMsg, 0, len(msgs)-start+2)
	out = append(out, ChatMsg{Role: RoleSystem, Content: DigestOrFallback(dropped, digestBytes)})
	out = append(out, msgs[start:]...)
	out = append(out, ChatMsg{Role: RoleUser, Content: ResumeMessage})
	return out, true
}

// DigestOrFallback renders the structured digest for dropped messages, falling
// back to the historical one-line summary only when extraction yields nothing.
func DigestOrFallback(dropped []ChatMsg, digestBytes int) string {
	d := BuildDigest(dropped)
	if !d.Empty() {
		return d.Render(digestBytes)
	}
	var parts []string
	for _, m := range dropped {
		line := strings.TrimSpace(m.Role + ": " + firstLine(m.Content))
		if line != "" {
			parts = append(parts, line)
		}
	}
	return fmt.Sprintf("[compacted %d earlier messages] %s",
		len(dropped), firstLine(strings.Join(parts, " | ")))
}

// WindowTokensFromKB converts a context KB budget into an approximate token
// window.
//
// Deprecated: a KB figure is a PROMPT BYTE budget, not the model's context
// window. Treating WindowTokensFromKB(16)=4096 as a Qwen-32B's whole window
// makes the watchdog think a 32768-token model is at 80% capacity with ~3K
// tokens in hand. Use WindowTokensFor with the model profile's ContextLimit.
func WindowTokensFromKB(kb int) int {
	if kb <= 0 {
		kb = 16
	}
	return EstimateTokens(kb * 1024)
}

// WindowTokensFor returns the real per-model context window in tokens.
// contextLimitTokens is config.ModelProfile.ContextLimit; maxContextKB is the
// legacy prompt-byte budget used only when no model limit is known.
func WindowTokensFor(contextLimitTokens, maxContextKB int) int {
	if contextLimitTokens > 0 {
		return contextLimitTokens
	}
	return WindowTokensFromKB(maxContextKB)
}
