package cli

import (
	"io"
	"sync"
)

// InputKind classifies what the input pump produced.
type InputKind int

const (
	InputLine      InputKind = iota // a completed line (Line)
	InputRedraw                     // buffer changed — repaint the prompt
	InputCancel                     // Esc
	InputInterrupt                  // Ctrl-C on an empty buffer
	InputEOF                        // Ctrl-D on an empty buffer / stream closed
	InputComplete                   // Tab
	InputClear                      // Ctrl-L
	InputHotkey                     // a key claimed by the host's hotkey filter
)

// InputEvent is one thing the user did.
type InputEvent struct {
	Kind InputKind
	Line string
	Key  Key
}

// InputPump reads keystrokes on its own goroutine and publishes events on a
// channel. Decoupling input from the run loop is what makes the REPL
// steerable: the agent can be mid-run while the user types.
type InputPump struct {
	ed  *LineEditor
	ch  chan InputEvent
	kr  *KeyReader
	mu  sync.Mutex
	hot func(Key) bool
	// closed guards against double-close from Stop.
	stopOnce sync.Once
	done     chan struct{}
}

// StartInputPump begins reading r and returns the pump. Call Stop to release
// the goroutine (the goroutine also exits when r reaches EOF).
func StartInputPump(r io.Reader, ed *LineEditor) *InputPump {
	p := &InputPump{
		ed:   ed,
		ch:   make(chan InputEvent, 64),
		kr:   NewKeyReader(r),
		done: make(chan struct{}),
	}
	go p.loop()
	return p
}

// Events is the event channel; it is closed when input ends.
func (p *InputPump) Events() <-chan InputEvent { return p.ch }

// Stop releases the pump. The reader goroutine may stay blocked on a read until
// the underlying stream yields, which is fine — it publishes nothing further.
func (p *InputPump) Stop() {
	p.stopOnce.Do(func() { close(p.done) })
}

// SetHotkeys installs a predicate consulted before the editor sees a key. When
// it returns true the key is delivered as InputHotkey and never reaches the
// buffer. Used for single-keystroke gate answers ([y]/[n]/[e]/[r]).
func (p *InputPump) SetHotkeys(fn func(Key) bool) {
	p.mu.Lock()
	p.hot = fn
	p.mu.Unlock()
}

func (p *InputPump) hotkey(k Key) bool {
	p.mu.Lock()
	fn := p.hot
	p.mu.Unlock()
	return fn != nil && fn(k)
}

func (p *InputPump) emit(e InputEvent) bool {
	select {
	case <-p.done:
		return false
	case p.ch <- e:
		return true
	}
}

func (p *InputPump) loop() {
	defer close(p.ch)
	for {
		select {
		case <-p.done:
			return
		default:
		}
		k, err := p.kr.ReadKey()
		if err != nil {
			p.emit(InputEvent{Kind: InputEOF})
			return
		}
		if p.hotkey(k) {
			if !p.emit(InputEvent{Kind: InputHotkey, Key: k}) {
				return
			}
			continue
		}
		var ev InputEvent
		switch p.ed.Feed(k) {
		case ActSubmit:
			ev = InputEvent{Kind: InputLine, Line: p.ed.Submitted(), Key: k}
		case ActCancel:
			ev = InputEvent{Kind: InputCancel, Key: k}
		case ActInterrupt:
			ev = InputEvent{Kind: InputInterrupt, Key: k}
		case ActEOF:
			p.emit(InputEvent{Kind: InputEOF})
			return
		case ActComplete:
			ev = InputEvent{Kind: InputComplete, Key: k}
		case ActClear:
			ev = InputEvent{Kind: InputClear, Key: k}
		case ActRedraw:
			ev = InputEvent{Kind: InputRedraw, Key: k}
		default:
			continue
		}
		if !p.emit(ev) {
			return
		}
	}
}
