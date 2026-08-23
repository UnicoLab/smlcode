package cli

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

func TestStatusTrackerObserve(t *testing.T) {
	st := NewStatusTracker()
	st.Observe(stream.Event{Kind: stream.KindAgentStart, Phase: "execute", Agent: "worker", TaskID: "T1", Message: "edit"})
	st.Observe(stream.Event{Kind: stream.KindAgentEnd, Agent: "reviewer", TaskID: "T1", Message: "review approved=true"})
	foot := st.Footer()
	if !strings.Contains(foot, "phase=execute") && !strings.Contains(foot, "execute") {
		t.Fatalf("footer missing phase: %s", foot)
	}
	if !strings.Contains(foot, "steps=1") {
		t.Fatalf("expected steps=1 in %s", foot)
	}
	line := FormatEvent(stream.Event{
		Kind: stream.KindFileChange, Agent: "worker", Message: "edit pkg/x.go", Scope: "pkg/x.go", Output: "snippet",
	})
	if !strings.Contains(line, "pkg/x.go") && !strings.Contains(line, "edit") {
		t.Fatalf("format event weak: %s", line)
	}
}

func TestFormatEventKinds(t *testing.T) {
	for _, kind := range []string{stream.KindPhase, stream.KindAgentStart, stream.KindCoord, stream.KindFileChange} {
		s := FormatEvent(stream.Event{Kind: kind, Phase: "plan", Message: "hello", Agent: "planner"})
		if strings.TrimSpace(s) == "" {
			t.Fatalf("empty format for %s", kind)
		}
	}
}

func TestFormatCompositionEvent(t *testing.T) {
	line := FormatEvent(stream.Event{
		Kind: stream.KindComposition, Phase: "compose", Agent: "composer",
		Message: "dynamic pipeline active",
		Output:  "# Dynamic pipeline\n\n**Summary:** use go-worker\n\n## Phases\n\n- `execute`",
	})
	if !strings.Contains(line, "dynamic pipeline active") || !strings.Contains(line, "use go-worker") {
		t.Fatalf("composition line weak: %s", line)
	}
}

// TestStatusTrackerTaskFieldIsBoardProgress is the regression for the footer's
// worst lie: its counters count agent calls, and labeling them done/fail made
// a run that finished 0 of 1 tasks print "done=14 fail=3".
func TestStatusTrackerTaskFieldIsBoardProgress(t *testing.T) {
	SetColorMode(ColorNever)
	st := NewStatusTracker()
	for i := 0; i < 5; i++ {
		st.Observe(stream.Event{Kind: stream.KindAgentEnd, Agent: "worker"})
	}
	if got := st.Footer(); strings.Contains(got, "tasks=") {
		t.Fatalf("no task source installed, footer must omit tasks=: %q", got)
	}
	st.SetTaskSource(func() (int, int) { return 0, 1 })
	foot := st.Footer()
	if !strings.Contains(foot, "tasks=0/1") {
		t.Errorf("footer missing board progress: %q", foot)
	}
	if !strings.Contains(foot, "steps=5") {
		t.Errorf("footer must keep the agent-call counter, separately labeled: %q", foot)
	}
	if strings.Contains(foot, "done=") || strings.Contains(foot, "fail=") {
		t.Errorf("the misleading labels are back: %q", foot)
	}
}

func TestFilterStderrDropsDependencyChatterAndKeepsTheRest(t *testing.T) {
	for _, tc := range []struct {
		line string
		drop bool
	}{
		{`time="2026-01-01" level=info msg="Executing node"`, true},
		{`INFO[0012] Creating agent with validated configuration`, true},
		{`DEBU[0001] whatever`, true},
		{`time="2026-01-01" level=warning msg="retrying"`, false},
		{`time="2026-01-01" level=error msg="boom"`, false},
		{`✖ run finished with failures`, false},
		{`panic: runtime error`, false},
		{``, false},
	} {
		if got := IsDependencyChatter(tc.line); got != tc.drop {
			t.Errorf("IsDependencyChatter(%q) = %v, want %v", tc.line, got, tc.drop)
		}
	}
}

func TestFormatEventDropsStudioAPIAdvice(t *testing.T) {
	SetColorMode(ColorNever)
	line := FormatEvent(stream.Event{
		Kind: "ask", Agent: "plan-approve",
		Message: "approve plan? 1 tasks — POST /api/plan/approve",
	})
	if strings.Contains(line, "/api/") {
		t.Fatalf("the terminal renderer must not tell a terminal user to POST: %q", line)
	}
	if !strings.Contains(line, "approve plan? 1 tasks") {
		t.Fatalf("the message itself was lost: %q", line)
	}
}
