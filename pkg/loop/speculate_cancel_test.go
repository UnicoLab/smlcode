package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// streamingRaceExec models what a REAL streaming provider does to a racer the
// harness cancels: the request dies mid-stream, so the caller gets back both
// the partial body received so far and the provider's own wrapped
// "context canceled" — not a clean empty error.
type streamingRaceExec struct {
	delay   map[string]time.Duration
	full    map[string]string
	partial map[string]string
}

func (e *streamingRaceExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		d := e.delay[req.AgentID]
		if d == 0 {
			d = 2 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			// The provider flattens the chain with %v, so errors.Is finds
			// nothing — exactly the shape that made a text match tempting.
			err := fmt.Errorf("chat failed: OpenAI streaming failed: "+
				"Post \"http://127.0.0.1:1/v1/chat/completions\": %v", ctx.Err()) //nolint:errorlint // models a provider that drops the chain
			out[i] = ggagent.SubAgentResult{
				AgentID: req.AgentID, TaskID: req.TaskID,
				Output: e.partial[req.AgentID], Error: err,
			}
			return out, err
		case <-time.After(d):
			out[i] = ggagent.SubAgentResult{
				AgentID: req.AgentID, TaskID: req.TaskID, Output: e.full[req.AgentID],
			}
		}
	}
	return out, nil
}

// TestSpeculateSwallowsItsOwnLosersCancellation is the regression net for
// defect 1 at the source: neither the error nor the half-streamed body of a
// loser the race canceled itself may escape as a result.
func TestSpeculateSwallowsItsOwnLosersCancellation(t *testing.T) {
	exec := &streamingRaceExec{
		delay: map[string]time.Duration{
			plan.RoleReviewer:  2 * time.Second,
			roleReviewerStrict: 2 * time.Millisecond,
		},
		full:    map[string]string{roleReviewerStrict: `{"approved":true,"score":92,"summary":"ok"}`},
		partial: map[string]string{plan.RoleReviewer: `{"approved":true,"score":92,"summ`},
	}
	r := NewRunner(exec, ggagent.NewSharedState())
	r.MaxParallel = 4
	r.Timeout = 30 * time.Second
	r.Log = func(string, ...interface{}) {}

	res := r.speculate(context.Background(), []SpecSlot{
		{Role: plan.RoleReviewer, Prompt: "review"},
		{Role: roleReviewerStrict, Prompt: "review strictly"},
	})
	var loser, winner SpecResult
	for _, sr := range res {
		switch sr.Role {
		case plan.RoleReviewer:
			loser = sr
		case roleReviewerStrict:
			winner = sr
		}
	}
	if winner.Skipped || winner.Err != nil || winner.Output == "" {
		t.Fatalf("winner must survive intact: %+v", winner)
	}
	if !loser.Skipped {
		t.Fatalf("a loser this race canceled must be Skipped: %+v", loser)
	}
	if loser.Err != nil {
		t.Fatalf("a self-inflicted cancellation must not be reported as an error: %v", loser.Err)
	}
	if loser.Output != "" {
		t.Fatalf("a half-streamed body is not a result: %q", loser.Output)
	}
}

// TestSpeculateReportsARealInterrupt is the other half: when the CALLER's
// context goes away, the race must still say so, or a genuine Ctrl-C would be
// silently swallowed.
func TestSpeculateReportsARealInterrupt(t *testing.T) {
	exec := &streamingRaceExec{
		delay: map[string]time.Duration{plan.RoleReviewer: 2 * time.Second},
	}
	r := NewRunner(exec, ggagent.NewSharedState())
	r.MaxParallel = 4
	r.Timeout = 30 * time.Second
	r.Log = func(string, ...interface{}) {}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	defer cancel()
	res := r.speculate(ctx, []SpecSlot{{Role: plan.RoleReviewer, Prompt: "review"}})
	if len(res) != 1 {
		t.Fatalf("res=%+v", res)
	}
	if res[0].Err == nil {
		t.Fatalf("a caller-side cancellation must be reported: %+v", res[0])
	}
}

// TestSpeculativeReviewKeepsTheWinnersVerdict is the regression net for
// defect 2. The reviewer's stream is cut short by the winner and comes back as
// a truncated `{"approved":true,"score":92,"summ`. That is not a verdict —
// preferring it over the strict reviewer's COMPLETE approval is how an
// approved:true score:92 payload was rendered and acted on as
// `approved=false score=0`, buying a correction round nobody needed.
func TestSpeculativeReviewKeepsTheWinnersVerdict(t *testing.T) {
	exec := &streamingRaceExec{
		delay: map[string]time.Duration{
			plan.RoleReviewer:  2 * time.Second,
			roleReviewerStrict: 2 * time.Millisecond,
		},
		full:    map[string]string{roleReviewerStrict: `{"approved":true,"score":92,"summary":"acceptance met"}`},
		partial: map[string]string{plan.RoleReviewer: `{"approved":true,"score":92,"summ`},
	}
	r := NewRunner(exec, ggagent.NewSharedState())
	r.MaxParallel = 4 // the shipped default; >=3 also arms the strict slot
	r.Timeout = 30 * time.Second
	r.Log = func(string, ...interface{}) {}

	cur := plan.Task{
		ID: "T1", Title: "Update a", Role: plan.RoleWorker,
		Description: "update the greeting in a.go", Acceptance: "greeting updated",
		Files:  []string{"a.go"},
		Output: `{"status":"done","summary":"x","files_changed":["a.go"]}`,
	}
	review, raw, err := r.speculativeReview(context.Background(), cur, gateState{}, map[string]string{})
	if err != nil {
		t.Fatalf("speculativeReview: %v", err)
	}
	if !review.Approved || review.Score != 92 {
		t.Fatalf("winner's verdict dropped: approved=%v score=%d raw=%q",
			review.Approved, review.Score, raw)
	}
	if strings.Contains(raw, `"summ`) && !strings.Contains(raw, `"summary"`) {
		t.Fatalf("the truncated loser's body was used as the verdict: %q", raw)
	}
}

// TestReviewCancellationIsNotAUserInterrupt is the regression net for defect 1
// at the wave level: RunBoard must not synthesize context.Canceled — the value
// the orchestrator checkpoints as "interrupted at execute", exit 130 — out of a
// sub-agent cancellation while the run's own context is alive.
func TestReviewCancellationIsNotAUserInterrupt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Every reviewer slot dies with a provider-wrapped cancellation while the
	// worker succeeds. Nothing canceled the run.
	exec := &cancelingReviewExec{root: root}
	r := defaultRunner(t, root, exec)
	r.MaxRetries = 0
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Update a", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
		Description: "update the greeting in a.go", Acceptance: "greeting updated",
		Files: []string{"a.go"},
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard reported %v with a live run context — that is the phantom interrupt", err)
	}
}

type cancelingReviewExec struct{ root string }

func (e *cancelingReviewExec) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	var first error
	for i, req := range reqs {
		if strings.Contains(req.AgentID, "review") {
			err := errors.New("chat failed: OpenAI streaming failed: " +
				"Post \"http://127.0.0.1:1/v1/chat/completions\": context canceled")
			out[i] = ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID, Error: err}
			if first == nil {
				first = err
			}
			continue
		}
		out[i] = ggagent.SubAgentResult{
			AgentID: req.AgentID, TaskID: req.TaskID,
			Output: `{"status":"done","summary":"x","files_changed":["a.go"]}`,
		}
	}
	return out, first
}
