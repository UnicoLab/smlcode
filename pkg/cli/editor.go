package cli

import (
	"strings"
	"sync"
)

// EditorAction is what the host loop must do after feeding a key to the editor.
type EditorAction int

const (
	ActNone      EditorAction = iota
	ActRedraw                 // buffer/cursor changed — repaint the prompt line
	ActSubmit                 // user pressed enter — Submitted() holds the line
	ActCancel                 // Esc — interrupt whatever is running
	ActInterrupt              // Ctrl-C
	ActEOF                    // Ctrl-D on an empty buffer
	ActComplete               // Tab — host should offer completions
	ActClear                  // Ctrl-L — clear/repaint the screen
)

// LineEditor is a pure, testable readline-style buffer. It holds no terminal
// state: the host renders Render() and applies the returned action.
type LineEditor struct {
	// mu guards the buffer: the input pump writes from its own goroutine while
	// the render loop reads to repaint the prompt.
	mu        sync.Mutex
	buf       []rune
	cursor    int
	submitted string
	history   *PromptHistory
	draft     string // buffer stashed when history browsing starts

	searching bool
	searchQ   []rune
	searchHit string
}

// NewLineEditor creates an editor bound to an optional prompt history.
func NewLineEditor(h *PromptHistory) *LineEditor {
	return &LineEditor{history: h}
}

// Value returns the current buffer.
func (e *LineEditor) Value() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return string(e.buf)
}

// Cursor returns the cursor position in runes.
func (e *LineEditor) Cursor() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cursor
}

// Submitted returns the last submitted line.
func (e *LineEditor) Submitted() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.submitted
}

// Searching reports whether reverse-search mode is active.
func (e *LineEditor) Searching() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.searching
}

// SearchQuery returns the current Ctrl-R query.
func (e *LineEditor) SearchQuery() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return string(e.searchQ)
}

// SetValue replaces the buffer and puts the cursor at the end.
func (e *LineEditor) SetValue(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setValueLocked(s)
}

func (e *LineEditor) setValueLocked(s string) {
	e.buf = []rune(s)
	e.cursor = len(e.buf)
}

// Reset clears the buffer.
func (e *LineEditor) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetLocked()
}

func (e *LineEditor) resetLocked() {
	e.buf = e.buf[:0]
	e.cursor = 0
	e.searching = false
	e.searchQ = nil
	e.searchHit = ""
}

// Feed applies one keystroke and reports what the host should do.
func (e *LineEditor) Feed(k Key) EditorAction {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.searching {
		return e.feedSearch(k)
	}
	switch k.Type {
	case KeyRune:
		e.insert(k.Rune)
		return ActRedraw
	case KeyEnter:
		e.submitted = strings.TrimRight(string(e.buf), " \t")
		e.resetLocked()
		return ActSubmit
	case KeyBackspace:
		if e.cursor > 0 {
			e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
			e.cursor--
		}
		return ActRedraw
	case KeyDelete:
		if e.cursor < len(e.buf) {
			e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
		}
		return ActRedraw
	case KeyLeft:
		if e.cursor > 0 {
			e.cursor--
		}
		return ActRedraw
	case KeyRight:
		if e.cursor < len(e.buf) {
			e.cursor++
		}
		return ActRedraw
	case KeyHome, KeyCtrlA:
		e.cursor = 0
		return ActRedraw
	case KeyEnd, KeyCtrlE:
		e.cursor = len(e.buf)
		return ActRedraw
	case KeyCtrlU:
		e.buf = append([]rune{}, e.buf[e.cursor:]...)
		e.cursor = 0
		return ActRedraw
	case KeyCtrlK:
		e.buf = e.buf[:e.cursor]
		return ActRedraw
	case KeyCtrlW:
		e.deleteWord()
		return ActRedraw
	case KeyUp, KeyCtrlP:
		e.historyPrev()
		return ActRedraw
	case KeyDown, KeyCtrlN:
		e.historyNext()
		return ActRedraw
	case KeyCtrlR:
		e.searching = true
		e.searchQ = nil
		e.searchHit = ""
		return ActRedraw
	case KeyTab:
		return ActComplete
	case KeyCtrlL:
		return ActClear
	case KeyEscape:
		return ActCancel
	case KeyCtrlC:
		if len(e.buf) > 0 {
			e.resetLocked()
			return ActRedraw
		}
		return ActInterrupt
	case KeyCtrlD:
		if len(e.buf) == 0 {
			return ActEOF
		}
		if e.cursor < len(e.buf) {
			e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
		}
		return ActRedraw
	}
	return ActNone
}

func (e *LineEditor) feedSearch(k Key) EditorAction {
	switch k.Type {
	case KeyRune:
		e.searchQ = append(e.searchQ, k.Rune)
		e.searchHit = e.findHistory(string(e.searchQ))
		return ActRedraw
	case KeyBackspace:
		if len(e.searchQ) > 0 {
			e.searchQ = e.searchQ[:len(e.searchQ)-1]
		}
		e.searchHit = e.findHistory(string(e.searchQ))
		return ActRedraw
	case KeyEnter:
		if e.searchHit != "" {
			e.setValueLocked(e.searchHit)
		}
		e.searching = false
		e.searchQ = nil
		e.searchHit = ""
		return ActRedraw
	case KeyEscape, KeyCtrlC, KeyCtrlR:
		if k.Type == KeyCtrlR && len(e.searchQ) > 0 {
			// Ctrl-R again cycles is not supported; treat as accept.
			if e.searchHit != "" {
				e.setValueLocked(e.searchHit)
			}
		}
		e.searching = false
		e.searchQ = nil
		e.searchHit = ""
		return ActRedraw
	}
	return ActNone
}

// SearchHit returns the current reverse-search match.
func (e *LineEditor) SearchHit() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.searchHit
}

func (e *LineEditor) findHistory(q string) string {
	if e.history == nil || q == "" {
		return ""
	}
	items := e.history.Recent(maxPromptHistory)
	for i := len(items) - 1; i >= 0; i-- {
		if strings.Contains(items[i], q) {
			return items[i]
		}
	}
	return ""
}

func (e *LineEditor) insert(r rune) {
	e.buf = append(e.buf, 0)
	copy(e.buf[e.cursor+1:], e.buf[e.cursor:])
	e.buf[e.cursor] = r
	e.cursor++
}

func (e *LineEditor) deleteWord() {
	i := e.cursor
	for i > 0 && e.buf[i-1] == ' ' {
		i--
	}
	for i > 0 && e.buf[i-1] != ' ' {
		i--
	}
	e.buf = append(append([]rune{}, e.buf[:i]...), e.buf[e.cursor:]...)
	e.cursor = i
}

func (e *LineEditor) historyPrev() {
	if e.history == nil {
		return
	}
	if !e.browsing() {
		e.draft = string(e.buf)
	}
	if v, ok := e.history.Prev(); ok {
		e.setValueLocked(v)
	}
}

func (e *LineEditor) historyNext() {
	if e.history == nil {
		return
	}
	v, ok := e.history.Next()
	if !ok {
		return
	}
	if v == "" {
		e.setValueLocked(e.draft)
		e.draft = ""
		return
	}
	e.setValueLocked(v)
}

func (e *LineEditor) browsing() bool {
	if e.history == nil {
		return false
	}
	e.history.mu.Lock()
	defer e.history.mu.Unlock()
	return e.history.idx >= 0
}

// Render returns the prompt line as it should appear on screen (prompt +
// buffer, or the reverse-search prompt), plus the cursor column offset in
// display cells from the start of the line.
func (e *LineEditor) Render(prompt string) (string, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.searching {
		line := Dim("(reverse-i-search)`") + Yellow(string(e.searchQ)) + Dim("': ") + e.searchHit
		return line, VisibleWidth(line)
	}
	line := prompt + string(e.buf)
	col := VisibleWidth(prompt) + StringWidth(string(e.buf[:e.cursor]))
	return line, col
}
