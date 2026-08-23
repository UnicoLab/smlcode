package cli

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

func TestActivityNeverSaysIdleWhileAgentsRun(t *testing.T) {
	SetColorMode(ColorNever)
	a := NewActivity()
	a.Start()
	// An AgentStart with no TaskID used to leave the map empty, so the footer
	// claimed "idle" while the agent was mid-thought.
	a.Observe(stream.Event{Kind: stream.KindAgentStart, Phase: "explore", Agent: "explorer"})
	line := a.Line(120)
	if strings.Contains(line, "idle") {
		t.Fatalf("status line lies about being idle: %q", line)
	}
	if !strings.Contains(line, "@explorer") {
		t.Fatalf("active agent missing: %q", line)
	}
}

func TestActivityIdleWhenNothingRuns(t *testing.T) {
	SetColorMode(ColorNever)
	a := NewActivity()
	if !strings.Contains(a.Line(120), "idle") {
		t.Fatalf("line=%q", a.Line(120))
	}
}

func TestActivityCountsUseEventLevelNotProse(t *testing.T) {
	a := NewActivity()
	a.Start()
	// "no errors found" must NOT count as a failure.
	a.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "reviewer", TaskID: "T1"})
	a.Observe(stream.Event{
		Kind: stream.KindAgentEnd, Agent: "reviewer", TaskID: "T1",
		Message: "review complete: no errors found", Level: stream.LevelSuccess,
	})
	a.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "worker", TaskID: "T2"})
	a.Observe(stream.Event{
		Kind: stream.KindAgentEnd, Agent: "worker", TaskID: "T2",
		Message: "all good", Level: stream.LevelError,
	})
	SetColorMode(ColorNever)
	line := a.Line(120)
	if !strings.Contains(line, "✔1") || !strings.Contains(line, "✖1") {
		t.Fatalf("counters wrong: %q", line)
	}
}

func TestActivityAgentEndClearsTheAgent(t *testing.T) {
	a := NewActivity()
	a.Start()
	a.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "worker", TaskID: "T1"})
	if len(a.ActiveAgents()) != 1 {
		t.Fatalf("active=%v", a.ActiveAgents())
	}
	a.Observe(stream.Event{Kind: stream.KindAgentEnd, Agent: "worker", TaskID: "T1"})
	if len(a.ActiveAgents()) != 0 {
		t.Fatalf("active=%v", a.ActiveAgents())
	}
}

func TestActivityTokenStream(t *testing.T) {
	a := NewActivity()
	a.Start()
	a.Observe(stream.Event{Kind: stream.KindToken, Agent: "worker", Data: stream.Token{Delta: "package ", Tokens: 1}})
	a.Observe(stream.Event{Kind: stream.KindToken, Agent: "worker", Data: stream.Token{Delta: "main\n", Tokens: 2}})
	a.Observe(stream.Event{Kind: stream.KindToken, Agent: "worker", Data: stream.Token{Delta: "func ", Tokens: 3}})
	if got := a.LastTokenLine(); got != "func " {
		t.Fatalf("last line=%q (should reset at each newline)", got)
	}
	SetColorMode(ColorNever)
	if !strings.Contains(a.Line(120), "3 tok") {
		t.Fatalf("token count missing: %q", a.Line(120))
	}
}

func TestActivityUsageParsing(t *testing.T) {
	tokens, cost, ok := parseUsage("tokens=1234 prompt=900 completion=334 cost=$0.0021")
	if !ok || tokens != 1234 || cost < 0.002 || cost > 0.003 {
		t.Fatalf("tokens=%d cost=%f ok=%v", tokens, cost, ok)
	}
	if _, _, ok := parseUsage("nothing useful here"); ok {
		t.Fatal("expected no parse")
	}
}

func TestActivityUsageEventUpdatesLine(t *testing.T) {
	SetColorMode(ColorNever)
	a := NewActivity()
	a.Start()
	a.Observe(stream.Event{Kind: stream.KindUsage, Message: "tokens=2500 cost=$0.0400"})
	line := a.Line(120)
	if !strings.Contains(line, "2.5k tok") {
		t.Fatalf("line=%q", line)
	}
	if !strings.Contains(line, "$0.0400") {
		t.Fatalf("cost missing: %q", line)
	}
}

func TestActivityDoneClearsAgents(t *testing.T) {
	a := NewActivity()
	a.Start()
	a.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "worker", TaskID: "T1"})
	a.Observe(stream.Event{Kind: stream.KindPhase, Phase: "done"})
	if a.Running() || len(a.ActiveAgents()) != 0 {
		t.Fatalf("running=%v active=%v", a.Running(), a.ActiveAgents())
	}
}

func TestActivityLineFitsWidth(t *testing.T) {
	SetColorMode(ColorAlways)
	defer SetColorMode(ColorNever)
	a := NewActivity()
	a.Start()
	for i := 0; i < 8; i++ {
		a.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "verylongagentname", TaskID: string(rune('A' + i))})
	}
	if got := VisibleWidth(a.Line(40)); got > 40 {
		t.Fatalf("line width=%d want <= 40", got)
	}
}

func TestActivityMultipleAgentsSummarize(t *testing.T) {
	SetColorMode(ColorNever)
	a := NewActivity()
	a.Start()
	a.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "worker", TaskID: "T1"})
	a.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "tester", TaskID: "T2"})
	line := a.Line(200)
	if !strings.Contains(line, "+1") {
		t.Fatalf("expected an \"+N more\" summary: %q", line)
	}
}

func TestHumanCount(t *testing.T) {
	for in, want := range map[int]string{5: "5", 1500: "1.5k", 2_400_000: "2.4M"} {
		if got := humanCount(in); got != want {
			t.Errorf("humanCount(%d)=%q want %q", in, got, want)
		}
	}
}

func TestStatusTrackerLevelBasedCounters(t *testing.T) {
	st := NewStatusTracker()
	st.Observe(stream.Event{Kind: stream.KindAgentStart, Phase: "execute", Agent: "worker", TaskID: "T1"})
	st.Observe(stream.Event{
		Kind: stream.KindAgentEnd, Agent: "worker", TaskID: "T1",
		Message: "found no errors", Level: stream.LevelSuccess,
	})
	SetColorMode(ColorNever)
	foot := st.Footer()
	if !strings.Contains(foot, "done=1") || !strings.Contains(foot, "fail=0") {
		t.Fatalf("footer=%q", foot)
	}
}

func TestStatusTrackerShowsAgentWithoutTaskID(t *testing.T) {
	SetColorMode(ColorNever)
	st := NewStatusTracker()
	st.Observe(stream.Event{Kind: stream.KindAgentStart, Phase: "plan", Agent: "planner"})
	if strings.Contains(st.Footer(), "active=idle") {
		t.Fatalf("footer claims idle while the planner runs: %q", st.Footer())
	}
}

func TestEventIconColorsErrorsRed(t *testing.T) {
	SetColorMode(ColorAlways)
	defer SetColorMode(ColorNever)
	// A failing agent_end must not render with the green completion marker.
	icon := eventIcon(stream.KindAgentEnd, stream.LevelError)
	if !strings.Contains(icon, "31") {
		t.Fatalf("agent_end at error level should be red, got %q", icon)
	}
	if strings.Contains(icon, "32") {
		t.Fatalf("agent_end at error level must not be green: %q", icon)
	}
	if ok := eventIcon(stream.KindAgentEnd, stream.LevelSuccess); !strings.Contains(ok, "32") {
		t.Fatalf("successful agent_end should be green, got %q", ok)
	}
}

func TestFormatEventErrorLevelIsRed(t *testing.T) {
	SetColorMode(ColorAlways)
	defer SetColorMode(ColorNever)
	line := FormatEvent(stream.Event{
		Kind: stream.KindAgentEnd, Agent: "worker", Level: stream.LevelError,
		Message: "connection refused",
	})
	if !strings.Contains(line, "\033[31m") {
		t.Fatalf("expected red output: %q", line)
	}
}
