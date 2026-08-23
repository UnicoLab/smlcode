package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

func TestRenderDashboardSmoke(t *testing.T) {
	var buf bytes.Buffer
	board := &plan.Board{Tasks: []plan.Task{
		{ID: "T1", Title: "Edit hello", Column: plan.ColInProgress, Role: "worker"},
		{ID: "T2", Title: "Test", Column: plan.ColReadyToDev, Role: "tester"},
	}}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	RenderDashboard(&buf, DashboardState{
		Root:     "/tmp/demo",
		Provider: "omlx",
		Model:    "qwen2.5-coder:14b",
		Endpoint: "http://127.0.0.1:13131/v1",
		Backend:  "slmcode",
		Phase:    "execute",
		Running:  true,
		Board:    board,
		Agents:   []string{"@worker:T1"},
		Events: []stream.Event{
			{Kind: stream.KindAgentStart, Agent: "worker", TaskID: "T1", Message: "edit", Phase: "execute"},
			{Kind: stream.KindFileChange, Message: "edit hello.go", Output: "+ // hi"},
		},
		ErrorsHead: "",
		Queries:    []string{"run-1"},
	})
	out := buf.String()
	for _, want := range []string{"SLMCODE", "omlx", "T1", "worker", "Live", "/q"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard missing %q in:\n%s", want, out)
		}
	}
}

func TestLiveSessionObserve(t *testing.T) {
	s := NewLiveSession()
	s.SetIO(strings.NewReader(""), io.Discard, false)
	s.SetState(DashboardState{Provider: "ollama", Model: "m"})
	s.Observe(stream.Event{Kind: stream.KindAgentStart, Agent: "worker", TaskID: "T3", Phase: "execute"})
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Running || len(s.state.Agents) != 1 {
		t.Fatalf("%+v", s.state)
	}
}
