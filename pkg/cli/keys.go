package cli

import (
	"bufio"
	"io"
	"unicode/utf8"
)

// KeyType classifies a decoded keystroke.
type KeyType int

const (
	KeyRune KeyType = iota
	KeyEnter
	KeyBackspace
	KeyDelete
	KeyTab
	KeyShiftTab
	KeyEscape
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyCtrlC
	KeyCtrlD
	KeyCtrlA
	KeyCtrlE
	KeyCtrlK
	KeyCtrlU
	KeyCtrlW
	KeyCtrlL
	KeyCtrlR
	KeyCtrlN
	KeyCtrlP
	KeyUnknown
)

// Key is one decoded keystroke.
type Key struct {
	Type KeyType
	Rune rune
}

// String renders a key for help text and tests.
func (k Key) String() string {
	switch k.Type {
	case KeyRune:
		return string(k.Rune)
	case KeyEnter:
		return "enter"
	case KeyBackspace:
		return "backspace"
	case KeyDelete:
		return "delete"
	case KeyTab:
		return "tab"
	case KeyShiftTab:
		return "shift-tab"
	case KeyEscape:
		return "esc"
	case KeyUp:
		return "up"
	case KeyDown:
		return "down"
	case KeyLeft:
		return "left"
	case KeyRight:
		return "right"
	case KeyHome:
		return "home"
	case KeyEnd:
		return "end"
	case KeyPageUp:
		return "pgup"
	case KeyPageDown:
		return "pgdn"
	case KeyCtrlC:
		return "ctrl-c"
	case KeyCtrlD:
		return "ctrl-d"
	case KeyCtrlA:
		return "ctrl-a"
	case KeyCtrlE:
		return "ctrl-e"
	case KeyCtrlK:
		return "ctrl-k"
	case KeyCtrlU:
		return "ctrl-u"
	case KeyCtrlW:
		return "ctrl-w"
	case KeyCtrlL:
		return "ctrl-l"
	case KeyCtrlR:
		return "ctrl-r"
	case KeyCtrlN:
		return "ctrl-n"
	case KeyCtrlP:
		return "ctrl-p"
	default:
		return "?"
	}
}

// KeyReader decodes a byte stream into keystrokes. It is deliberately
// independent of the terminal so tests can drive it with a scripted reader.
type KeyReader struct {
	br *bufio.Reader
}

// NewKeyReader wraps r.
func NewKeyReader(r io.Reader) *KeyReader {
	return &KeyReader{br: bufio.NewReaderSize(r, 256)}
}

// ReadKey blocks for the next keystroke. It returns io.EOF at end of input.
//
// Escape handling: a lone ESC with no immediately buffered follow-up bytes is
// reported as KeyEscape (that is how a user pressing Esc is distinguished from
// a CSI sequence, which always arrives in one read).
func (k *KeyReader) ReadKey() (Key, error) {
	b, err := k.br.ReadByte()
	if err != nil {
		return Key{Type: KeyUnknown}, err
	}
	switch b {
	case 0x0d, 0x0a:
		return Key{Type: KeyEnter}, nil
	case 0x09:
		return Key{Type: KeyTab}, nil
	case 0x7f, 0x08:
		return Key{Type: KeyBackspace}, nil
	case 0x01:
		return Key{Type: KeyCtrlA}, nil
	case 0x03:
		return Key{Type: KeyCtrlC}, nil
	case 0x04:
		return Key{Type: KeyCtrlD}, nil
	case 0x05:
		return Key{Type: KeyCtrlE}, nil
	case 0x0b:
		return Key{Type: KeyCtrlK}, nil
	case 0x0c:
		return Key{Type: KeyCtrlL}, nil
	case 0x0e:
		return Key{Type: KeyCtrlN}, nil
	case 0x10:
		return Key{Type: KeyCtrlP}, nil
	case 0x12:
		return Key{Type: KeyCtrlR}, nil
	case 0x15:
		return Key{Type: KeyCtrlU}, nil
	case 0x17:
		return Key{Type: KeyCtrlW}, nil
	case 0x1b:
		return k.readEscape()
	}
	if b < 0x20 {
		return Key{Type: KeyUnknown}, nil
	}
	if b < utf8.RuneSelf {
		return Key{Type: KeyRune, Rune: rune(b)}, nil
	}
	// Multi-byte UTF-8: push the lead byte back and decode a full rune.
	if err := k.br.UnreadByte(); err != nil {
		return Key{Type: KeyUnknown}, nil
	}
	r, _, err := k.br.ReadRune()
	if err != nil {
		return Key{Type: KeyUnknown}, err
	}
	return Key{Type: KeyRune, Rune: r}, nil
}

func (k *KeyReader) readEscape() (Key, error) {
	// Only '[' (CSI) and 'O' (application cursor mode) continue a sequence.
	// Anything else — including nothing at all — is the user pressing Esc, and
	// the following byte must stay in the buffer so it is not swallowed.
	if k.br.Buffered() == 0 {
		return Key{Type: KeyEscape}, nil
	}
	peek, err := k.br.Peek(1)
	if err != nil || len(peek) == 0 || (peek[0] != '[' && peek[0] != 'O') {
		return Key{Type: KeyEscape}, nil
	}
	b, err := k.br.ReadByte()
	if err != nil {
		return Key{Type: KeyEscape}, nil
	}
	switch b {
	case '[':
		return k.readCSI()
	case 'O': // application cursor mode
		c, err := k.br.ReadByte()
		if err != nil {
			return Key{Type: KeyEscape}, nil
		}
		switch c {
		case 'A':
			return Key{Type: KeyUp}, nil
		case 'B':
			return Key{Type: KeyDown}, nil
		case 'C':
			return Key{Type: KeyRight}, nil
		case 'D':
			return Key{Type: KeyLeft}, nil
		case 'H':
			return Key{Type: KeyHome}, nil
		case 'F':
			return Key{Type: KeyEnd}, nil
		}
		return Key{Type: KeyUnknown}, nil
	}
	return Key{Type: KeyEscape}, nil
}

func (k *KeyReader) readCSI() (Key, error) {
	var params []byte
	for {
		b, err := k.br.ReadByte()
		if err != nil {
			return Key{Type: KeyUnknown}, nil
		}
		if b >= 0x40 && b <= 0x7e {
			return csiKey(b, params), nil
		}
		params = append(params, b)
		if len(params) > 32 {
			return Key{Type: KeyUnknown}, nil
		}
	}
}

func csiKey(final byte, params []byte) Key {
	switch final {
	case 'A':
		return Key{Type: KeyUp}
	case 'B':
		return Key{Type: KeyDown}
	case 'C':
		return Key{Type: KeyRight}
	case 'D':
		return Key{Type: KeyLeft}
	case 'H':
		return Key{Type: KeyHome}
	case 'F':
		return Key{Type: KeyEnd}
	case 'Z':
		return Key{Type: KeyShiftTab}
	case '~':
		switch string(params) {
		case "1", "7":
			return Key{Type: KeyHome}
		case "3":
			return Key{Type: KeyDelete}
		case "4", "8":
			return Key{Type: KeyEnd}
		case "5":
			return Key{Type: KeyPageUp}
		case "6":
			return Key{Type: KeyPageDown}
		}
	}
	return Key{Type: KeyUnknown}
}
