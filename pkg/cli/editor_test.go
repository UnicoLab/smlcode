package cli

import (
	"strings"
	"testing"
)

// feed drives the editor with a scripted key sequence.
func feed(e *LineEditor, keys ...Key) EditorAction {
	last := ActNone
	for _, k := range keys {
		last = e.Feed(k)
	}
	return last
}

func runes(s string) []Key {
	out := make([]Key, 0, len(s))
	for _, r := range s {
		out = append(out, Key{Type: KeyRune, Rune: r})
	}
	return out
}

func TestEditorTypeAndSubmit(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("fix the parser")...)
	if e.Value() != "fix the parser" {
		t.Fatalf("value=%q", e.Value())
	}
	if act := e.Feed(Key{Type: KeyEnter}); act != ActSubmit {
		t.Fatalf("act=%v", act)
	}
	if e.Submitted() != "fix the parser" {
		t.Fatalf("submitted=%q", e.Submitted())
	}
	if e.Value() != "" {
		t.Fatalf("buffer not reset: %q", e.Value())
	}
}

func TestEditorBackspaceAndCursor(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("abcd")...)
	feed(e, Key{Type: KeyLeft}, Key{Type: KeyLeft})
	if e.Cursor() != 2 {
		t.Fatalf("cursor=%d", e.Cursor())
	}
	e.Feed(Key{Type: KeyBackspace})
	if e.Value() != "acd" {
		t.Fatalf("value=%q", e.Value())
	}
	e.Feed(Key{Type: KeyDelete})
	if e.Value() != "ad" {
		t.Fatalf("value=%q", e.Value())
	}
}

func TestEditorInsertMidBuffer(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("ac")...)
	e.Feed(Key{Type: KeyLeft})
	e.Feed(Key{Type: KeyRune, Rune: 'b'})
	if e.Value() != "abc" {
		t.Fatalf("value=%q", e.Value())
	}
}

func TestEditorHomeEndKillLine(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("hello world")...)
	e.Feed(Key{Type: KeyHome})
	if e.Cursor() != 0 {
		t.Fatalf("cursor=%d", e.Cursor())
	}
	e.Feed(Key{Type: KeyCtrlK}) // kill to end from position 0
	if e.Value() != "" {
		t.Fatalf("ctrl-k left %q", e.Value())
	}
	feed(e, runes("abc def")...)
	e.Feed(Key{Type: KeyCtrlU})
	if e.Value() != "" {
		t.Fatalf("ctrl-u left %q", e.Value())
	}
}

func TestEditorDeleteWord(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("one two three")...)
	e.Feed(Key{Type: KeyCtrlW})
	if e.Value() != "one two " {
		t.Fatalf("value=%q", e.Value())
	}
}

func TestEditorCtrlCClearsThenInterrupts(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("draft")...)
	if act := e.Feed(Key{Type: KeyCtrlC}); act != ActRedraw {
		t.Fatalf("first ctrl-c act=%v", act)
	}
	if e.Value() != "" {
		t.Fatal("buffer should be cleared")
	}
	if act := e.Feed(Key{Type: KeyCtrlC}); act != ActInterrupt {
		t.Fatalf("second ctrl-c act=%v", act)
	}
}

func TestEditorCtrlDOnEmptyIsEOF(t *testing.T) {
	e := NewLineEditor(nil)
	if act := e.Feed(Key{Type: KeyCtrlD}); act != ActEOF {
		t.Fatalf("act=%v", act)
	}
	feed(e, runes("x")...)
	if act := e.Feed(Key{Type: KeyCtrlD}); act == ActEOF {
		t.Fatal("ctrl-d with content must not quit")
	}
}

func TestEditorEscapeIsCancel(t *testing.T) {
	e := NewLineEditor(nil)
	if act := e.Feed(Key{Type: KeyEscape}); act != ActCancel {
		t.Fatalf("act=%v", act)
	}
}

func TestEditorTabRequestsCompletion(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("/perm")...)
	if act := e.Feed(Key{Type: KeyTab}); act != ActComplete {
		t.Fatalf("act=%v", act)
	}
}

func TestEditorHistoryUpDown(t *testing.T) {
	h := LoadPromptHistory("") // in-memory only
	h.Add("first query")
	h.Add("second query")
	e := NewLineEditor(h)

	e.Feed(Key{Type: KeyUp})
	if e.Value() != "second query" {
		t.Fatalf("up#1=%q", e.Value())
	}
	e.Feed(Key{Type: KeyUp})
	if e.Value() != "first query" {
		t.Fatalf("up#2=%q", e.Value())
	}
	e.Feed(Key{Type: KeyDown})
	if e.Value() != "second query" {
		t.Fatalf("down#1=%q", e.Value())
	}
}

func TestEditorHistoryRestoresDraft(t *testing.T) {
	h := LoadPromptHistory("")
	h.Add("older")
	e := NewLineEditor(h)
	feed(e, runes("draft in progress")...)
	e.Feed(Key{Type: KeyUp})
	if e.Value() != "older" {
		t.Fatalf("up=%q", e.Value())
	}
	e.Feed(Key{Type: KeyDown})
	if e.Value() != "draft in progress" {
		t.Fatalf("draft not restored: %q", e.Value())
	}
}

func TestEditorReverseSearch(t *testing.T) {
	h := LoadPromptHistory("")
	h.Add("add jwt auth")
	h.Add("fix flaky test")
	e := NewLineEditor(h)

	e.Feed(Key{Type: KeyCtrlR})
	if !e.Searching() {
		t.Fatal("expected reverse-search mode")
	}
	feed(e, runes("jwt")...)
	if e.SearchHit() != "add jwt auth" {
		t.Fatalf("hit=%q", e.SearchHit())
	}
	e.Feed(Key{Type: KeyEnter})
	if e.Searching() {
		t.Fatal("search should have ended")
	}
	if e.Value() != "add jwt auth" {
		t.Fatalf("value=%q", e.Value())
	}
}

func TestEditorReverseSearchEscapeAborts(t *testing.T) {
	h := LoadPromptHistory("")
	h.Add("something")
	e := NewLineEditor(h)
	e.Feed(Key{Type: KeyCtrlR})
	feed(e, runes("some")...)
	e.Feed(Key{Type: KeyEscape})
	if e.Searching() || e.Value() != "" {
		t.Fatalf("searching=%v value=%q", e.Searching(), e.Value())
	}
}

func TestEditorRenderCursorColumn(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("abc")...)
	e.Feed(Key{Type: KeyLeft})
	line, col := e.Render("slm › ")
	if !strings.HasSuffix(line, "abc") {
		t.Fatalf("line=%q", line)
	}
	// prompt is 6 cells; cursor sits before 'c'
	if col != 6+2 {
		t.Fatalf("col=%d", col)
	}
}

func TestEditorRenderWideRuneCursor(t *testing.T) {
	e := NewLineEditor(nil)
	feed(e, runes("世界")...)
	_, col := e.Render("> ")
	if col != 2+4 {
		t.Fatalf("col=%d want 6 (wide runes are 2 cells each)", col)
	}
}
