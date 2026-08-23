package cli

import (
	"context"
	"io"
	"os"
	"strings"
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
