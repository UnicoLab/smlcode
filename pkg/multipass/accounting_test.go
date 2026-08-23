package multipass

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/core"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// scriptedAgent returns canned outputs in order and records what it was asked.
type scriptedAgent struct {
	mu      sync.Mutex
	outputs []string
	errs    []error
	inputs  []string
	delay   time.Duration
	cleared int
}

func (a *scriptedAgent) Execute(ctx context.Context, input string) (*agent.AgentExecution, error) {
	a.mu.Lock()
	n := len(a.inputs)
	a.inputs = append(a.inputs, input)
	var out string
	if n < len(a.outputs) {
		out = a.outputs[n]
	}
	var err error
	if n < len(a.errs) {
		err = a.errs[n]
	}
	d := a.delay
	a.mu.Unlock()

	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return &agent.AgentExecution{Output: out}, nil
}

func (a *scriptedAgent) calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.inputs...)
}

func (a *scriptedAgent) GetConfig() *agent.AgentConfig               { return agent.DefaultAgentConfig() }
func (a *scriptedAgent) UpdateConfig(*agent.AgentConfig)             {}
func (a *scriptedAgent) GetGraph() *core.Graph                       { return nil }
func (a *scriptedAgent) IsRunning() bool                             { return false }
func (a *scriptedAgent) GetConversation() []llm.Message              { return nil }
func (a *scriptedAgent) SeedConversation([]llm.Message)              {}
func (a *scriptedAgent) GetExecutionHistory() []agent.AgentExecution { return nil }
func (a *scriptedAgent) ClearHistory()                               {}
func (a *scriptedAgent) Name() string                                { return "scripted" }
func (a *scriptedAgent) ClearConversation() {
	a.mu.Lock()
	a.cleared++
	a.mu.Unlock()
}

func TestOnCallReportsEveryPass(t *testing.T) {
	a := &scriptedAgent{outputs: []string{
		"draft prose, not json",
		"defect: missing acceptance",
		"still prose",
		"defect: still missing",
		`{"summary":"s","steps":["a"]}`,
	}}
	var calls []CallInfo
	var usage Usage
	r := New(2)
	r.OnCall = func(c CallInfo) { calls = append(calls, c) }
	r.OnUsage = func(u Usage) { usage = u }

	out, err := r.Execute(context.Background(), a, "task")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"summary":"s","steps":["a"]}` {
		t.Fatalf("out = %q", out)
	}
	if len(calls) != 5 {
		t.Fatalf("got %d calls, want 5 (1 draft + 2×(critique+refine))", len(calls))
	}
	wantPasses := []string{PassDraft, PassCritique, PassRefine, PassCritique, PassRefine}
	for i, want := range wantPasses {
		if calls[i].Pass != want {
			t.Errorf("call %d pass = %q, want %q", i, calls[i].Pass, want)
		}
		if calls[i].InputChars == 0 {
			t.Errorf("call %d recorded no input size", i)
		}
	}
	if calls[1].Index != 1 || calls[3].Index != 2 {
		t.Errorf("refine indices = %d %d", calls[1].Index, calls[3].Index)
	}
	if usage.Calls != 5 {
		t.Errorf("usage.Calls = %d", usage.Calls)
	}
	if usage.OutputChars == 0 || usage.InputChars == 0 {
		t.Errorf("usage = %+v", usage)
	}
	if usage.EarlyExit {
		t.Error("EarlyExit set on a run that used every pass")
	}
}

func TestOnUsageReportsEarlyExit(t *testing.T) {
	a := &scriptedAgent{outputs: []string{`{"tasks":[]}`}}
	var usage Usage
	r := New(2)
	r.OnUsage = func(u Usage) { usage = u }
	if _, err := r.Execute(context.Background(), a, "task"); err != nil {
		t.Fatal(err)
	}
	if usage.Calls != 1 || !usage.EarlyExit {
		t.Errorf("usage = %+v, want 1 call with EarlyExit", usage)
	}
}

func TestPassTimeoutBoundsASinglePass(t *testing.T) {
	a := &scriptedAgent{outputs: []string{"slow prose"}, delay: 500 * time.Millisecond}
	r := New(2)
	r.PassTimeout = 30 * time.Millisecond
	var usage Usage
	r.OnUsage = func(u Usage) { usage = u }

	start := time.Now()
	_, err := r.Execute(context.Background(), a, "task")
	if err == nil {
		t.Fatal("expected the draft pass to time out")
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Errorf("pass timeout not applied: %v", elapsed)
	}
	if !usage.TimedOut {
		t.Error("usage did not report the timeout")
	}
}

func TestBudgetStopsTheRunAndKeepsBestAnswer(t *testing.T) {
	a := &scriptedAgent{
		outputs: []string{"draft prose", "critique", "refined prose", "critique2", "refined2"},
		delay:   40 * time.Millisecond,
	}
	r := New(2)
	r.Budget = 100 * time.Millisecond
	var usage Usage
	r.OnUsage = func(u Usage) { usage = u }

	out, err := r.Execute(context.Background(), a, "task")
	if err != nil {
		t.Fatalf("budget exhaustion must not be an error: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("best answer so far was discarded")
	}
	if usage.Calls >= 5 {
		t.Errorf("budget did not stop the run: %d calls", usage.Calls)
	}
	if !usage.TimedOut {
		t.Error("usage did not report the budget stop")
	}
}

func TestExecuteRoleReusesTheAgent(t *testing.T) {
	built := 0
	shared := &scriptedAgent{outputs: []string{`{"tasks":[]}`, `{"tasks":[]}`, `{"tasks":[]}`}}
	r := New(1)
	r.Factory = func(role string) (agent.Agent, error) {
		built++
		return shared, nil
	}
	for i := 0; i < 3; i++ {
		if _, err := r.ExecuteRole(context.Background(), "splitter", "task"); err != nil {
			t.Fatal(err)
		}
	}
	if built != 1 {
		t.Errorf("factory called %d times, want 1 — the agent must be reused", built)
	}
	shared.mu.Lock()
	cleared := shared.cleared
	shared.mu.Unlock()
	if cleared < 2 {
		t.Errorf("reused agent's conversation cleared %d times — prior task messages would leak", cleared)
	}
	r.ResetAgents()
	if _, err := r.ExecuteRole(context.Background(), "splitter", "task"); err != nil {
		t.Fatal(err)
	}
	if built != 2 {
		t.Errorf("ResetAgents did not drop the cache: built=%d", built)
	}
}

func TestExecuteRoleWithoutFactory(t *testing.T) {
	r := New(1)
	if _, err := r.ExecuteRole(context.Background(), "planner", "x"); err == nil {
		t.Fatal("expected an error without a factory")
	}
	boom := errors.New("cannot build")
	r.Factory = func(string) (agent.Agent, error) { return nil, boom }
	if _, err := r.ExecuteRole(context.Background(), "planner", "x"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestOnCallSurfacesPassErrors(t *testing.T) {
	boom := errors.New("provider exploded")
	a := &scriptedAgent{
		outputs: []string{"draft prose", ""},
		errs:    []error{nil, boom},
	}
	var seen []CallInfo
	r := New(1)
	r.OnCall = func(c CallInfo) { seen = append(seen, c) }
	out, err := r.Execute(context.Background(), a, "task")
	if err != nil {
		t.Fatalf("a failed critique must keep the draft, got %v", err)
	}
	if out != "draft prose" {
		t.Errorf("out = %q", out)
	}
	if len(seen) != 2 || seen[1].Err == nil {
		t.Fatalf("call errors not reported: %+v", seen)
	}
}

func TestBackwardCompatibleExecuteStillWorks(t *testing.T) {
	// No hooks, no timeouts: identical behavior to the original Runner.
	a := &scriptedAgent{outputs: []string{"prose", "LOOKS_GOOD"}}
	out, err := New(2).Execute(context.Background(), a, "task")
	if err != nil {
		t.Fatal(err)
	}
	if out != "prose" {
		t.Errorf("out = %q", out)
	}
	if n := len(a.calls()); n != 2 {
		t.Errorf("LOOKS_GOOD did not short-circuit: %d calls", n)
	}
}
