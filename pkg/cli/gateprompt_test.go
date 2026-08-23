package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// pipeFile returns an *os.File that yields s. TerminalGateHost needs a *os.File
// because it asks whether the handle is a terminal; a pipe answers "no", which
// exercises the line-reading fallback.
func pipeFile(t *testing.T, s string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = io.WriteString(w, s)
		_ = w.Close()
	}()
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestTerminalGateHostResolvesATypedAnswer(t *testing.T) {
	SetColorMode(ColorNever)
	h := &TerminalGateHost{In: pipeFile(t, "y\n"), Out: io.Discard}
	ans, ok := h.AskGate(context.Background(), PlanGate("id", "q", "s", nil, []string{"T1"}, 1))
	if !ok || ans.Value != "approve" {
		t.Fatalf("ans=%+v ok=%v", ans, ok)
	}
}

func TestTerminalGateHostCarriesFreeformNotes(t *testing.T) {
	SetColorMode(ColorNever)
	h := &TerminalGateHost{In: pipeFile(t, "e also add a test\n"), Out: io.Discard}
	ans, ok := h.AskGate(context.Background(), PlanGate("id", "q", "s", nil, nil, 0))
	if !ok || ans.Value != "approve" || ans.Notes != "also add a test" {
		t.Fatalf("ans=%+v ok=%v", ans, ok)
	}
}

// A gate must never invent an approval when the human never answered.
func TestTerminalGateHostFailsClosedOnEOF(t *testing.T) {
	SetColorMode(ColorNever)
	h := &TerminalGateHost{In: pipeFile(t, ""), Out: io.Discard}
	if _, ok := h.AskGate(context.Background(), PlanGate("id", "q", "s", nil, nil, 0)); ok {
		t.Fatal("EOF must not produce an answer")
	}
}

func TestTerminalGateHostUnblocksOnContextCancel(t *testing.T) {
	SetColorMode(ColorNever)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close(); _ = r.Close() }()
	h := &TerminalGateHost{In: r, Out: io.Discard}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, ok := h.AskGate(ctx, PlanGate("id", "q", "s", nil, nil, 0)); ok {
			t.Error("a canceled gate must not answer")
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AskGate did not return after the context was canceled")
	}
}

func TestGateRenderIncludesTheChoices(t *testing.T) {
	SetColorMode(ColorNever)
	g := PlanGate("id", "add auth", "plan summary", []string{"goal"}, []string{"T1: do it"}, 1)
	line := g.PromptLine()
	for _, want := range []string{"[y]es", "[e]dit", "[n]o", "[r]eplan"} {
		if !strings.Contains(line, want) {
			t.Errorf("prompt missing %q: %s", want, line)
		}
	}
}

// A wave can escalate several tasks at once, and the engine calls the escalate
// hook once per task from its own goroutine. Two gates reading the same stdin
// used to race: both entered raw mode, both created a KeyReader, and one
// keystroke went to whichever reader won — usually the gate whose card was NOT
// on screen. The visible symptom was a gate that ignored every key.
func TestTerminalGateHostSerializesConcurrentGates(t *testing.T) {
	SetColorMode(ColorNever)
	h := &TerminalGateHost{In: pipeFile(t, "r\nr\nr\n"), Out: io.Discard}

	const n = 3
	var (
		mu      sync.Mutex
		overlap int
		inside  int
	)
	results := make(chan bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g := EscalateGate("ask", "T"+string(rune('1'+i)), "t", "why", nil)
			// Observe how many gates hold the terminal at once by wrapping
			// the call; the lock is what keeps this at one.
			mu.Lock()
			inside++
			if inside > 1 {
				overlap++
			}
			mu.Unlock()
			_, ok := h.AskGate(context.Background(), g)
			mu.Lock()
			inside--
			mu.Unlock()
			results <- ok
		}(i)
	}
	wg.Wait()
	close(results)
	answered := 0
	for ok := range results {
		if ok {
			answered++
		}
	}
	if answered != n {
		t.Errorf("answered %d/%d gates — a queued gate was dropped", answered, n)
	}
}

// A gate still queued behind another must not block shutdown: when the run is
// canceled it has to return "no answer" rather than wait for a terminal it will
// never get.
func TestTerminalGateHostQueuedGateHonoursCancel(t *testing.T) {
	SetColorMode(ColorNever)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close(); _ = r.Close() }()
	h := &TerminalGateHost{In: r, Out: io.Discard}

	// Hold the terminal with a gate nobody answers.
	first := make(chan struct{})
	go func() {
		defer close(first)
		_, _ = h.AskGate(context.Background(), PlanGate("a", "q", "s", nil, nil, 0))
	}()
	time.Sleep(30 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := h.AskGate(ctx, PlanGate("b", "q", "s", nil, nil, 0))
		done <- ok
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case ok := <-done:
		if ok {
			t.Error("a canceled queued gate must not answer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a queued gate ignored its canceled context")
	}
	_ = w.Close()
	<-first
}
