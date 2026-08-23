package cli

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

// scriptedSession wires a LiveSession to a scripted input stream and a
// discarding output, so the whole REPL loop can be driven from a test.
func scriptedSession(t *testing.T, script string) *LiveSession {
	t.Helper()
	SetColorMode(ColorNever)
	s := NewLiveSession()
	s.SetIO(strings.NewReader(script), io.Discard, false)
	s.SetShowDashboard(false)
	s.SetSlashRegistry(testRegistry())
	return s
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestREPLAcceptsCommandsWhileARunIsInFlight is the headline regression: the
// old loop called onRun synchronously from the input loop, so /stop, /feedback,
// /permission and every other steering command were unreachable until the run
// finished.
func TestREPLAcceptsCommandsWhileARunIsInFlight(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var slashDuringRun []string
	runStarted := make(chan struct{})
	stopped := make(chan struct{}, 1)

	s := scriptedSession(t, "do the thing\n/stop\n/q\n")
	s.OnRun(func(q string) error {
		close(runStarted)
		<-release // stays busy until the test lets go
		return nil
	})
	s.OnStop(func() {
		select {
		case stopped <- struct{}{}:
		default:
		}
	})
	handler := func(line string) (bool, error) {
		mu.Lock()
		slashDuringRun = append(slashDuringRun, line)
		mu.Unlock()
		if strings.HasPrefix(line, "/stop") {
			close(release)
		}
		return strings.HasPrefix(line, "/q"), nil
	}
	s.OnSlash(handler)
	s.OnLiveSlash(handler)

	done := make(chan error, 1)
	go func() { done <- s.RunInteractive() }()

	select {
	case <-runStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the run never started")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunInteractive: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunInteractive never returned — the REPL is still blocking on the run")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(slashDuringRun) == 0 || slashDuringRun[0] != "/stop" {
		t.Fatalf("/stop was not delivered during the run: %v", slashDuringRun)
	}
}

// TestREPLEscapeCancelsAndCollectsRedirection covers Esc → cancel → "what
// should I change?" → the typed redirection feeding the next run.
func TestREPLEscapeCancelsAndCollectsRedirection(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var queries []string
	var steered []string
	stopCalled := make(chan struct{}, 1)
	secondRun := make(chan struct{})

	// Input is driven through a pipe so the test controls exactly when the
	// quit command is delivered — otherwise it can race ahead of the restart.
	pr, pw := io.Pipe()
	SetColorMode(ColorNever)
	s := NewLiveSession()
	s.SetIO(pr, io.Discard, false)
	s.SetShowDashboard(false)
	s.SetSlashRegistry(testRegistry())

	s.OnRun(func(q string) error {
		mu.Lock()
		queries = append(queries, q)
		n := len(queries)
		mu.Unlock()
		switch n {
		case 1:
			<-release
		case 2:
			close(secondRun)
		}
		return nil
	})
	s.OnStop(func() {
		select {
		case stopCalled <- struct{}{}:
			close(release)
		default:
		}
	})
	s.OnSteer(func(text string) {
		mu.Lock()
		steered = append(steered, text)
		mu.Unlock()
	})
	quit := func(line string) (bool, error) { return strings.HasPrefix(line, "/q"), nil }
	s.OnSlash(quit)
	s.OnLiveSlash(quit)

	done := make(chan error, 1)
	go func() { done <- s.RunInteractive() }()

	writeOrFail := func(sfx string) {
		if _, err := pw.Write([]byte(sfx)); err != nil {
			t.Errorf("write %q: %v", sfx, err)
		}
	}
	writeOrFail("first task\n")
	waitFor(t, "the first run to start", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(queries) == 1
	})

	writeOrFail("\x1b")                   // Esc → interrupt
	writeOrFail("use the other parser\n") // the redirection

	select {
	case <-stopCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("Esc did not cancel the run")
	}
	select {
	case <-secondRun:
	case <-time.After(3 * time.Second):
		mu.Lock()
		qs := append([]string(nil), queries...)
		mu.Unlock()
		t.Fatalf("the redirection never restarted the run, queries=%v", qs)
	}

	writeOrFail("/q\n")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunInteractive never returned")
	}
	_ = pw.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(steered) == 0 || steered[0] != "use the other parser" {
		t.Fatalf("redirection was not delivered to OnSteer: %v", steered)
	}
	if len(queries) < 2 {
		t.Fatalf("expected the run to restart, queries=%v", queries)
	}
	if !strings.Contains(queries[1], "use the other parser") {
		t.Fatalf("second run did not carry the redirection: %q", queries[1])
	}
	if !strings.Contains(queries[1], "first task") {
		t.Fatalf("second run lost the original query: %q", queries[1])
	}
}

func TestREPLPlainTextDuringRunSteers(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var steered []string
	runStarted := make(chan struct{})

	s := scriptedSession(t, "build it\nalso add tests\n/q\n")
	s.OnRun(func(q string) error {
		close(runStarted)
		<-release
		return nil
	})
	s.OnSteer(func(text string) {
		mu.Lock()
		steered = append(steered, text)
		mu.Unlock()
		close(release)
	})
	quit := func(line string) (bool, error) { return strings.HasPrefix(line, "/q"), nil }
	s.OnSlash(quit)
	s.OnLiveSlash(quit)

	done := make(chan error, 1)
	go func() { done <- s.RunInteractive() }()
	<-runStarted

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunInteractive never returned")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(steered) == 0 || steered[0] != "also add tests" {
		t.Fatalf("steering text not delivered: %v", steered)
	}
}

func TestREPLQuitsOnEOF(t *testing.T) {
	s := scriptedSession(t, "")
	s.OnSlash(func(string) (bool, error) { return false, nil })
	done := make(chan error, 1)
	go func() { done <- s.RunInteractive() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("EOF did not end the session")
	}
}

func TestREPLSlashErrorsSurfaceWithoutQuitting(t *testing.T) {
	s := scriptedSession(t, "/bogus\n/q\n")
	var seen []string
	var mu sync.Mutex
	h := func(line string) (bool, error) {
		mu.Lock()
		seen = append(seen, line)
		mu.Unlock()
		if strings.HasPrefix(line, "/q") {
			return true, nil
		}
		return false, io.ErrUnexpectedEOF
	}
	s.OnSlash(h)
	s.OnLiveSlash(h)
	done := make(chan error, 1)
	go func() { done <- s.RunInteractive() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("both commands should have been dispatched: %v", seen)
	}
}

func TestLiveSessionObserveKeepsAgentsAndClearsThem(t *testing.T) {
	s := NewLiveSession()
	s.SetIO(strings.NewReader(""), io.Discard, false)
	s.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "worker", TaskID: "T1", Phase: "execute"})
	if st := s.State(); !st.Running || len(st.Agents) != 1 {
		t.Fatalf("state=%+v", st)
	}
	s.Observe(stream.Event{Kind: stream.KindAgentEnd, Agent: "worker", TaskID: "T1", Phase: "execute"})
	if st := s.State(); len(st.Agents) != 0 {
		t.Fatalf("agents not cleared: %+v", st.Agents)
	}
}

func TestLiveSessionTokenEventsStayOutOfTheTranscript(t *testing.T) {
	s := NewLiveSession()
	s.SetIO(strings.NewReader(""), io.Discard, false)
	s.Observe(stream.Event{Kind: stream.KindToken, Agent: "worker", Data: stream.Token{Delta: "hi", Tokens: 1}})
	if len(s.State().Events) != 0 {
		t.Fatal("token deltas must not be appended as transcript events")
	}
	if s.Activity().LastTokenLine() != "hi" {
		t.Fatalf("token not routed to the activity line: %q", s.Activity().LastTokenLine())
	}
}

func TestShouldRenderRespectsLogLevel(t *testing.T) {
	defer SetLogLevel(LogInfo)

	SetLogLevel(LogError)
	if ShouldRender(stream.Event{Kind: stream.KindPhase, Message: "x"}) {
		t.Fatal("info-level phase events must be hidden at --log-level=error")
	}
	if !ShouldRender(stream.Event{Kind: stream.KindPhase, Level: stream.LevelError}) {
		t.Fatal("errors must always render")
	}

	SetLogLevel(LogInfo)
	if ShouldRender(stream.Event{Kind: stream.KindDebug}) {
		t.Fatal("debug events must be hidden at info")
	}
	SetLogLevel(LogDebug)
	if !ShouldRender(stream.Event{Kind: stream.KindDebug}) {
		t.Fatal("debug events must render at debug")
	}
}

func TestParseLogLevel(t *testing.T) {
	for in, want := range map[string]LogLevel{
		"":      LogInfo,
		"error": LogError,
		"warn":  LogWarn,
		"info":  LogInfo,
		"debug": LogDebug,
	} {
		got, ok := ParseLogLevel(in)
		if !ok || got != want {
			t.Errorf("ParseLogLevel(%q)=%v ok=%v want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseLogLevel("loud"); ok {
		t.Fatal("invalid level must be rejected")
	}
}

func TestInlineSuggestionsAppearWhileTypingASlashCommand(t *testing.T) {
	SetColorMode(ColorNever)
	s := NewLiveSession()
	s.SetIO(strings.NewReader(""), io.Discard, false)
	s.SetSlashRegistry(testRegistry())

	if got := s.suggestions(100); got != nil {
		t.Fatalf("an empty buffer must not suggest: %v", got)
	}
	s.ed.SetValue("/p")
	got := s.suggestions(100)
	if len(got) == 0 {
		t.Fatal("expected suggestions for a partial slash command")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "/permission") || !strings.Contains(joined, "/provider") {
		t.Fatalf("suggestions=%q", joined)
	}

	// Once an argument is being typed the picker gets out of the way.
	s.ed.SetValue("/provider ollama")
	if got := s.suggestions(100); got != nil {
		t.Fatalf("arguments must suppress the picker: %v", got)
	}

	// Plain queries are never slash commands.
	s.ed.SetValue("add jwt auth")
	if got := s.suggestions(100); got != nil {
		t.Fatalf("plain text must not suggest: %v", got)
	}
}

func TestInlineSuggestionsAreClippedToWidth(t *testing.T) {
	SetColorMode(ColorNever)
	s := NewLiveSession()
	s.SetIO(strings.NewReader(""), io.Discard, false)
	s.SetSlashRegistry(testRegistry())
	s.ed.SetValue("/p")
	for _, line := range s.suggestions(40) {
		if VisibleWidth(line) > 40 {
			t.Fatalf("suggestion too wide (%d): %q", VisibleWidth(line), line)
		}
	}
}
