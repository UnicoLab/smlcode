package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
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
	// mu serializes gates. See AskGate.
	mu sync.Mutex
	// keys is the ONE decoder over In, created on first use.
	//
	// Every gate used to build its own bufio-backed reader. Buffered readers
	// read ahead, so the first gate of a run swallowed the bytes meant for the
	// next one: piping two answers in answered one gate and starved the other,
	// and in raw mode a fast typist could lose a keystroke the same way. One
	// decoder for the whole terminal keeps every unconsumed byte in one place.
	keys *KeyReader
}

// NewTerminalGateHost returns a host bound to the process terminal.
func NewTerminalGateHost() *TerminalGateHost {
	return &TerminalGateHost{In: os.Stdin, Out: os.Stdout}
}

// acquire takes the terminal lock, giving up if ctx is canceled while waiting.
//
// A queued gate must not outlive its run: when the user interrupts, the gates
// still waiting behind the current one have to return "no answer" rather than
// block a shutdown forever.
func (h *TerminalGateHost) acquire(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		h.mu.Lock()
		close(done)
	}()
	select {
	case <-done:
		if ctx.Err() != nil {
			h.mu.Unlock()
			return false
		}
		return true
	case <-ctx.Done():
		// The lock attempt above is still in flight; hand it straight back
		// when it lands so the mutex is never left held by a dead caller.
		go func() {
			<-done
			h.mu.Unlock()
		}()
		return false
	}
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
	// ONE gate owns the terminal at a time.
	//
	// A wave can escalate several tasks at once, and the engine calls the
	// escalate hook once per task from its own goroutine. Without this lock
	// two AskGate calls raced: both put the terminal into raw mode, both
	// created a KeyReader on the same stdin, and the single keystroke the user
	// pressed went to whichever reader won — frequently the one whose card was
	// NOT the last thing on screen. The visible symptom was a gate card that
	// simply ignored every key, with no way to tell that a second, invisible
	// gate had eaten it. Restoring the terminal was equally racy: the loser's
	// Restore could put it back into cooked mode under the winner's reader.
	//
	// Serializing also makes the transcript truthful: the card for the second
	// task is drawn when it is actually the one being asked.
	if !h.acquire(ctx) {
		return GateAnswer{}, false
	}
	defer h.mu.Unlock()
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

// reader returns the host's single key decoder, creating it on first use.
//
// Callers hold h.mu (AskGate is the only path in), so no extra locking.
func (h *TerminalGateHost) reader() *KeyReader {
	if h.keys == nil {
		h.keys = NewKeyReader(h.in())
	}
	return h.keys
}

// read collects one answer. On a terminal it accepts a single keystroke; a
// freeform option (or Enter) drops to a typed line so the user can attach
// notes. Without a terminal it reads a line, which keeps the host usable from a
// script that feeds answers on stdin.
func (h *TerminalGateHost) read(g Gate) (GateAnswer, bool) {
	rm, err := EnterRaw(h.in())
	if err != nil || rm == nil {
		return h.readLine(g, "")
	}
	kr := h.reader()
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
//
// It decodes through the same KeyReader as the keystroke path so the two never
// buffer past each other — the bug that made a second piped answer unreachable.
func (h *TerminalGateHost) readLine(g Gate, seed string) (GateAnswer, bool) {
	kr := h.reader()
	var b strings.Builder
	b.WriteString(seed)
	for {
		k, err := kr.ReadKey()
		if err != nil {
			if strings.TrimSpace(b.String()) == "" {
				return GateAnswer{}, false
			}
			return g.Resolve(b.String())
		}
		switch k.Type {
		case KeyEnter:
			return g.Resolve(b.String())
		case KeyCtrlC, KeyCtrlD, KeyEscape:
			return GateAnswer{Value: GateInterrupted}, true
		case KeyBackspace:
			s := b.String()
			if r := []rune(s); len(r) > 0 {
				b.Reset()
				b.WriteString(string(r[:len(r)-1]))
			}
		case KeyRune:
			b.WriteRune(k.Rune)
		}
	}
}
