package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// DashboardState is everything the premium TUI renders (parity with Studio + more).
type DashboardState struct {
	Root            string
	Provider        string
	Model           string
	Endpoint        string
	Backend         string
	Permission      string
	ShellPermission string
	Compact         bool
	Running         bool
	Phase           string
	Query           string
	Board           *plan.Board
	Events          []stream.Event
	Agents          []string // active "@agent:task"
	ErrorsHead      string
	DiffHead        string
	Queries         []string // recent query turn ids / titles
	LatencyHead     string   // last-run phase latency summary
	UsageHead       string   // last-run token/cost summary
	TurnHead        string   // turn budget meter (e.g. turn 12/16)
	Intervention    string   // latest harness intervention banner
	ProgressHead    string   // per-task progress strip
	Settings        string
	Message         string
	Probe           ProbeResult // connection health driving the dot
	PendingGate     string      // one-line description of a waiting HITL gate
}

// IsInteractive reports whether stdin is a TTY suitable for the premium TUI.
func IsInteractive() bool {
	if os.Getenv("SLMCODE_TUI") == "0" || os.Getenv("CI") == "true" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// RenderDashboard paints the multi-panel status view.
//
// It is *append-only*: nothing clears the screen, so scrollback keeps what the
// agents said. Width is clamped to [40,120]; below 70 columns it degrades to a
// single-column layout instead of wrapping into confetti.
func RenderDashboard(w io.Writer, st DashboardState) {
	if w == nil {
		w = os.Stdout
	}
	width, _ := TermSize()
	inner := width - 2
	bar := strings.Repeat("─", inner)
	narrow := NarrowLayout(width)

	// Terminal paint writes: nothing actionable can be done with a write
	// failure mid-dashboard render, so these are intentionally ignored.
	row := func(s string) {
		_, _ = fmt.Fprintln(w, Accent("│")+PadWidth(s, inner)+Accent("│"))
	}
	sep := func() { _, _ = fmt.Fprintln(w, Accent("├"+bar+"┤")) }

	_, _ = fmt.Fprintln(w, Accent("┌"+bar+"┐"))
	title := Bold(" SLMCODE ") + Dim("premium TUI") + "  " + Cyan(shortPath(st.Root))
	row(title)
	sep()

	dot := st.Probe.Dot()
	if st.Probe.State == ProbeUnknown && st.Probe.CheckedAt.IsZero() {
		dot = Dim("○")
	}
	if narrow {
		row(fmt.Sprintf(" %s %s", dot, White(st.Provider)))
		row(" " + Accent(ClipWidth(st.Model, inner-2)))
		row(" " + Dim(ClipWidth(st.Endpoint, inner-2)))
	} else {
		// Build the row from fixed-cost trailing badges first, then spend the
		// remaining budget on the model id and endpoint so nothing important
		// gets sliced off at the border.
		var badges string
		switch st.Probe.State {
		case ProbeDown:
			badges += "  " + Red("offline")
		case ProbeDegrade:
			badges += "  " + Yellow("degraded")
		}
		if st.Running {
			badges += "  " + Yellow("▶ RUN")
		} else {
			badges += "  " + Dim("idle")
		}
		if st.Phase != "" {
			badges += "  phase=" + Cyan(st.Phase)
		}
		if st.TurnHead != "" {
			badges += "  " + Yellow("⟳ "+st.TurnHead)
		}
		head := fmt.Sprintf(" %s  ", dot+" "+White(st.Provider))
		// head + model + "/" + backend + "  " + endpoint + badges must fit.
		budget := inner - VisibleWidth(head) - VisibleWidth(badges) -
			StringWidth(st.Backend) - 3
		modelW, endpointW := 0, 0
		if budget > 8 {
			modelW = budget * 3 / 5
			endpointW = budget - modelW
		}
		conn := head + Accent(ClipWidth(st.Model, modelW)) + "/" + Dim(st.Backend)
		if endpointW > 6 {
			conn += "  " + Dim(ClipWidth(st.Endpoint, endpointW))
		}
		conn += badges
		row(conn)
	}
	sep()

	// Board columns strip
	counts := map[string]int{}
	total, doneN := 0, 0
	if st.Board != nil {
		for _, t := range st.Board.Tasks {
			t.Normalize()
			counts[t.Column]++
			total++
			if t.Column == plan.ColDone {
				doneN++
			}
		}
	}
	prog := " board "
	for _, col := range []string{"to_scope", "scoped", "ready_to_dev", "in_progress", "in_review", "done", "blocked"} {
		n := counts[col]
		if n == 0 {
			continue
		}
		prog += ColumnColor(col) + fmt.Sprintf(":%d ", n)
	}
	if total > 0 {
		pct := doneN * 100 / total
		prog += Dim(fmt.Sprintf("· %d/%d (%d%%)", doneN, total, pct))
	} else {
		prog += Dim("· empty — run a query to populate")
	}
	row(prog)

	active := " agents "
	if len(st.Agents) == 0 {
		active += Dim("none active")
	} else {
		active += Cyan(ClipWidth(strings.Join(st.Agents, "  "), inner-9))
	}
	row(active)
	if st.ProgressHead != "" {
		row(" progress " + Dim(ClipWidth(st.ProgressHead, inner-11)))
	}
	if st.PendingGate != "" {
		row(Yellow(" ⏸ waiting ") + White(ClipWidth(st.PendingGate, inner-12)))
	}
	if st.Intervention != "" {
		row(Yellow(" ⚠ ") + White(ClipWidth(st.Intervention, inner-4)))
	}
	sep()

	// Tasks panel
	row(Bold(" Tasks"))
	shown := 0
	if st.Board != nil {
		for _, t := range st.Board.Tasks {
			if shown >= 8 {
				break
			}
			t.Normalize()
			var line string
			if narrow {
				line = fmt.Sprintf("  %s %s", Accent(t.ID), ClipWidth(t.Title, inner-10))
			} else {
				line = fmt.Sprintf("  %s %s @%s %s",
					Accent(t.ID), PadWidth(ColumnColor(t.Column), 12),
					PadWidth(Dim(t.Role), 10), ClipWidth(t.Title, maxInt(8, inner-40)))
			}
			row(line)
			shown++
		}
	}
	if shown == 0 {
		row(Dim("  (no tasks yet)"))
	}

	sep()
	row(Bold(" Live"))
	maxLive := 10
	if st.Compact {
		maxLive = 8
	}
	if narrow {
		maxLive = 5
	}
	evStart := 0
	if len(st.Events) > maxLive {
		evStart = len(st.Events) - maxLive
	}
	if len(st.Events) == 0 {
		row(Dim("  waiting for events…"))
	}
	for _, e := range st.Events[evStart:] {
		line := "  " + FormatEvent(e)
		if i := strings.Index(line, "\n"); i >= 0 {
			line = line[:i]
		}
		row(ClipWidth(line, inner))
	}

	if strings.TrimSpace(st.ErrorsHead) != "" {
		sep()
		row(Red(" Errors ") + Dim(ClipWidth(st.ErrorsHead, inner-9)))
	}
	if strings.TrimSpace(st.DiffHead) != "" {
		row(Green(" Diff ") + Dim(ClipWidth(st.DiffHead, inner-7)))
	}
	if len(st.Queries) > 0 {
		row(Blue(" Queries ") + Dim(ClipWidth(strings.Join(st.Queries, " · "), inner-10)))
	}
	if strings.TrimSpace(st.LatencyHead) != "" {
		row(Yellow(" Latency ") + Dim(ClipWidth(st.LatencyHead, inner-10)))
	}
	if strings.TrimSpace(st.UsageHead) != "" {
		row(Cyan(" Tokens ") + Dim(ClipWidth(st.UsageHead, inner-9)))
	}

	sep()
	help := Dim(" keys ") + White("[enter]") + Dim(" run  ") +
		White("[esc]") + Dim(" interrupt  ") +
		White("?") + Dim(" help  ") +
		White("/") + Dim(" commands  ") +
		White("/q") + Dim(" quit")
	row(help)
	if st.Message != "" {
		row(" " + Yellow(ClipWidth(st.Message, inner-2)))
	}
	_, _ = fmt.Fprintln(w, Accent("└"+bar+"┘"))
	if st.Query != "" {
		_, _ = fmt.Fprintln(w, Dim(" last query: ")+ClipWidth(st.Query, width-14))
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// PrintStaticDashboard is the non-interactive / CI fallback.
func PrintStaticDashboard(st DashboardState) {
	fmt.Println(Banner())
	fmt.Println(Bold("Connection"))
	KeyVal("root", st.Root)
	KeyVal("provider", st.Provider)
	KeyVal("model", st.Model)
	KeyVal("endpoint", st.Endpoint)
	KeyVal("backend", st.Backend)
	KeyVal("permission", st.Permission)
	if st.ShellPermission != "" {
		KeyVal("shell", st.ShellPermission)
	}
	if st.Compact {
		KeyVal("compact", "on")
	}
	fmt.Println()
	RenderBoardGlance(st.Board)
	fmt.Println()
	fmt.Println(Dim("Interactive TUI needs a terminal. Try:"))
	fmt.Println(Cyan("  slmcode") + Dim("           # premium TUI (default)"))
	fmt.Println(Cyan("  slmcode chat") + Dim("      # REPL"))
	fmt.Println(Cyan("  slmcode studio") + Dim("    # browser GUI"))
	fmt.Println(Cyan("  slmcode run \"…\"") + Dim("  # one-shot pipeline"))
	fmt.Println(Dim("Also spelled ") + Accent("smlcode") + Dim(" in some docs — binary is ") + Bold("slmcode") + Dim("."))
}

// RenderBoardGlance prints a compact board summary without clearing the screen.
func RenderBoardGlance(board *plan.Board) {
	fmt.Println(Bold("Board"))
	if board == nil || len(board.Tasks) == 0 {
		fmt.Println(Dim("  (empty)"))
		return
	}
	by := board.ByColumn()
	for _, col := range plan.Columns() {
		tasks := by[col]
		if len(tasks) == 0 {
			continue
		}
		fmt.Printf("  %s  %s\n", ColumnColor(PadWidth(col, 14)), Bold(fmt.Sprintf("%d", len(tasks))))
		for i, t := range tasks {
			if i >= 3 {
				fmt.Println(Dim(fmt.Sprintf("    … +%d more", len(tasks)-3)))
				break
			}
			fmt.Printf("    %s @%s  %s\n", Accent(t.ID), t.Role, Clip(t.Title, 56))
		}
	}
}

// ── Interactive session ──────────────────────────────────────────────────────

// LiveSession drives the interactive premium TUI REPL.
//
// Input, the run, and rendering each live on their own goroutine and meet in a
// select loop, so every steering command works *while an agent is running*.
type LiveSession struct {
	mu      sync.Mutex
	state   DashboardState
	act     *Activity
	history *PromptHistory
	ed      *LineEditor
	console *Console
	slash   *SlashRegistry

	onRun          func(query string) error
	onStop         func()
	onSlash        func(cmd string) (quit bool, err error)
	onLiveSlash    func(cmd string) (quit bool, err error)
	onSteer        func(text string)
	onBoardRefresh func() *plan.Board

	gate      *Gate
	gateReply chan GateAnswer

	in       io.Reader
	out      io.Writer
	tty      bool
	showDash bool

	wakeCh chan struct{}
}

// NewLiveSession constructs a TUI session. Call SetState / Observe as events arrive.
func NewLiveSession() *LiveSession {
	hist := LoadPromptHistory(DefaultPromptHistoryPath())
	width, _ := TermSize()
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	return &LiveSession{
		act:      NewActivity(),
		history:  hist,
		ed:       NewLineEditor(hist),
		console:  NewConsole(os.Stdout, width, tty),
		in:       os.Stdin,
		out:      os.Stdout,
		tty:      tty,
		showDash: true,
		state:    DashboardState{Compact: true},
		wakeCh:   make(chan struct{}, 1),
	}
}

// SetIO overrides the input/output streams (tests, embedded hosts).
func (s *LiveSession) SetIO(in io.Reader, out io.Writer, sticky bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.in = in
	s.out = out
	s.tty = sticky
	width, _ := TermSize()
	s.console = NewConsole(out, width, sticky)
}

// SetShowDashboard controls whether the boxed dashboard is painted on start
// and on Ctrl-L. The classic `chat` REPL turns it off for a plain transcript.
func (s *LiveSession) SetShowDashboard(on bool) {
	s.mu.Lock()
	s.showDash = on
	s.mu.Unlock()
}

// ShowDashboard reports whether the boxed dashboard is enabled.
func (s *LiveSession) ShowDashboard() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.showDash
}

// SetSlashRegistry installs the command catalog used for help, the `/` picker
// and Tab completion.
func (s *LiveSession) SetSlashRegistry(r *SlashRegistry) {
	s.mu.Lock()
	s.slash = r
	s.mu.Unlock()
}

// Console exposes the transcript writer so callers can print above the footer.
func (s *LiveSession) Console() *Console { return s.console }

// History returns the session prompt history (may be nil).
func (s *LiveSession) History() *PromptHistory {
	if s == nil {
		return nil
	}
	return s.history
}

// Activity returns the live activity indicator.
func (s *LiveSession) Activity() *Activity { return s.act }

// SetProbe records the latest endpoint probe (drives the connection dot).
func (s *LiveSession) SetProbe(p ProbeResult) {
	s.mu.Lock()
	s.state.Probe = p
	s.mu.Unlock()
	s.wake()
}

// ClearLive resets live stream state (events, agents, banners) without quitting.
func (s *LiveSession) ClearLive() {
	s.mu.Lock()
	s.state.Events = nil
	s.state.Agents = nil
	s.state.Intervention = ""
	s.state.TurnHead = ""
	s.state.ProgressHead = ""
	s.state.Message = "cleared"
	s.state.Running = false
	s.mu.Unlock()
	s.act = NewActivity()
}

// OnBoardRefresh registers a callback used to refresh the board mid-run.
func (s *LiveSession) OnBoardRefresh(fn func() *plan.Board) {
	s.mu.Lock()
	s.onBoardRefresh = fn
	s.mu.Unlock()
}

func (s *LiveSession) SetState(st DashboardState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The sticky line reports the model's measured decode rate; this is the
	// only place the session learns which model is active.
	s.act.SetModel(st.Model)
	// preserve events / latency / compact if caller omitted
	if st.Events == nil {
		st.Events = s.state.Events
	}
	if st.LatencyHead == "" {
		st.LatencyHead = s.state.LatencyHead
	}
	if st.UsageHead == "" {
		st.UsageHead = s.state.UsageHead
	}
	if st.TurnHead == "" {
		st.TurnHead = s.state.TurnHead
	}
	if st.Intervention == "" {
		st.Intervention = s.state.Intervention
	}
	if st.ProgressHead == "" {
		st.ProgressHead = s.state.ProgressHead
	}
	if st.Probe.CheckedAt.IsZero() {
		st.Probe = s.state.Probe
	}
	if !st.Compact && s.state.Compact {
		st.Compact = true
	}
	st.PendingGate = s.state.PendingGate
	s.state = st
}

// State returns a copy of the dashboard state.
func (s *LiveSession) State() DashboardState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Observe folds a live event into the session: it appends a transcript line
// (never destroying scrollback) and refreshes the sticky footer.
func (s *LiveSession) Observe(e stream.Event) {
	s.act.Observe(e)

	if e.Kind == stream.KindDebug {
		return
	}
	if e.Kind == stream.KindToken {
		s.wake()
		return // rendered live in the footer, not the transcript
	}

	s.mu.Lock()
	if s.state.Compact && e.Kind == stream.KindOutput {
		s.mu.Unlock()
		s.wake()
		return
	}
	s.state.Events = append(s.state.Events, e)
	maxEv := 200
	if s.state.Compact {
		maxEv = 80
	}
	if len(s.state.Events) > maxEv {
		s.state.Events = s.state.Events[len(s.state.Events)-maxEv:]
	}
	if e.Phase != "" {
		s.state.Phase = e.Phase
	}
	refreshBoard := false
	switch e.Kind {
	case stream.KindAgentStart:
		if k := agentKey(e); k != "" {
			s.state.Agents = appendUnique(s.state.Agents, k)
			s.state.Running = true
		}
	case stream.KindAgentEnd:
		if k := agentKey(e); k != "" {
			var next []string
			for _, a := range s.state.Agents {
				if a != k {
					next = append(next, a)
				}
			}
			s.state.Agents = next
		}
		refreshBoard = true
	case stream.KindFileChange:
		refreshBoard = true
	case stream.KindPhase:
		if e.Phase == "plan" || e.Phase == "split" || e.Phase == "execute" || e.Phase == "test" {
			refreshBoard = true
		}
	case stream.KindLatency:
		if e.Message != "" {
			s.state.LatencyHead = e.Message
		}
	case stream.KindUsage:
		if e.Message != "" {
			s.state.UsageHead = e.Message
		}
	case stream.KindAsk:
		banner := e.Message
		if strings.Contains(strings.ToLower(e.Output), `"kind":"escalate"`) || e.Agent == "escalate" {
			banner = "ESCALATE — answer inline or /escalate re_scope|retry|mark_done|abort · " + e.Message
		} else if strings.Contains(strings.ToLower(e.Output), `"kind":"continue"`) || e.Agent == "continue" {
			banner = "CONTINUE? answer inline · " + e.Message
		}
		s.state.Intervention = banner
		s.state.Message = banner
	case stream.KindIntervention:
		banner := e.Message
		if e.Scope != "" {
			banner = "[" + e.Scope + "] " + banner
		}
		s.state.Intervention = banner
		s.state.Message = banner
	case stream.KindLoop:
		banner := "↺ " + e.Message
		if e.Scope != "" {
			banner = "↺ [" + e.Scope + "] " + e.Message
		}
		s.state.Intervention = banner
		s.state.Message = banner
		if e.Phase != "" {
			s.state.Phase = e.Phase
		}
	case stream.KindTurn:
		if e.Message != "" {
			s.state.TurnHead = e.Message
		}
		if e.Scope != "" && e.Message == "" {
			s.state.TurnHead = e.Scope
		}
	}
	parts := []string{}
	if s.state.Phase != "" {
		parts = append(parts, s.state.Phase)
	}
	if len(s.state.Agents) > 0 {
		parts = append(parts, strings.Join(s.state.Agents, ","))
	}
	if s.state.TurnHead != "" {
		parts = append(parts, s.state.TurnHead)
	}
	s.state.ProgressHead = strings.Join(parts, " · ")
	if e.Phase == "done" {
		s.state.Running = false
		s.state.Agents = nil
		s.state.TurnHead = ""
		refreshBoard = true
	}
	fn := s.onBoardRefresh
	s.mu.Unlock()

	s.console.Write(FormatEvent(e))

	if refreshBoard && fn != nil {
		if b := fn(); b != nil {
			s.mu.Lock()
			s.state.Board = b
			s.mu.Unlock()
		}
	}
	s.wake()
}

// SetCompact toggles compact live-event mode.
func (s *LiveSession) SetCompact(on bool) {
	s.mu.Lock()
	s.state.Compact = on
	s.mu.Unlock()
}

// Compact reports whether compact mode is enabled.
func (s *LiveSession) Compact() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Compact
}

// LatencyHead returns the last latency summary line.
func (s *LiveSession) LatencyHead() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LatencyHead
}

// UsageHead returns the last token/cost summary line.
func (s *LiveSession) UsageHead() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.UsageHead
}

func (s *LiveSession) OnRun(fn func(string) error) { s.onRun = fn }
func (s *LiveSession) OnStop(fn func())            { s.onStop = fn }
func (s *LiveSession) OnSlash(fn func(string) (bool, error)) {
	s.onSlash = fn
}

// OnLiveSlash registers the handler used for slash commands typed *during* a
// run. Falls back to OnSlash when unset.
func (s *LiveSession) OnLiveSlash(fn func(string) (bool, error)) { s.onLiveSlash = fn }

// OnSteer registers the sink for mid-run redirection text (Esc → "what should
// I change?", or any plain line typed while a run is in flight).
func (s *LiveSession) OnSteer(fn func(string)) { s.onSteer = fn }

// Print writes a line into the transcript above the sticky footer.
func (s *LiveSession) Print(a ...any) {
	s.console.Write(strings.TrimRight(fmt.Sprintln(a...), "\n"))
}

// Printf writes formatted text into the transcript.
func (s *LiveSession) Printf(format string, a ...any) {
	s.console.Write(fmt.Sprintf(format, a...))
}

func (s *LiveSession) wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// AskGate publishes a human-in-the-loop gate and blocks until the user answers
// or ctx is canceled. Called from the orchestrator's goroutine.
func (s *LiveSession) AskGate(ctx context.Context, g Gate) (GateAnswer, bool) {
	reply := make(chan GateAnswer, 1)
	s.mu.Lock()
	s.gate = &g
	s.gateReply = reply
	s.state.PendingGate = g.Title
	width := s.console.Width()
	s.mu.Unlock()

	s.console.Write(g.Render(width))
	s.wake()

	defer func() {
		s.mu.Lock()
		s.gate = nil
		s.gateReply = nil
		s.state.PendingGate = ""
		s.mu.Unlock()
		s.wake()
	}()

	select {
	case a := <-reply:
		return a, true
	case <-ctx.Done():
		return GateAnswer{}, false
	}
}

// PendingGate returns the waiting gate, if any.
func (s *LiveSession) PendingGate() *Gate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gate
}

func (s *LiveSession) answerGate(a GateAnswer) bool {
	s.mu.Lock()
	ch := s.gateReply
	s.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- a:
		return true
	default:
		return false
	}
}

// stickyLines builds the parked block: optional live-token preview, the
// activity line, and the prompt (or gate prompt).
func (s *LiveSession) stickyLines() ([]string, int) {
	width := s.console.Width()
	var lines []string

	// Live command discovery: typing "/" (and nothing else yet) parks the
	// matching commands right above the prompt, so the catalog is never more
	// than one keystroke away.
	if sug := s.suggestions(width); len(sug) > 0 {
		lines = append(lines, sug...)
	}

	if tok := s.act.LastTokenLine(); tok != "" && s.act.Running() {
		lines = append(lines, "  "+Dim("› ")+Dim(ClipWidth(collapseWhitespace(tok), width-4)))
	}
	lines = append(lines, s.act.Line(width))

	s.mu.Lock()
	g := s.gate
	s.mu.Unlock()

	prompt := Accent("slm › ")
	if g != nil {
		prompt = g.PromptLine() + " "
	} else if s.act.Running() {
		prompt = Accent("slm ▶ ")
	}
	line, col := s.ed.Render(prompt)
	lines = append(lines, TruncateWidth(line, width))
	return lines, col
}

// suggestions renders the inline slash picker for the current buffer.
func (s *LiveSession) suggestions(width int) []string {
	s.mu.Lock()
	reg := s.slash
	gate := s.gate
	s.mu.Unlock()
	if reg == nil || gate != nil {
		return nil
	}
	buf := s.ed.Value()
	if !strings.HasPrefix(buf, "/") || strings.ContainsAny(buf, " \t") {
		return nil
	}
	cands := reg.Find(buf)
	if len(cands) == 0 {
		return []string{Dim("  no command matches ") + Yellow(buf)}
	}
	if len(cands) == 1 && cands[0].Name == buf {
		return nil // already exact — no need to nag
	}
	const maxSuggest = 5
	if len(cands) > maxSuggest {
		cands = cands[:maxSuggest]
	}
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		sig := c.Name
		if c.Args != "" {
			sig += " " + c.Args
		}
		out = append(out, ClipWidth("  "+Cyan(PadWidth(sig, 30))+"  "+Dim(c.Help), width))
	}
	return out
}

func (s *LiveSession) repaint() {
	lines, col := s.stickyLines()
	s.console.SetSticky(lines, col)
}

// RunInteractive enters the premium dashboard loop.
//
// Three concurrent sources feed one select loop: keystrokes (own goroutine, raw
// mode so single keys are readable), the in-flight run (own goroutine), and a
// repaint ticker. Nothing blocks the others — /stop, /feedback, /permission and
// Esc all work while an agent is mid-thought.
func (s *LiveSession) RunInteractive() error {
	raw, err := EnterRaw(os.Stdin)
	if err == nil && raw != nil {
		s.console.SetRaw(true)
	}
	restore := func() {
		s.console.ClearSticky()
		s.console.SetRaw(false)
		raw.Restore()
	}
	defer restore()
	defer func() {
		if r := recover(); r != nil {
			RestoreAllRaw()
			panic(r)
		}
	}()

	if s.showDash {
		RenderDashboard(s.out, s.State())
	}

	pump := StartInputPump(s.in, s.ed)
	defer pump.Stop()
	pump.SetHotkeys(func(k Key) bool {
		if k.Type != KeyRune || s.ed.Value() != "" {
			return false
		}
		g := s.PendingGate()
		if g == nil {
			return false
		}
		_, ok := g.ResolveKey(k)
		return ok
	})

	resize, stopResize := NotifyResize()
	defer stopResize()

	runCh := make(chan error, 1)
	running := false
	var lastQuery string
	var pendingRedirect string
	awaitRedirect := false

	startRun := func(q string) {
		if s.onRun == nil || running {
			return
		}
		running = true
		lastQuery = q
		if s.history != nil {
			s.history.Add(q)
		}
		s.mu.Lock()
		s.state.Running = true
		s.state.Query = q
		s.state.Message = ""
		s.state.Intervention = ""
		s.state.TurnHead = ""
		s.state.ProgressHead = ""
		s.mu.Unlock()
		s.act.Start()
		s.console.Write(Accent("› ") + Bold(q))
		go func(query string) {
			defer func() {
				if r := recover(); r != nil {
					runCh <- fmt.Errorf("run panicked: %v", r)
				}
			}()
			runCh <- s.onRun(query)
		}(q)
	}

	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	s.repaint()

	for {
		select {
		case <-ticker.C:
			if s.act.Running() {
				s.act.Tick()
			}
			s.repaint()

		case <-s.wakeCh:
			s.repaint()

		case <-resize:
			w, _ := TermSize()
			s.console.SetWidth(w)
			s.repaint()

		case err := <-runCh:
			running = false
			s.mu.Lock()
			s.state.Running = false
			if err != nil {
				s.state.Message = err.Error()
			} else {
				s.state.Message = "run finished"
			}
			s.mu.Unlock()
			if err != nil {
				s.act.Stop("failed")
				s.console.Write(Warn(err.Error()))
			} else {
				s.act.Stop("done")
				s.console.Write(Success("run finished"))
			}
			if pendingRedirect != "" {
				next := pendingRedirect
				pendingRedirect = ""
				startRun(next)
			}
			s.repaint()

		case ev, ok := <-pump.Events():
			if !ok {
				return nil
			}
			switch ev.Kind {
			case InputRedraw:
				s.repaint()

			case InputClear:
				if s.ShowDashboard() {
					RenderDashboard(s.out, s.State())
				}
				s.repaint()

			case InputHotkey:
				if g := s.PendingGate(); g != nil {
					if a, ok := g.ResolveKey(ev.Key); ok {
						s.console.Write(Dim("  → ") + Green(a.Value))
						s.answerGate(a)
					}
				}
				s.repaint()

			case InputComplete:
				s.completeLine()
				s.repaint()

			case InputCancel: // Esc
				if running {
					if s.onStop != nil {
						s.onStop()
					}
					awaitRedirect = true
					s.console.Write(Warn("interrupted — what should I change? (type a redirection, or Enter to just stop)"))
					s.act.SetNote("interrupting…")
				} else if g := s.PendingGate(); g != nil {
					s.console.Write(Dim("  (answer required — pick one of the options)"))
				} else {
					s.ed.Reset()
				}
				s.repaint()

			case InputInterrupt: // Ctrl-C on empty line
				if running {
					if s.onStop != nil {
						s.onStop()
					}
					s.console.Write(Warn("interrupted — board preserved; press Ctrl-C again to quit"))
					s.act.SetNote("interrupting…")
					s.repaint()
					continue
				}
				s.console.Write(Dim("bye"))
				return nil

			case InputEOF:
				s.console.Write(Dim("bye"))
				return nil

			case InputLine:
				line := strings.TrimSpace(ev.Line)
				// 1) A waiting gate consumes the line first.
				if g := s.PendingGate(); g != nil {
					if line == "" {
						s.repaint()
						continue
					}
					if a, ok := g.Resolve(line); ok {
						s.console.Write(Dim("  → ") + Green(a.Value) + " " + Dim(a.Notes))
						s.answerGate(a)
					} else {
						s.console.Write(Warn("unrecognized answer — " + StripANSI(g.PromptLine())))
					}
					s.repaint()
					continue
				}
				// 2) Esc redirection: queued, then applied as soon as the
				// canceled run unwinds (never blocks this loop).
				if awaitRedirect {
					awaitRedirect = false
					if line == "" {
						s.console.Write(Dim("stopped."))
						s.repaint()
						continue
					}
					if s.onSteer != nil {
						s.onSteer(line)
					}
					next := lastQuery
					if next == "" {
						next = line
					} else {
						next += "\n\nUser redirection: " + line
					}
					if running {
						pendingRedirect = next
						s.console.Write(Cyan("redirection queued — restarting when the run unwinds"))
					} else {
						startRun(next)
					}
					s.repaint()
					continue
				}
				if line == "" {
					s.repaint()
					continue
				}
				// 3) Help / picker.
				if line == "?" || line == "help" {
					s.printHelp()
					s.repaint()
					continue
				}
				if line == "/" {
					s.mu.Lock()
					reg := s.slash
					s.mu.Unlock()
					if reg != nil {
						s.console.Write(reg.RenderPicker("", s.console.Width(), 40))
					}
					s.repaint()
					continue
				}
				// 4) Slash commands — routed to the live handler while running.
				if strings.HasPrefix(line, "/") {
					handler := s.onSlash
					if running && s.onLiveSlash != nil {
						handler = s.onLiveSlash
					}
					if handler != nil {
						quit, err := handler(line)
						if err != nil {
							s.console.Write(Error(err.Error()))
							s.setMsg(err.Error())
						}
						if quit {
							s.console.Write(Dim("bye"))
							return nil
						}
					}
					s.repaint()
					continue
				}
				// 5) Plain text: a new run, or live steering while one runs.
				if running {
					if s.onSteer != nil {
						s.onSteer(line)
						s.console.Write(Cyan("steering: ") + line)
					} else {
						s.console.Write(Dim("a run is in flight — /stop first, or Esc to redirect"))
					}
					s.repaint()
					continue
				}
				startRun(line)
				s.repaint()
			}
		}
	}
}

// completeLine applies Tab completion to the current buffer.
func (s *LiveSession) completeLine() {
	s.mu.Lock()
	reg := s.slash
	s.mu.Unlock()
	if reg == nil {
		return
	}
	line := s.ed.Value()
	if !strings.HasPrefix(line, "/") {
		return
	}
	completed, cands := reg.Complete(line)
	if completed != line {
		s.ed.SetValue(completed)
		return
	}
	if len(cands) > 0 {
		s.console.Write(reg.RenderPicker(strings.TrimPrefix(line, "/"), s.console.Width(), 12))
	}
}

func (s *LiveSession) setMsg(m string) {
	s.mu.Lock()
	s.state.Message = m
	s.mu.Unlock()
}

func (s *LiveSession) printHelp() {
	s.mu.Lock()
	reg := s.slash
	s.mu.Unlock()
	if reg != nil {
		s.console.Write(reg.RenderHelp(s.console.Width()))
		return
	}
	s.console.Write(Bold("Premium TUI") + "\n" +
		"  " + Cyan("<query>") + "   run the pipeline\n" +
		"  " + Cyan("/q") + "        quit")
}

func shortPath(p string) string {
	if p == "" {
		return "."
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return filepath.Base(p)
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// TickClock is a tiny helper for elapsed display in tests / footers.
func TickClock(since time.Time) string {
	return time.Since(since).Round(time.Second).String()
}
