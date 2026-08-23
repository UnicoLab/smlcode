package cli

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
)

// A gate prompt for the plain (non-TUI) terminal.
//
// `slmcode run` on a real terminal used to register its HITL hooks with a nil
// host, so every gate fell through to the headless policy and the user was
// told "no TTY — --on-gate-timeout=stop" while sitting at a TTY. With the
// default plan_approve=ask that turned the very first `slmcode run "…"` into
// three silent replans and an exit-1 failure.
//
// TerminalGateHost renders the same Gate card the TUI shows and answers it
// with a single keystroke, so `run` and the TUI behave identically.

// GateInterrupted is the answer value a gate carries when the user pressed
// Ctrl-C, Ctrl-D or Esc at the prompt instead of choosing.
//
// It has to be a distinct value rather than "no answer": raw mode disables the
// terminal's ISIG handling, so Ctrl-C at a gate never becomes a SIGINT, and
// without this the CLI could not tell "the user aborted" from "there was
// nobody there" — and answered a deliberate Ctrl-C with a page of advice about
// --on-gate-timeout.
const GateInterrupted = "__interrupt__"

// TerminalGateHost answers gates from stdin/stdout with no live dashboard.
type TerminalGateHost struct {
	In  *os.File // defaults to os.Stdin
	Out io.Writer
}

// NewTerminalGateHost returns a host bound to the process terminal.
func NewTerminalGateHost() *TerminalGateHost {
	return &TerminalGateHost{In: os.Stdin, Out: os.Stdout}
}

func (h *TerminalGateHost) in() *os.File {
	if h == nil || h.In == nil {
		return os.Stdin
	}
	return h.In
}

func (h *TerminalGateHost) out() io.Writer {
	if h == nil || h.Out == nil {
		return os.Stdout
	}
	return h.Out
}

// AskGate renders g and blocks until the user answers or ctx is canceled.
//
// The second return value is false when no answer was obtained (ctx canceled,
// stdin closed, Ctrl-C) — the caller must then fail closed rather than invent
// an approval.
func (h *TerminalGateHost) AskGate(ctx context.Context, g Gate) (GateAnswer, bool) {
	width, _ := TermSize()
	out := h.out()
	// Writes to the terminal: a failure here means the terminal is gone, which
	// the read below reports as "no answer". Nothing useful to do with the err.
	write := func(s string) { _, _ = io.WriteString(out, s) }
	write("\n" + g.Render(width))
	write(g.PromptLine() + " ")

	type result struct {
		ans GateAnswer
		ok  bool
	}
	ch := make(chan result, 1)
	go func() { a, ok := h.read(g); ch <- result{a, ok} }()

	select {
	case r := <-ch:
		switch {
		case r.ok && r.ans.Value == GateInterrupted:
			write(Yellow("interrupted") + "\n")
		case r.ok:
			write(Green(r.ans.Value) + "\n")
		default:
			write("\n")
		}
		return r.ans, r.ok
	case <-ctx.Done():
		write("\n")
		return GateAnswer{}, false
	}
}

// read collects one answer. On a terminal it accepts a single keystroke; a
// freeform option (or Enter) drops to a typed line so the user can attach
// notes. Without a terminal it reads one line, which keeps the host usable
// from a script that feeds answers on stdin.
func (h *TerminalGateHost) read(g Gate) (GateAnswer, bool) {
	in := h.in()
	rm, err := EnterRaw(in)
	if err != nil || rm == nil {
		return h.readLine(g, "")
	}
	kr := NewKeyReader(in)
	for {
		k, kerr := kr.ReadKey()
		if kerr != nil {
			rm.Restore()
			return GateAnswer{}, false
		}
		switch k.Type {
		case KeyCtrlC, KeyCtrlD, KeyEscape:
			rm.Restore()
			return GateAnswer{Value: GateInterrupted}, true
		case KeyEnter:
			rm.Restore()
			return h.readLine(g, "")
		}
		if ans, ok := g.ResolveKey(k); ok {
			rm.Restore()
			return ans, true
		}
		// A key that belongs to a freeform option, or any other printable
		// character, starts a typed answer with that character already in.
		if k.Type == KeyRune {
			rm.Restore()
			_, _ = io.WriteString(h.out(), string(k.Rune))
			return h.readLine(g, string(k.Rune))
		}
	}
}

// readLine reads the rest of a typed answer, seeded with what the user already
// pressed, and resolves it against the gate's options.
func (h *TerminalGateHost) readLine(g Gate, seed string) (GateAnswer, bool) {
	br := bufio.NewReader(h.in())
	line, err := br.ReadString('\n')
	if err != nil && strings.TrimSpace(seed+line) == "" {
		return GateAnswer{}, false
	}
	return g.Resolve(seed + strings.TrimRight(line, "\r\n"))
}
