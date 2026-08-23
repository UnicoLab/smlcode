package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// Console owns the bottom of the terminal.
//
// The transcript is append-only — nothing ever clears the screen, so scrollback
// keeps everything an agent said. A sticky block (status line + prompt) is
// parked at the bottom and repainted in place: before writing new transcript
// lines the sticky block is erased, the lines are appended, and the block is
// redrawn. This is the Claude Code model and it is what makes "read what
// happened two minutes ago" possible.
type Console struct {
	mu      sync.Mutex
	out     io.Writer
	sticky  []string // last painted sticky lines (for erase accounting)
	painted int      // physical rows currently occupied by the sticky block
	width   int
	raw     bool // raw mode → "\n" must be "\r\n"
	cursor  int  // desired cursor column within the last sticky line
	enabled bool // false → plain, non-sticky output (pipes, non-TTY)
}

// NewConsole builds a console writing to out. When sticky is false every write
// is a plain append with no cursor manipulation, which is the correct behavior
// for pipes, CI and `--json`.
func NewConsole(out io.Writer, width int, sticky bool) *Console {
	if width <= 0 {
		width = 88
	}
	return &Console{out: out, width: width, enabled: sticky}
}

// SetRaw tells the console the terminal is in raw mode, so line feeds need an
// explicit carriage return.
func (c *Console) SetRaw(raw bool) {
	c.mu.Lock()
	c.raw = raw
	c.mu.Unlock()
}

// SetWidth updates the wrap width (call on SIGWINCH).
func (c *Console) SetWidth(w int) {
	c.mu.Lock()
	if w > 0 {
		c.width = w
	}
	c.mu.Unlock()
}

// Width returns the current wrap width.
func (c *Console) Width() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.width
}

func (c *Console) nl() string {
	if c.raw {
		return "\r\n"
	}
	return "\n"
}

// eraseStickyLocked removes the sticky block from the screen.
func (c *Console) eraseStickyLocked() {
	if !c.enabled || c.painted == 0 {
		return
	}
	// Move to column 0 of the first sticky row, then erase to end of screen.
	var b strings.Builder
	b.WriteString("\r")
	if c.painted > 1 {
		fmt.Fprintf(&b, "\033[%dA", c.painted-1)
	}
	b.WriteString("\033[J")
	_, _ = io.WriteString(c.out, b.String())
	c.painted = 0
}

// paintStickyLocked draws the sticky block and positions the cursor.
func (c *Console) paintStickyLocked() {
	if !c.enabled || len(c.sticky) == 0 {
		return
	}
	var b strings.Builder
	rows := 0
	for i, line := range c.sticky {
		line = TruncateWidth(line, c.width)
		b.WriteString(line)
		if i < len(c.sticky)-1 {
			b.WriteString(c.nl())
		}
		rows++
	}
	// Park the cursor at the requested column of the last line.
	last := ""
	if len(c.sticky) > 0 {
		last = c.sticky[len(c.sticky)-1]
	}
	end := VisibleWidth(TruncateWidth(last, c.width))
	if c.cursor >= 0 && c.cursor < end {
		fmt.Fprintf(&b, "\033[%dD", end-c.cursor)
	}
	_, _ = io.WriteString(c.out, b.String())
	c.painted = rows
}

// SetSticky replaces the sticky block (status + prompt) and repaints it.
// cursorCol is the desired cursor column on the final sticky line; pass -1 to
// leave the cursor at the end.
func (c *Console) SetSticky(lines []string, cursorCol int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eraseStickyLocked()
	c.sticky = append([]string(nil), lines...)
	c.cursor = cursorCol
	c.paintStickyLocked()
}

// ClearSticky removes the sticky block entirely (used before handing the
// terminal back, e.g. on exit or when spawning $EDITOR).
func (c *Console) ClearSticky() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eraseStickyLocked()
	c.sticky = nil
}

// Write appends transcript text above the sticky block. Embedded newlines are
// handled, and the sticky block is restored afterwards.
func (c *Console) Write(s string) {
	if s == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eraseStickyLocked()
	body := strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", c.nl())
	_, _ = io.WriteString(c.out, body+c.nl())
	c.paintStickyLocked()
}

// Writef is Write with formatting.
func (c *Console) Writef(format string, args ...any) {
	c.Write(fmt.Sprintf(format, args...))
}

// WriteLines appends several transcript lines in one repaint cycle.
func (c *Console) WriteLines(lines []string) {
	if len(lines) == 0 {
		return
	}
	c.Write(strings.Join(lines, "\n"))
}

// Raw writes bytes verbatim with no sticky handling — used by the token stream
// where partial lines must accumulate.
func (c *Console) Raw(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = io.WriteString(c.out, s)
}
