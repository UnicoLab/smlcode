package cli

import (
	"bufio"
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
	Settings        string
	Message         string
}

// IsInteractive reports whether stdin is a TTY suitable for the premium TUI.
func IsInteractive() bool {
	if os.Getenv("SLMCODE_TUI") == "0" || os.Getenv("CI") == "true" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// RenderDashboard paints a premium multi-panel status view (no bubbletea dep).
func RenderDashboard(w io.Writer, st DashboardState) {
	if w == nil {
		w = os.Stdout
	}
	width := 88
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if tw, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && tw >= 60 {
			width = tw
		}
	}
	bar := strings.Repeat("─", min(width-2, 96))

	fmt.Fprint(w, "\033[H\033[2J") // home + clear
	fmt.Fprintln(w, Accent("┌"+bar+"┐"))
	title := Bold(" SLMCODE ") + Dim("premium TUI") + "  " + Cyan(shortPath(st.Root))
	fmt.Fprintln(w, Accent("│")+padRight(title, min(width-2, 96))+Accent("│"))
	fmt.Fprintln(w, Accent("├"+bar+"┤"))

	conn := fmt.Sprintf(" %s  %s/%s  %s",
		Green("●")+" "+White(st.Provider),
		Accent(st.Model),
		Dim(st.Backend),
		Dim(clipMid(st.Endpoint, 36)))
	if st.Running {
		conn += "  " + Yellow("▶ RUN")
	} else {
		conn += "  " + Dim("idle")
	}
	if st.Phase != "" {
		conn += "  phase=" + Cyan(st.Phase)
	}
	fmt.Fprintln(w, Accent("│")+padRight(conn, min(width-2, 96))+Accent("│"))
	fmt.Fprintln(w, Accent("├"+bar+"┤"))

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
	fmt.Fprintln(w, Accent("│")+padRight(prog, min(width-2, 96))+Accent("│"))

	// Active agents
	active := " agents "
	if len(st.Agents) == 0 {
		active += Dim("none active")
	} else {
		active += Cyan(strings.Join(st.Agents, "  "))
	}
	fmt.Fprintln(w, Accent("│")+padRight(active, min(width-2, 96))+Accent("│"))
	fmt.Fprintln(w, Accent("├"+bar+"┤"))

	// Tasks panel (top cards)
	fmt.Fprintln(w, Accent("│")+padRight(Bold(" Tasks"), min(width-2, 96))+Accent("│"))
	shown := 0
	if st.Board != nil {
		for _, t := range st.Board.Tasks {
			if shown >= 8 {
				break
			}
			t.Normalize()
			line := fmt.Sprintf("  %s %-12s @%-10s %s",
				Accent(t.ID), ColumnColor(t.Column), Dim(t.Role), clipMid(t.Title, width-42))
			fmt.Fprintln(w, Accent("│")+padRight(line, min(width-2, 96))+Accent("│"))
			shown++
		}
	}
	if shown == 0 {
		fmt.Fprintln(w, Accent("│")+padRight(Dim("  (no tasks yet)"), min(width-2, 96))+Accent("│"))
	}

	fmt.Fprintln(w, Accent("├"+bar+"┤"))
	fmt.Fprintln(w, Accent("│")+padRight(Bold(" Live"), min(width-2, 96))+Accent("│"))
	evStart := 0
	if len(st.Events) > 6 {
		evStart = len(st.Events) - 6
	}
	if len(st.Events) == 0 {
		fmt.Fprintln(w, Accent("│")+padRight(Dim("  waiting for events…"), min(width-2, 96))+Accent("│"))
	}
	for _, e := range st.Events[evStart:] {
		line := "  " + collapseWhitespace(FormatEvent(e))
		// single line for panel
		if i := strings.Index(line, "\n"); i >= 0 {
			line = line[:i]
		}
		fmt.Fprintln(w, Accent("│")+padRight(clipMid(line, width-4), min(width-2, 96))+Accent("│"))
	}

	// Errors / file changes glance
	if strings.TrimSpace(st.ErrorsHead) != "" {
		fmt.Fprintln(w, Accent("├"+bar+"┤"))
		fmt.Fprintln(w, Accent("│")+padRight(Red(" Errors ")+Dim(clipMid(st.ErrorsHead, width-12)), min(width-2, 96))+Accent("│"))
	}
	if strings.TrimSpace(st.DiffHead) != "" {
		fmt.Fprintln(w, Accent("│")+padRight(Green(" Diff ")+Dim(clipMid(st.DiffHead, width-10)), min(width-2, 96))+Accent("│"))
	}
	if len(st.Queries) > 0 {
		fmt.Fprintln(w, Accent("│")+padRight(Blue(" Queries ")+Dim(strings.Join(st.Queries, " · ")), min(width-2, 96))+Accent("│"))
	}
	if strings.TrimSpace(st.LatencyHead) != "" {
		fmt.Fprintln(w, Accent("│")+padRight(Yellow(" Latency ")+Dim(clipMid(st.LatencyHead, width-12)), min(width-2, 96))+Accent("│"))
	}

	fmt.Fprintln(w, Accent("├"+bar+"┤"))
	help := Dim(" keys ") + White("[enter]") + Dim(" run  ") +
		White("?") + Dim(" help  ") +
		White("/compact") + Dim("  ") +
		White("/stats") + Dim("  ") +
		White("/sessions") + Dim("  ") +
		White("/stop") + Dim("  ") +
		White("/q")
	fmt.Fprintln(w, Accent("│")+padRight(help, min(width-2, 96))+Accent("│"))
	if st.Message != "" {
		fmt.Fprintln(w, Accent("│")+padRight(" "+Yellow(clipMid(st.Message, width-4)), min(width-2, 96))+Accent("│"))
	}
	fmt.Fprintln(w, Accent("└"+bar+"┘"))
	if st.Query != "" {
		fmt.Fprintln(w, Dim(" last query: ")+clipMid(st.Query, width-14))
	}
}

// PrintStaticDashboard is the non-interactive / CI fallback (no clear-screen loop).
func PrintStaticDashboard(st DashboardState) {
	width := 72
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
	_ = width
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
		fmt.Printf("  %s  %s\n", ColumnColor(fmt.Sprintf("%-14s", col)), Bold(fmt.Sprintf("%d", len(tasks))))
		for i, t := range tasks {
			if i >= 3 {
				fmt.Println(Dim(fmt.Sprintf("    … +%d more", len(tasks)-3)))
				break
			}
			fmt.Printf("    %s @%s  %s\n", Accent(t.ID), t.Role, clipMid(t.Title, 56))
		}
	}
}

// LiveSession drives the interactive premium TUI REPL.
type LiveSession struct {
	mu      sync.Mutex
	state   DashboardState
	status  *StatusTracker
	onRun   func(query string) error
	onStop  func()
	onSlash func(cmd string) (quit bool, err error)
	in      io.Reader
	out     io.Writer
}

// NewLiveSession constructs a TUI session. Call SetState / Observe as events arrive.
func NewLiveSession() *LiveSession {
	return &LiveSession{
		status: NewStatusTracker(),
		in:     os.Stdin,
		out:    os.Stdout,
	}
}

func (s *LiveSession) SetState(st DashboardState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// preserve events / latency / compact if caller omitted
	if st.Events == nil {
		st.Events = s.state.Events
	}
	if st.LatencyHead == "" {
		st.LatencyHead = s.state.LatencyHead
	}
	if !st.Compact && s.state.Compact {
		st.Compact = true
	}
	s.state = st
}

func (s *LiveSession) Observe(e stream.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != nil {
		s.status.Observe(e)
	}
	// Compact mode: keep latency + phase/agent/file events; drop noisy output dumps.
	if s.state.Compact && e.Kind == stream.KindOutput {
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
	switch e.Kind {
	case stream.KindAgentStart:
		if e.TaskID != "" {
			label := e.TaskID
			if e.Agent != "" {
				label = "@" + e.Agent + ":" + e.TaskID
			}
			s.state.Agents = appendUnique(s.state.Agents, label)
			s.state.Running = true
		}
	case stream.KindAgentEnd:
		if e.TaskID != "" {
			filter := e.TaskID
			var next []string
			for _, a := range s.state.Agents {
				if !strings.Contains(a, filter) {
					next = append(next, a)
				}
			}
			s.state.Agents = next
		}
	case stream.KindLatency:
		if e.Message != "" {
			s.state.LatencyHead = e.Message
		}
	}
	if e.Phase == "done" {
		s.state.Running = false
		s.state.Agents = nil
	}
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

func (s *LiveSession) OnRun(fn func(string) error)   { s.onRun = fn }
func (s *LiveSession) OnStop(fn func())               { s.onStop = fn }
func (s *LiveSession) OnSlash(fn func(string) (bool, error)) {
	s.onSlash = fn
}

// RunInteractive enters the premium dashboard loop. Non-TTY callers should use PrintStaticDashboard.
func (s *LiveSession) RunInteractive() error {
	s.redraw()
	sc := bufio.NewScanner(s.in)
	for {
		fmt.Fprint(s.out, Accent("\nslm › "))
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			s.redraw()
			continue
		}
		if line == "?" || line == "help" || line == "/help" {
			s.printHelp()
			continue
		}
		if strings.HasPrefix(line, "/") {
			if s.onSlash != nil {
				quit, err := s.onSlash(line)
				if err != nil {
					s.setMsg(err.Error())
				}
				if quit {
					fmt.Fprintln(s.out, Dim("bye"))
					return nil
				}
			}
			s.redraw()
			continue
		}
		if s.onRun != nil {
			s.setMsg("running…")
			s.mu.Lock()
			s.state.Running = true
			s.state.Query = line
			s.state.Events = nil
			s.status = NewStatusTracker()
			s.mu.Unlock()
			s.redraw()
			err := s.onRun(line)
			s.mu.Lock()
			s.state.Running = false
			if err != nil {
				s.state.Message = err.Error()
			} else {
				s.state.Message = "run finished"
			}
			s.mu.Unlock()
		}
		s.redraw()
	}
	return sc.Err()
}

func (s *LiveSession) redraw() {
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()
	RenderDashboard(s.out, st)
}

func (s *LiveSession) setMsg(m string) {
	s.mu.Lock()
	s.state.Message = m
	s.mu.Unlock()
}

func (s *LiveSession) printHelp() {
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, Bold("Premium TUI — commands"))
	fmt.Fprintln(s.out, "  "+Cyan("<query>")+"          run full SLM pipeline")
	fmt.Fprintln(s.out, "  "+Cyan("/board")+"            refresh + show board")
	fmt.Fprintln(s.out, "  "+Cyan("/status")+"           connection / settings glance")
	fmt.Fprintln(s.out, "  "+Cyan("/errors")+"           tail .slmcode/errors/errors.md")
	fmt.Fprintln(s.out, "  "+Cyan("/diff")+"             git dirty files")
	fmt.Fprintln(s.out, "  "+Cyan("/queries")+"          recent query turns")
	fmt.Fprintln(s.out, "  "+Cyan("/agents")+"           list specialists")
	fmt.Fprintln(s.out, "  "+Cyan("/agent …")+"          show|new|edit|delete agents (Studio parity)")
	fmt.Fprintln(s.out, "  "+Cyan("/skills")+"           list skills")
	fmt.Fprintln(s.out, "  "+Cyan("/studio")+"           print Studio URL hint")
	fmt.Fprintln(s.out, "  "+Cyan("/model <id>")+"       switch model (persists)")
	fmt.Fprintln(s.out, "  "+Cyan("/provider <name>")+"  switch provider")
	fmt.Fprintln(s.out, "  "+Cyan("/permission …")+"     auto|dry-run|review  or  shell=allow|ask|deny")
	fmt.Fprintln(s.out, "  "+Cyan("/compact")+"          toggle compact live stream")
	fmt.Fprintln(s.out, "  "+Cyan("/stats")+"            last-run phase latency")
	fmt.Fprintln(s.out, "  "+Cyan("/sessions")+"         pick a prior query turn")
	fmt.Fprintln(s.out, "  "+Cyan("/stop")+"             request stop of current run")
	fmt.Fprintln(s.out, "  "+Cyan("/refresh")+"          redraw dashboard")
	fmt.Fprintln(s.out, "  "+Cyan("/q")+"                quit")
	fmt.Fprintln(s.out, Dim("  Binary is slmcode (docs sometimes say smlcode)."))
}

func padRight(s string, n int) string {
	// strip ANSI for width — approximate by visible length heuristic
	vis := visibleLen(s)
	if vis >= n {
		return trimVisible(s, n)
	}
	return s + strings.Repeat(" ", n-vis)
}

func visibleLen(s string) int {
	n := 0
	inEsc := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEsc = false
			}
			continue
		}
		n++
	}
	return n
}

func trimVisible(s string, n int) string {
	if visibleLen(s) <= n {
		return s
	}
	// fallback: raw truncate
	if len(s) > n {
		return s[:max(0, n-1)] + "…"
	}
	return s
}

func clipMid(s string, n int) string {
	s = collapseWhitespace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-1] + "…"
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TickClock is a tiny helper for elapsed display in tests / footers.
func TickClock(since time.Time) string {
	return time.Since(since).Round(time.Second).String()
}
