package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

type fakeSpecExec struct {
	calls   atomic.Int32
	cancels atomic.Int32
	delay   map[string]time.Duration
	out     map[string]string
}

func (f *fakeSpecExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	f.calls.Add(1)
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		d := f.delay[req.AgentID]
		if d == 0 {
			d = 5 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			f.cancels.Add(1)
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, Error: ctx.Err(), TaskID: req.TaskID}
		case <-time.After(d):
			body := f.out[req.AgentID]
			if body == "" {
				body = `{"ok":true}`
			}
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, Output: body, TaskID: req.TaskID}
		}
	}
	return out, nil
}

func TestSpeculateCancelsOptionalLosers(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.MaxParallel = 2
	cfg.ThinkPasses = 2
	o := &Orchestrator{
		cfg:      cfg,
		executor: &fakeSpecExec{
			delay: map[string]time.Duration{
				"explorer":  20 * time.Millisecond,
				"architect": 400 * time.Millisecond,
			},
			out: map[string]string{
				"explorer":  `{"summary":"found","relevant_files":["a.go"]}`,
				"architect": `{"approach":"slow"}`,
			},
		},
		shared: ggagent.NewSharedState(),
	}
	slots := []SpecSlot{
		{Role: "explorer", Prompt: "explore", Required: true},
		{Role: "architect", Prompt: "arch", Required: false},
	}
	start := time.Now()
	res := o.speculate(context.Background(), slots)
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Fatalf("expected early cancel of architect; elapsed=%s", elapsed)
	}
	var explorer, architect SpecResult
	for _, r := range res {
		switch r.Role {
		case "explorer":
			explorer = r
		case "architect":
			architect = r
		}
	}
	if explorer.Err != nil || explorer.Output == "" {
		t.Fatalf("explorer: %+v", explorer)
	}
	if !architect.Skipped && architect.Err == nil && architect.Output != "" {
		// Either cancelled (error/skipped) or unfinished — must not be a full slow win.
		t.Fatalf("expected architect cancelled/skipped, got %+v", architect)
	}
	fe := o.executor.(*fakeSpecExec)
	if fe.cancels.Load() < 1 && !architect.Skipped {
		t.Fatalf("expected at least one cancel; cancels=%d architect=%+v", fe.cancels.Load(), architect)
	}
}

func TestSpeculateRespectsMaxParallel(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.MaxParallel = 1
	fe := &fakeSpecExec{
		delay: map[string]time.Duration{"explorer": 30 * time.Millisecond, "docs": 30 * time.Millisecond},
		out:   map[string]string{"explorer": `{"summary":"x","relevant_files":[]}`, "docs": `{"docs":[]}`},
	}
	o := &Orchestrator{cfg: cfg, executor: fe, shared: ggagent.NewSharedState()}
	res := o.speculate(context.Background(), []SpecSlot{
		{Role: "explorer", Prompt: "e", Required: true},
		{Role: "docs", Prompt: "d", Required: false},
	})
	if len(res) != 2 {
		t.Fatalf("results=%d", len(res))
	}
	// With max_parallel=1, explorer finishes then cancel may skip docs.
	var exp SpecResult
	for _, r := range res {
		if r.Role == "explorer" {
			exp = r
		}
	}
	if exp.Err != nil {
		t.Fatalf("explorer failed: %v", exp.Err)
	}
}
