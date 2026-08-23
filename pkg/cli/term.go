package cli

import (
	"os"
	"sync"

	"golang.org/x/term"
)

// Raw-mode management. Every entry point that switches the terminal into raw
// mode registers here so a panic, a signal handler, or an early return can put
// the terminal back into cooked mode — a shell left in raw mode is unusable.

type rawTerm struct {
	fd    int
	state *term.State
}

var (
	rawMu     sync.Mutex
	rawActive []*rawTerm
)

// RawMode is a handle on a terminal switched to raw mode.
type RawMode struct {
	t    *rawTerm
	once sync.Once
}

// EnterRaw puts f into raw mode. It returns a nil *RawMode and a nil error when
// f is not a terminal, so callers can use the same code path for pipes.
func EnterRaw(f *os.File) (*RawMode, error) {
	if f == nil {
		return nil, nil
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return nil, nil
	}
	st, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	rt := &rawTerm{fd: fd, state: st}
	rawMu.Lock()
	rawActive = append(rawActive, rt)
	rawMu.Unlock()
	return &RawMode{t: rt}, nil
}

// Restore returns the terminal to cooked mode. Safe to call more than once and
// safe on a nil receiver, so `defer rm.Restore()` always compiles.
func (r *RawMode) Restore() {
	if r == nil || r.t == nil {
		return
	}
	r.once.Do(func() {
		_ = term.Restore(r.t.fd, r.t.state)
		rawMu.Lock()
		for i, x := range rawActive {
			if x == r.t {
				rawActive = append(rawActive[:i], rawActive[i+1:]...)
				break
			}
		}
		rawMu.Unlock()
	})
}

// RestoreAllRaw puts every terminal this process put into raw mode back into
// cooked mode. Call it from a deferred panic guard and from signal handling so
// a crash never leaves the user with a broken shell.
func RestoreAllRaw() {
	rawMu.Lock()
	list := append([]*rawTerm(nil), rawActive...)
	rawActive = nil
	rawMu.Unlock()
	for _, rt := range list {
		_ = term.Restore(rt.fd, rt.state)
	}
}

// TermSize returns the usable width/height of the terminal attached to stdout,
// clamped to sane values. Width is clamped to [40, 120] because the dashboard
// boxes become unreadable confetti when they exceed the real screen and are
// wasteful past ~120 columns.
func TermSize() (width, height int) {
	width, height = 88, 24
	fd := int(os.Stdout.Fd())
	if term.IsTerminal(fd) {
		if w, h, err := term.GetSize(fd); err == nil {
			if w > 0 {
				width = w
			}
			if h > 0 {
				height = h
			}
		}
	}
	return clampWidth(width), height
}

func clampWidth(w int) int {
	if w < 40 {
		return 40
	}
	if w > 120 {
		return 120
	}
	return w
}

// NarrowLayout reports whether the terminal is too narrow for the two-column
// dashboard and should fall back to a single-column degraded layout.
func NarrowLayout(width int) bool { return width < 70 }
