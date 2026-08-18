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
	if !strings.Contains(foot, "done=1") {
		t.Fatalf("expected done=1 in %s", foot)
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
