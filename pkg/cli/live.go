package cli

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

// StatusTracker keeps a compact Claude Code–style footer for TUI runs.
type StatusTracker struct {
	mu       sync.Mutex
	phase    string
	agent    string
	taskID   string
	message  string
	active   map[string]string // taskID -> agent
	done     int
	failed   int
	started  time.Time
	lastLine string
}

func NewStatusTracker() *StatusTracker {
	return &StatusTracker{
		active:  map[string]string{},
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
		if e.TaskID != "" {
			s.active[e.TaskID] = e.Agent
		}
	case stream.KindAgentEnd:
		if e.TaskID != "" {
			delete(s.active, e.TaskID)
		}
		lower := strings.ToLower(e.Message + " " + e.Output)
		if strings.Contains(lower, "error") || strings.Contains(lower, "blocked") || strings.Contains(lower, "rejected") {
			s.failed++
		} else if strings.Contains(lower, "approved") || strings.Contains(lower, "done") || strings.Contains(lower, "finished") {
			s.done++
		}
	}
	if e.Phase == "done" {
		s.active = map[string]string{}
	}
}

func (s *StatusTracker) Footer() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := time.Since(s.started).Round(time.Second)
	var agents []string
	for id, ag := range s.active {
		if ag == "" {
			agents = append(agents, id)
		} else {
			agents = append(agents, "@"+ag+":"+id)
		}
	}
	active := "idle"
	if len(agents) > 0 {
		active = strings.Join(agents, " ")
	}
	phase := s.phase
	if phase == "" {
		phase = "idle"
	}
	return fmt.Sprintf("%s  phase=%s  active=%s  done=%d  fail=%d  elapsed=%s",
		Dim("──"), Accent(phase), Cyan(active), s.done, s.failed, Dim(elapsed.String()))
}

// FormatEvent renders a clean, Claude Code–inspired live event line.
func FormatEvent(e stream.Event) string {
	kind := e.Kind
	if kind == "" {
		kind = stream.KindPhase
	}

	// Compact one-line primary; details indented only when useful.
	icon := Dim("·")
	switch kind {
	case stream.KindAgentStart:
		icon = Blue("▸")
	case stream.KindAgentEnd:
		icon = Green("◂")
	case stream.KindCoord:
		icon = Magenta("◆")
	case stream.KindLearn:
		icon = Yellow("★")
	case stream.KindOutput:
		icon = Cyan("∴")
	case stream.KindFileChange:
		icon = Green("✎")
	case stream.KindTool:
		icon = Dim("⚙")
	}

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
	if len(msg) > 120 {
		msg = msg[:117] + "…"
	}

	head := fmt.Sprintf("%s %s %s", icon, Dim(who), msg)
	if e.TaskID != "" {
		head += " " + Accent(e.TaskID)
	}
	if e.Scope != "" && kind == stream.KindAgentStart {
		head += "\n  " + Dim("scope") + " " + Cyan(shortScope(e.Scope))
	}

	// Show short output snippets only for completions / file patches — not every token dump.
	if kind == stream.KindAgentEnd || kind == stream.KindOutput || kind == stream.KindFileChange {
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
		snippet := out[i:]
		if len(snippet) > 160 {
			snippet = snippet[:160] + "…"
		}
		return collapseWhitespace(snippet)
	}
	if strings.Contains(lower, "approved") || strings.Contains(lower, "rejected") {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				if len(line) > 140 {
					return line[:137] + "…"
				}
				return line
			}
		}
	}
	// Default: first non-empty line only (avoid log walls).
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 140 {
			return line[:137] + "…"
		}
		return line
	}
	return ""
}
