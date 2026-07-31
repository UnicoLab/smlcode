package multipass

import (
	"context"
	"strings"
	"testing"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/core"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

type stubAgent struct {
	calls  int
	config *agent.AgentConfig
}

func (s *stubAgent) Execute(_ context.Context, input string) (*agent.AgentExecution, error) {
	s.calls++
	out := "draft answer"
	if strings.Contains(input, "CRITIQUE") {
		if s.calls >= 3 {
			out = "LOOKS_GOOD"
		} else {
			out = "missing acceptance check"
		}
	}
	if strings.Contains(input, "REFINE") {
		out = "refined answer"
	}
	return &agent.AgentExecution{Output: out}, nil
}
func (s *stubAgent) GetConfig() *agent.AgentConfig {
	if s.config == nil {
		s.config = agent.DefaultAgentConfig()
	}
	return s.config
}
func (s *stubAgent) UpdateConfig(config *agent.AgentConfig) { s.config = config }
func (s *stubAgent) GetGraph() *core.Graph                 { return nil }
func (s *stubAgent) IsRunning() bool                       { return false }
func (s *stubAgent) GetConversation() []llm.Message        { return nil }
func (s *stubAgent) ClearConversation()                    {}
func (s *stubAgent) GetExecutionHistory() []agent.AgentExecution {
	return nil
}
func (s *stubAgent) ClearHistory() {}
func (s *stubAgent) Name() string  { return "stub" }

func TestRunnerCritiqueRefine(t *testing.T) {
	r := New(2)
	a := &stubAgent{}
	out, err := r.Execute(context.Background(), a, "do the thing")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("empty output")
	}
	if a.calls < 2 {
		t.Fatalf("expected multipass calls, got %d", a.calls)
	}
}

func TestNewDefaultPasses(t *testing.T) {
	r := New(0)
	if r.Passes != 2 {
		t.Fatalf("passes=%d", r.Passes)
	}
}

type jsonDraftAgent struct {
	calls    int
	critiques int
}

func (s *jsonDraftAgent) Execute(_ context.Context, input string) (*agent.AgentExecution, error) {
	s.calls++
	if strings.Contains(input, "CRITIQUE") || strings.Contains(input, "REFINE") {
		s.critiques++
		return &agent.AgentExecution{Output: "LOOKS_GOOD"}, nil
	}
	return &agent.AgentExecution{Output: `{"summary":"ok","goals":["g"],"steps":["s1"]}`}, nil
}
func (s *jsonDraftAgent) GetConfig() *agent.AgentConfig {
	return agent.DefaultAgentConfig()
}
func (s *jsonDraftAgent) UpdateConfig(*agent.AgentConfig)            {}
func (s *jsonDraftAgent) GetGraph() *core.Graph                      { return nil }
func (s *jsonDraftAgent) IsRunning() bool                            { return false }
func (s *jsonDraftAgent) GetConversation() []llm.Message             { return nil }
func (s *jsonDraftAgent) ClearConversation()                         {}
func (s *jsonDraftAgent) GetExecutionHistory() []agent.AgentExecution { return nil }
func (s *jsonDraftAgent) ClearHistory()                               {}
func (s *jsonDraftAgent) Name() string                                { return "json-stub" }

func TestEarlyExitSkipsCritiqueOnCompleteJSON(t *testing.T) {
	r := New(2)
	a := &jsonDraftAgent{}
	out, err := r.Execute(context.Background(), a, "plan this")
	if err != nil {
		t.Fatal(err)
	}
	if a.calls != 1 || a.critiques != 0 {
		t.Fatalf("expected single draft, no critique; calls=%d critiques=%d", a.calls, a.critiques)
	}
	if !strings.Contains(out, `"steps"`) {
		t.Fatalf("output: %s", out)
	}
}

func TestLooksCompleteJSON(t *testing.T) {
	if !LooksCompleteJSON(`{"tasks":[{"id":"T1"}]}`) {
		t.Fatal("tasks")
	}
	if !LooksCompleteJSON(`here\n{"status":"done","summary":"x"}`) {
		t.Fatal("status done")
	}
	if LooksCompleteJSON(`not json`) {
		t.Fatal("plain text")
	}
	if LooksCompleteJSON(`{"foo":1}`) {
		t.Fatal("unrelated object")
	}
}
