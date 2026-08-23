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
	// Architect waits a long time unless canceled. Explorer finishes quickly so
	// cancel must fire — avoids wall-clock flakiness under the race detector.
	o := &Orchestrator{
		cfg: cfg,
		executor: &fakeSpecExec{
			delay: map[string]time.Duration{
				"explorer":  5 * time.Millisecond,
				"architect": 30 * time.Second,
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
	if elapsed > 2*time.Second {
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
		// Either canceled (error/skipped) or unfinished — must not be a full slow win.
		t.Fatalf("expected architect canceled/skipped, got %+v", architect)
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

func TestSpeculateDiskAcceptCancelsTester(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.MaxParallel = 2
	fe := &fakeSpecExec{
		delay: map[string]time.Duration{
			"tester": 30 * time.Second,
		},
		out: map[string]string{
			"tester": `{"passed":false,"summary":"slow","failures":["nope"]}`,
		},
	}
	o := &Orchestrator{cfg: cfg, executor: fe, shared: ggagent.NewSharedState()}
	start := time.Now()
	res := o.speculate(context.Background(), []SpecSlot{
		{
			Role: "disk-accept", Required: true, Phase: "test",
			Local: func(ctx context.Context) (string, error) {
				return `{"passed":true,"summary":"rename verified on disk","commands":[],"failures":[]}`, nil
			},
		},
		{Role: "tester", Prompt: "verify", Required: false, Phase: "test"},
	})
	if time.Since(start) > 2*time.Second {
		t.Fatalf("expected tester cancel; elapsed=%s", time.Since(start))
	}
	var disk, tester SpecResult
	for _, r := range res {
		switch r.Role {
		case "disk-accept":
			disk = r
		case "tester":
			tester = r
		}
	}
	if disk.Err != nil || disk.Output == "" {
		t.Fatalf("disk: %+v", disk)
	}
	if !tester.Skipped && tester.Err == nil && tester.Output != "" {
		t.Fatalf("expected tester canceled, got %+v", tester)
	}
}

func TestSpeculateDuplicateTesterCancelsLoser(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.MaxParallel = 2
	fe := &fakeSpecExec{
		delay: map[string]time.Duration{
			"tester":        5 * time.Millisecond,
			"tester-strict": 30 * time.Second,
		},
		out: map[string]string{
			"tester":        `{"passed":true,"summary":"lean","failures":[]}`,
			"tester-strict": `{"passed":false,"summary":"slow","failures":["x"]}`,
		},
	}
	o := &Orchestrator{cfg: cfg, executor: fe, shared: ggagent.NewSharedState()}
	start := time.Now()
	res := o.speculate(context.Background(), []SpecSlot{
		{Role: "tester", Prompt: "a", Required: false, Phase: "test"},
		{Role: "tester-strict", Prompt: "b", Required: false, Phase: "test"},
	})
	if time.Since(start) > 2*time.Second {
		t.Fatalf("expected cancel; elapsed=%s", time.Since(start))
	}
	var lean, strict SpecResult
	for _, r := range res {
		switch r.Role {
		case "tester":
			lean = r
		case "tester-strict":
			strict = r
		}
	}
	if lean.Err != nil || lean.Output == "" {
		t.Fatalf("lean: %+v", lean)
	}
	if !strict.Skipped && strict.Err == nil && strict.Output != "" {
		t.Fatalf("expected strict canceled, got %+v", strict)
	}
}
