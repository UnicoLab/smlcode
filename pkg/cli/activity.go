package cli

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

// Activity is the single sticky status line: spinner, phase, active agents,
// elapsed, token count and estimated cost.
//
// The old StatusTracker inferred done/failed by substring-matching "error" and
// "done" in free agent prose (so "no errors found" counted as a failure) and
// showed active=idle while agents were running because AgentStart events with
// no TaskID never populated the map. Activity keys on Event.Level and Agent so
// neither happens.
type Activity struct {
	mu sync.Mutex

	started  time.Time
	running  bool
	phase    string
	active   map[string]time.Time // "agent:task" (or "agent") -> start
	done     int
	failed   int
	tokens   int
	costUSD  float64
	lastLine string // most recent token-stream fragment
	frame    int
	note     string
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewActivity creates a fresh indicator.
func NewActivity() *Activity {
	return &Activity{active: map[string]time.Time{}, started: time.Now()}
}

// Start marks a run as in flight and resets the counters.
func (a *Activity) Start() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = time.Now()
	a.running = true
	a.phase = ""
	a.active = map[string]time.Time{}
	a.done, a.failed, a.tokens = 0, 0, 0
	a.costUSD = 0
	a.lastLine = ""
	a.note = ""
}

// Stop marks the run finished; the line keeps the final counters.
func (a *Activity) Stop(note string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = false
	a.active = map[string]time.Time{}
	a.note = note
}

// Running reports whether a run is in flight.
func (a *Activity) Running() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// SetNote sets a short trailing note (e.g. "interrupted").
func (a *Activity) SetNote(s string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.note = s
	a.mu.Unlock()
}

// agentKey builds a stable identity for an event's agent, even when the engine
// omits the task id (which is exactly the case that used to show "idle").
func agentKey(e stream.Event) string {
	switch {
	case e.Agent != "" && e.TaskID != "":
		return "@" + e.Agent + ":" + e.TaskID
	case e.Agent != "":
		return "@" + e.Agent
	case e.TaskID != "":
		return e.TaskID
	case e.Phase != "":
		return e.Phase
	}
	return ""
}

// Observe folds one event into the indicator.
func (a *Activity) Observe(e stream.Event) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e.Phase != "" {
		a.phase = e.Phase
	}
	switch e.Kind {
	case stream.KindAgentStart:
		if k := agentKey(e); k != "" {
			a.active[k] = time.Now()
			a.running = true
		}
	case stream.KindAgentEnd:
		if k := agentKey(e); k != "" {
			delete(a.active, k)
		}
		// Level is authoritative — never guess from prose.
		switch e.Level {
		case stream.LevelError, stream.LevelProblem:
			a.failed++
		default:
			a.done++
		}
	case stream.KindToken:
		a.tokens++
		if t, ok := e.Data.(stream.Token); ok {
			if t.Tokens > 0 {
				a.tokens = t.Tokens
			}
			a.lastLine = lastLineOf(a.lastLine + t.Delta)
		} else if e.Message != "" {
			a.lastLine = lastLineOf(a.lastLine + e.Message)
		}
	case stream.KindUsage:
		if n, cost, ok := parseUsage(e.Message); ok {
			if n > 0 {
				a.tokens = n
			}
			if cost > 0 {
				a.costUSD = cost
			}
		}
	}
	if e.Phase == "done" {
		a.running = false
		a.active = map[string]time.Time{}
	}
}

func lastLineOf(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	return s
}

// parseUsage extracts a token count and dollar cost from a usage summary line
// such as "tokens=1234 prompt=900 completion=334 cost=$0.0021".
func parseUsage(msg string) (tokens int, cost float64, ok bool) {
	for _, f := range strings.Fields(msg) {
		k, v, found := strings.Cut(f, "=")
		if !found {
			continue
		}
		switch strings.ToLower(k) {
		case "tokens", "total", "total_tokens":
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				tokens, ok = n, true
			}
		case "cost", "usd", "$":
			v = strings.TrimPrefix(v, "$")
			var c float64
			if _, err := fmt.Sscanf(v, "%f", &c); err == nil {
				cost, ok = c, true
			}
		}
	}
	return tokens, cost, ok
}

// ActiveAgents returns the currently running agent labels, sorted.
func (a *Activity) ActiveAgents() []string {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.active))
	for k := range a.active {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Tick advances the spinner frame.
func (a *Activity) Tick() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.frame++
	a.mu.Unlock()
}

// LastTokenLine returns the tail of the live token stream.
func (a *Activity) LastTokenLine() string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLine
}

// Line renders the sticky status line clipped to width.
func (a *Activity) Line(width int) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	var head string
	if a.running {
		head = Accent(spinnerFrames[a.frame%len(spinnerFrames)])
	} else {
		head = Dim("·")
	}
	phase := a.phase
	if phase == "" {
		if a.running {
			phase = "starting"
		} else {
			phase = "ready"
		}
	}
	// active never lies: derived from the live agent set, not a guess.
	active := ""
	switch {
	case len(a.active) == 1:
		for k := range a.active {
			active = k
		}
	case len(a.active) > 1:
		keys := make([]string, 0, len(a.active))
		for k := range a.active {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		active = fmt.Sprintf("%s +%d", keys[0], len(keys)-1)
	case a.running:
		active = "thinking"
	default:
		active = "idle"
	}

	parts := []string{
		head + " " + Accent(phase),
		Cyan(active),
	}
	if a.done > 0 || a.failed > 0 {
		seg := Green(fmt.Sprintf("✔%d", a.done))
		if a.failed > 0 {
			seg += " " + Red(fmt.Sprintf("✖%d", a.failed))
		}
		parts = append(parts, seg)
	}
	if a.tokens > 0 {
		parts = append(parts, Dim(fmt.Sprintf("%s tok", humanCount(a.tokens))))
	}
	if a.costUSD > 0 {
		parts = append(parts, Dim(fmt.Sprintf("$%.4f", a.costUSD)))
	}
	elapsed := time.Since(a.started).Round(time.Second)
	if !a.running && a.started.IsZero() {
		elapsed = 0
	}
	parts = append(parts, Dim(elapsed.String()))
	if a.note != "" {
		parts = append(parts, Yellow(a.note))
	}
	line := "  " + strings.Join(parts, Dim(" · "))
	return ClipWidth(line, width)
}

func humanCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}
