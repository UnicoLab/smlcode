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
