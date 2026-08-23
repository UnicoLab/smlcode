package cli

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

// StatusTracker keeps a compact Claude Code–style footer for one-shot runs
// (`slmcode run`, `slmcode chat`). The premium TUI uses Activity instead.
type StatusTracker struct {
	mu       sync.Mutex
	phase    string
	agent    string
	taskID   string
	message  string
	active   map[string]time.Time // agent key -> start
	done     int
	failed   int
	tokens   int
	started  time.Time
}

func NewStatusTracker() *StatusTracker {
	return &StatusTracker{
		active:  map[string]time.Time{},
		started: time.Now(),
	}
}

func (s *StatusTracker) Observe(e stream.Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Phase != "" {
		s.phase = e.Phase
	}
	if e.Agent != "" {
		s.agent = e.Agent
	}
	if e.TaskID != "" {
		s.taskID = e.TaskID
	}
	if e.Message != "" {
		s.message = e.Message
	}
	switch e.Kind {
	case stream.KindAgentStart:
		// Key on agent+task so agents that arrive without a TaskID still show
		// as active instead of leaving the footer stuck on "idle".
		if k := agentKey(e); k != "" {
			s.active[k] = time.Now()
		}
	case stream.KindAgentEnd:
		if k := agentKey(e); k != "" {
			delete(s.active, k)
		}
		// Classify by the event's own level. Substring-matching free prose
		// counted "no errors found" as a failure.
		switch e.Level {
		case stream.LevelError, stream.LevelProblem:
			s.failed++
		default:
			s.done++
		}
	case stream.KindToken:
		s.tokens++
		if t, ok := e.Data.(stream.Token); ok && t.Tokens > 0 {
			s.tokens = t.Tokens
		}
	}
	if e.Phase == "done" {
		s.active = map[string]time.Time{}
	}
}

func (s *StatusTracker) Footer() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := time.Since(s.started).Round(time.Second)
	agents := make([]string, 0, len(s.active))
	for id := range s.active {
		agents = append(agents, id)
	}
	sort.Strings(agents)
	active := "idle"
	if len(agents) > 0 {
		active = strings.Join(agents, " ")
	}
	phase := s.phase
	if phase == "" {
		phase = "idle"
	}
	tok := ""
	if s.tokens > 0 {
		tok = fmt.Sprintf("  tokens=%s", humanCount(s.tokens))
	}
	return fmt.Sprintf("%s  phase=%s  active=%s  done=%d  fail=%d%s  elapsed=%s",
		Dim("──"), Accent(phase), Cyan(active), s.done, s.failed, Dim(tok), Dim(elapsed.String()))
}

// eventIcon maps kind+level onto the leading marker. Errors are red whatever
// the kind — an agent that failed must never render with a green marker.
func eventIcon(kind, level string) string {
	switch level {
	case stream.LevelError, stream.LevelProblem:
		switch kind {
		case stream.KindAgentEnd:
			return Red("◂")
		case stream.KindAgentStart:
			return Red("▸")
		default:
			return Red("✖")
		}
	case stream.LevelWarn:
		switch kind {
		case stream.KindAgentEnd:
			return Yellow("◂")
		default:
			return Yellow("⚠")
		}
	}
	switch kind {
	case stream.KindAgentStart:
		return Blue("▸")
	case stream.KindAgentEnd:
		return Green("◂")
	case stream.KindCoord:
		return Magenta("◆")
	case stream.KindLearn:
		return Yellow("★")
	case stream.KindOutput:
		return Cyan("∴")
	case stream.KindFileChange:
		return Green("✎")
	case stream.KindTool:
		return Dim("⚙")
	case stream.KindLatency:
		return Yellow("⏱")
	case stream.KindUsage:
		return Cyan("$")
	case stream.KindDebug:
		return Dim("·")
	case stream.KindIntervention:
		return Yellow("⚠")
	case stream.KindLoop:
		return Yellow("↺")
	case stream.KindTurn:
		return Yellow("⟳")
	case stream.KindComposition:
		return Cyan("◇")
	case stream.KindToken:
		return Dim("›")
	case stream.KindAsk:
		return Yellow("?")
	}
	return Dim("·")
}

// FormatEvent renders a clean, Claude Code–inspired live event line.
func FormatEvent(e stream.Event) string {
	kind := e.Kind
	if kind == "" {
		kind = stream.KindPhase
	}
	icon := eventIcon(kind, e.Level)

	who := e.Phase
	if e.Agent != "" {
		who = "@" + e.Agent
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = kind
	}
	// Collapse noisy log spam into a single readable line.
	msg = collapseWhitespace(msg)
	msg = ClipWidth(msg, 120)
	switch e.Level {
	case stream.LevelError, stream.LevelProblem:
		msg = Red(msg)
	case stream.LevelWarn:
		msg = Yellow(msg)
	}

	head := fmt.Sprintf("%s %s %s", icon, Dim(who), msg)
	if e.TaskID != "" {
		head += " " + Accent(e.TaskID)
	}
	if e.Scope != "" && kind == stream.KindAgentStart {
		head += "\n  " + Dim("scope") + " " + Cyan(shortScope(e.Scope))
	}

	// Show short output snippets only for completions / file patches — not every token dump.
	if kind == stream.KindAgentEnd || kind == stream.KindOutput || kind == stream.KindFileChange ||
		kind == stream.KindIntervention || kind == stream.KindLoop || kind == stream.KindComposition {
		out := summarizeOutput(e.Output)
		if out != "" {
			head += "\n  " + Dim("│ ") + White(out)
		}
	}
	return head
}

// PrintEvent writes a formatted live event to stdout.
func PrintEvent(e stream.Event) {
	fmt.Println(FormatEvent(e))
}

// PrintEventWithStatus prints the event and a sticky-looking status footer.
func PrintEventWithStatus(e stream.Event, st *StatusTracker) {
	if st != nil {
		st.Observe(e)
	}
	if e.Kind == stream.KindToken {
		return // token deltas belong to the live renderer, not the transcript
	}
	line := FormatEvent(e)
	fmt.Println(line)
	if st != nil && (e.Kind == stream.KindAgentStart || e.Kind == stream.KindAgentEnd || e.Phase == "done" || e.Phase == "execute") {
		fmt.Println(st.Footer())
	}
}

func collapseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

func shortScope(scope string) string {
	parts := strings.Split(scope, ",")
	if len(parts) <= 3 {
		return collapseWhitespace(scope)
	}
	return collapseWhitespace(strings.Join(parts[:3], ",")) + "…"
}

func summarizeOutput(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	lower := strings.ToLower(out)
	// Prefer JSON status line when present.
	if i := strings.Index(out, `"status"`); i >= 0 {
		return collapseWhitespace(ClipWidth(out[i:], 160))
	}
	if strings.Contains(lower, "approved") || strings.Contains(lower, "rejected") {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				return ClipWidth(line, 140)
			}
		}
	}
	// Default: first non-empty line only (avoid log walls).
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return ClipWidth(line, 140)
	}
	return ""
}
