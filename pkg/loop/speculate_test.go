package loop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

type fakeReviewExec struct {
	cancels atomic.Int32
	delay   map[string]time.Duration
	out     map[string]string
}

func (f *fakeReviewExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
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
				body = `{"approved":true,"score":90,"summary":"ok"}`
			}
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, Output: body, TaskID: req.TaskID}
		}
	}
	return out, nil
}

func TestSpeculateReviewCancelsOnAcceptanceWin(t *testing.T) {
	fe := &fakeReviewExec{
		delay: map[string]time.Duration{
			plan.RoleReviewer: 400 * time.Millisecond,
		},
		out: map[string]string{
			plan.RoleReviewer: `{"approved":false,"score":10,"summary":"slow reject"}`,
		},
	}
	r := &Runner{
		Executor:    fe,
		Shared:      ggagent.NewSharedState(),
		MaxParallel: 2,
		Timeout:     time.Minute,
		Log:         func(string, ...interface{}) {},
	}
	ready := make(chan struct{})
	close(ready)
	slots := []SpecSlot{
		{
			Role: "acceptance", Required: true,
			Local: func(ctx context.Context) (string, error) {
				return `{"approved":true,"score":85,"summary":"auto-approved: acceptance race won"}`, nil
			},
		},
		{Role: plan.RoleReviewer, Prompt: "review", Required: false},
	}
	start := time.Now()
	res := r.speculate(context.Background(), slots)
	if time.Since(start) > 350*time.Millisecond {
		t.Fatalf("expected early cancel; elapsed=%s", time.Since(start))
	}
	var acc, rev SpecResult
	for _, sr := range res {
		switch sr.Role {
		case "acceptance":
			acc = sr
		case plan.RoleReviewer:
			rev = sr
		}
	}
	if acc.Err != nil || acc.Output == "" {
		t.Fatalf("acceptance: %+v", acc)
	}
	if !rev.Skipped && rev.Err == nil && rev.Output != "" {
		t.Fatalf("expected reviewer canceled/skipped, got %+v", rev)
	}
	if fe.cancels.Load() < 1 && !rev.Skipped {
		t.Fatalf("expected cancel; cancels=%d rev=%+v", fe.cancels.Load(), rev)
	}
}

func TestSpeculateTesterDuplicateCancelsLoser(t *testing.T) {
	fe := &fakeReviewExec{
		delay: map[string]time.Duration{
			plan.RoleTester: 20 * time.Millisecond,
			"tester-strict": 400 * time.Millisecond,
		},
		out: map[string]string{
			plan.RoleTester: "Observation: ws_shell `go test ./... -short`\nok\nexit status 0\n" +
				`{"passed":true,"commands":["go test ./... -short"],"summary":"lean win","failures":[]}`,
			"tester-strict": `{"passed":false,"summary":"slow","failures":["x"]}`,
		},
	}
	r := &Runner{Executor: fe, Shared: ggagent.NewSharedState(), MaxParallel: 2, Timeout: time.Minute, Log: func(string, ...interface{}) {}}
	start := time.Now()
	res := r.speculate(context.Background(), []SpecSlot{
		{Role: plan.RoleTester, Prompt: "lean", Required: false},
		{Role: "tester-strict", Prompt: "strict", Required: false},
	})
	if time.Since(start) > 350*time.Millisecond {
		t.Fatalf("expected cancel of slow tester; elapsed=%s", time.Since(start))
	}
	var lean, strict SpecResult
	for _, sr := range res {
		switch sr.Role {
		case plan.RoleTester:
			lean = sr
		case "tester-strict":
			strict = sr
		}
	}
	if lean.Err != nil || !plan.ParseTesterJSON(lean.Output).Passed {
		t.Fatalf("lean: %+v", lean)
	}
	if !strict.Skipped && strict.Err == nil && strict.Output != "" {
		t.Fatalf("expected strict canceled, got %+v", strict)
	}
}
