package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestGatePromptLineMarksAccelerators(t *testing.T) {
	SetColorMode(ColorNever)
	g := PlanGate("id", "q", "summary", nil, nil, 0)
	line := g.PromptLine()
	for _, want := range []string{"Approve plan?", "[y]es", "[e]dit", "[n]o", "[r]eplan", "›"} {
		if !strings.Contains(line, want) {
			t.Fatalf("prompt %q missing %q", line, want)
		}
	}
}

func TestGateResolveByLetter(t *testing.T) {
	g := PlanGate("id", "q", "s", nil, nil, 0)
	for in, want := range map[string]string{
		"y":       "approve",
		"yes":     "approve",
		"n":       "reject",
		"r":       "replan",
		"replan":  "replan",
		"approve": "approve",
	} {
		got, ok := g.Resolve(in)
		if !ok || got.Value != want {
			t.Errorf("Resolve(%q)=%+v ok=%v want %q", in, got, ok, want)
		}
	}
}

func TestGateResolveCarriesNotes(t *testing.T) {
	g := PlanGate("id", "q", "s", nil, nil, 0)
	got, ok := g.Resolve("e also add tests")
	if !ok || got.Value != "approve" || got.Notes != "also add tests" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestGateResolveFreeTextFallsBackToFreeformOption(t *testing.T) {
	g := PlanGate("id", "q", "s", nil, nil, 0)
	got, ok := g.Resolve("please split task 2 in half")
	if !ok || got.Value != "approve" || got.Notes != "please split task 2 in half" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestGateResolveEmptyFails(t *testing.T) {
	g := ContinueGate("id", "why", "state", nil, nil)
	if _, ok := g.Resolve("   "); ok {
		t.Fatal("empty input must not resolve")
	}
}

func TestGateResolveKeySkipsFreeformOptions(t *testing.T) {
	g := PlanGate("id", "q", "s", nil, nil, 0)
	if _, ok := g.ResolveKey(Key{Type: KeyRune, Rune: 'e'}); ok {
		t.Fatal("the edit option needs typed follow-up, not a bare keystroke")
	}
	got, ok := g.ResolveKey(Key{Type: KeyRune, Rune: 'Y'})
	if !ok || got.Value != "approve" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestGateNonTTYDefaultsAreConservative(t *testing.T) {
	if PlanGate("i", "", "", nil, nil, 0).NonTTYDefault != "reject" {
		t.Fatal("a plan gate must never default to approving")
	}
	if ContinueGate("i", "", "", nil, nil).NonTTYDefault != "stop" {
		t.Fatal("a continue gate must default to stop")
	}
}

func TestEscalateGateOptions(t *testing.T) {
	g := EscalateGate("i", "T3", "fix parser", "3 failed reviews", []string{"pkg/x.go"})
	vals := map[string]bool{}
	for _, o := range g.Options {
		vals[o.Value] = true
	}
	for _, want := range []string{"retry", "re_scope", "mark_done", "abort"} {
		if !vals[want] {
			t.Fatalf("missing option %q", want)
		}
	}
	body := strings.Join(g.Body, "\n")
	if !strings.Contains(body, "T3") || !strings.Contains(body, "pkg/x.go") {
		t.Fatalf("body=%q", body)
	}
}

func TestClarifyGateNumbersOptions(t *testing.T) {
	g := ClarifyGate("i", "Which language?", []string{"Go", "Python"}, "Go")
	got, ok := g.Resolve("1")
	if !ok || got.Value != "Go" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	got, ok = g.Resolve("a")
	if !ok || got.Value != "__recommended__" {
		t.Fatalf("auto option: %+v ok=%v", got, ok)
	}
}

func TestGateRenderWrapsToWidth(t *testing.T) {
	SetColorMode(ColorNever)
	long := strings.Repeat("word ", 60)
	g := PlanGate("i", long, long, nil, nil, 3)
	out := g.Render(70)
	for _, line := range strings.Split(out, "\n") {
		if VisibleWidth(line) > 72 {
			t.Fatalf("gate line too wide (%d): %q", VisibleWidth(line), line)
		}
	}
}

func TestParseGateTimeoutPolicy(t *testing.T) {
	for in, want := range map[string]GateTimeoutPolicy{
		"":        GateTimeoutStop,
		"stop":    GateTimeoutStop,
		"approve": GateTimeoutApprove,
		"reject":  GateTimeoutReject,
	} {
		got, ok := ParseGateTimeoutPolicy(in)
		if !ok || got != want {
			t.Errorf("ParseGateTimeoutPolicy(%q)=%v ok=%v want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseGateTimeoutPolicy("maybe"); ok {
		t.Fatal("invalid policy must be rejected")
	}
}

func TestWrapPlainKeepsWords(t *testing.T) {
	lines := wrapPlain("alpha beta gamma delta", 11)
	if len(lines) < 2 {
		t.Fatalf("expected wrapping, got %v", lines)
	}
	for _, l := range lines {
		if StringWidth(l) > 11 {
			t.Fatalf("line too wide: %q", l)
		}
	}
	if strings.Join(lines, " ") != "alpha beta gamma delta" {
		t.Fatalf("content changed: %v", lines)
	}
}

func TestAskGateResolvesFromTheSessionLoop(t *testing.T) {
	SetColorMode(ColorNever)
	s := NewLiveSession()
	s.SetIO(strings.NewReader(""), io.Discard, false)

	g := ContinueGate("g1", "retries exhausted", "2 tasks left", nil, nil)
	done := make(chan GateAnswer, 1)
	go func() {
		ans, ok := s.AskGate(context.Background(), g)
		if !ok {
			t.Errorf("gate was not answered")
		}
		done <- ans
	}()

	// Wait for the gate to become visible, then answer it the way the run loop
	// would after a submitted line.
	deadline := time.Now().Add(2 * time.Second)
	for s.PendingGate() == nil {
		if time.Now().After(deadline) {
			t.Fatal("gate never became pending")
		}
		time.Sleep(5 * time.Millisecond)
	}
	pending := s.PendingGate()
	ans, ok := pending.Resolve("c")
	if !ok {
		t.Fatal("could not resolve 'c'")
	}
	if !s.answerGate(ans) {
		t.Fatal("answerGate rejected the reply")
	}

	select {
	case got := <-done:
		if got.Value != "continue" {
			t.Fatalf("answer=%+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskGate did not return")
	}
	if s.PendingGate() != nil {
		t.Fatal("gate should be cleared after answering")
	}
}

func TestAskGateUnblocksOnContextCancel(t *testing.T) {
	s := NewLiveSession()
	s.SetIO(strings.NewReader(""), io.Discard, false)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan bool, 1)
	go func() {
		_, ok := s.AskGate(ctx, PlanGate("g", "q", "s", nil, nil, 1))
		done <- ok
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("a cancelled gate must not report an answer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskGate did not unblock on cancel")
	}
}
