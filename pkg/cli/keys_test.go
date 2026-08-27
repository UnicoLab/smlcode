package cli

import (
	"io"
	"strings"
	"testing"
	"time"
)

// readAll drains a scripted byte stream into decoded keys.
func readAll(t *testing.T, input string) []Key {
	t.Helper()
	kr := NewKeyReader(strings.NewReader(input))
	var out []Key
	for {
		k, err := kr.ReadKey()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("ReadKey: %v", err)
		}
		out = append(out, k)
	}
}

func TestKeyReaderPrintable(t *testing.T) {
	keys := readAll(t, "abc")
	if len(keys) != 3 {
		t.Fatalf("got %d keys", len(keys))
	}
	for i, want := range []rune{'a', 'b', 'c'} {
		if keys[i].Type != KeyRune || keys[i].Rune != want {
			t.Fatalf("key %d = %+v", i, keys[i])
		}
	}
}

func TestKeyReaderMultibyteRune(t *testing.T) {
	keys := readAll(t, "héllo →")
	var got []rune
	for _, k := range keys {
		if k.Type == KeyRune {
			got = append(got, k.Rune)
		}
	}
	if string(got) != "héllo →" {
		t.Fatalf("decoded %q", string(got))
	}
}

func TestKeyReaderControlKeys(t *testing.T) {
	cases := map[string]KeyType{
		"\r":      KeyEnter,
		"\n":      KeyEnter,
		"\t":      KeyTab,
		"\x7f":    KeyBackspace,
		"\x01":    KeyCtrlA,
		"\x03":    KeyCtrlC,
		"\x04":    KeyCtrlD,
		"\x05":    KeyCtrlE,
		"\x0b":    KeyCtrlK,
		"\x0c":    KeyCtrlL,
		"\x12":    KeyCtrlR,
		"\x15":    KeyCtrlU,
		"\x17":    KeyCtrlW,
		"\x1b":    KeyEscape,
		"\x1b[A":  KeyUp,
		"\x1b[B":  KeyDown,
		"\x1b[C":  KeyRight,
		"\x1b[D":  KeyLeft,
		"\x1b[H":  KeyHome,
		"\x1b[F":  KeyEnd,
		"\x1b[Z":  KeyShiftTab,
		"\x1bOA":  KeyUp,
		"\x1b[3~": KeyDelete,
		"\x1b[5~": KeyPageUp,
		"\x1b[6~": KeyPageDown,
	}
	for in, want := range cases {
		keys := readAll(t, in)
		if len(keys) == 0 {
			t.Fatalf("%q produced no key", in)
		}
		if keys[0].Type != want {
			t.Errorf("%q → %v want %v", in, keys[0], Key{Type: want})
		}
	}
}

func TestKeyReaderLoneEscapeIsEscape(t *testing.T) {
	// A bare Esc with nothing buffered behind it is the user pressing Esc.
	keys := readAll(t, "\x1b")
	if len(keys) != 1 || keys[0].Type != KeyEscape {
		t.Fatalf("got %+v", keys)
	}
}

func TestKeyReaderUnknownCSIDoesNotStall(t *testing.T) {
	keys := readAll(t, "\x1b[200~x")
	// The bracketed-paste marker is unknown but must not swallow the 'x'.
	last := keys[len(keys)-1]
	if last.Type != KeyRune || last.Rune != 'x' {
		t.Fatalf("expected trailing rune, got %+v", keys)
	}
}

func TestKeyStringNames(t *testing.T) {
	if (Key{Type: KeyCtrlC}).String() != "ctrl-c" {
		t.Fatal("bad key name")
	}
	if (Key{Type: KeyRune, Rune: 'q'}).String() != "q" {
		t.Fatal("bad rune name")
	}
}

func TestInputPumpDeliversLines(t *testing.T) {
	ed := NewLineEditor(nil)
	p := StartInputPump(strings.NewReader("hello\nworld\n"), ed)
	defer p.Stop()

	var lines []string
	deadline := time.After(2 * time.Second)
	for len(lines) < 2 {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				t.Fatalf("channel closed early, lines=%v", lines)
			}
			if ev.Kind == InputLine {
				lines = append(lines, ev.Line)
			}
		case <-deadline:
			t.Fatalf("timed out, lines=%v", lines)
		}
	}
	if lines[0] != "hello" || lines[1] != "world" {
		t.Fatalf("lines=%v", lines)
	}
}

func TestInputPumpEscapeAndInterrupt(t *testing.T) {
	ed := NewLineEditor(nil)
	p := StartInputPump(strings.NewReader("\x1b\x03"), ed)
	defer p.Stop()

	var kinds []InputKind
	deadline := time.After(2 * time.Second)
	for len(kinds) < 2 {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				t.Fatalf("closed early: %v", kinds)
			}
			kinds = append(kinds, ev.Kind)
		case <-deadline:
			t.Fatalf("timed out: %v", kinds)
		}
	}
	if kinds[0] != InputCancel {
		t.Fatalf("first event = %v want InputCancel", kinds[0])
	}
	if kinds[1] != InputInterrupt {
		t.Fatalf("second event = %v want InputInterrupt", kinds[1])
	}
}

func TestInputPumpHotkeyFilter(t *testing.T) {
	ed := NewLineEditor(nil)

	// A PIPE, not strings.NewReader: StartInputPump launches its read loop
	// immediately, and SetHotkeys installs the filter afterwards. With input
	// already buffered, the pump can read and classify 'y' before the filter
	// lands, and the test then sees a plain rune event instead of a hotkey.
	// That is a race in the TEST's setup, not in the pump — the filter itself
	// is mutex-guarded — and it failed exactly this way on a loaded CI runner:
	//
	//	--- FAIL: TestInputPumpHotkeyFilter
	//	    keys_test.go:177: got {Kind:1 Line: Key:y}
	//
	// Writing only after SetHotkeys returns makes the ordering the test means
	// to assert the one it actually gets.
	pr, pw := io.Pipe()
	p := StartInputPump(pr, ed)
	defer p.Stop()
	p.SetHotkeys(func(k Key) bool { return k.Type == KeyRune && k.Rune == 'y' })
	go func() {
		_, _ = pw.Write([]byte("y"))
		_ = pw.Close()
	}()

	select {
	case ev := <-p.Events():
		if ev.Kind != InputHotkey || ev.Key.Rune != 'y' {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
	if ed.Value() != "" {
		t.Fatalf("hotkey leaked into the buffer: %q", ed.Value())
	}
}
