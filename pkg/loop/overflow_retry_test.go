package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// overflowThenOKExec fails the first worker call with a context-length error, then succeeds.
type overflowThenOKExec struct {
	mu          sync.Mutex
	workerCalls int
	compacted   bool
}

func (f *overflowThenOKExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		switch req.AgentID {
		case plan.RoleReviewer:
			out[i] = ggagent.SubAgentResult{Output: `{"approved":true,"score":90,"summary":"ok"}`}
		default:
			f.workerCalls++
			if f.workerCalls == 1 {
				out[i] = ggagent.SubAgentResult{
					Error: errors.New("this model's maximum context length is 8192 tokens"),
				}
				continue
			}
			out[i] = ggagent.SubAgentResult{
				Output: `{"status":"done","summary":"ok after compact","files_changed":["hello.py"]}`,
			}
		}
	}
	return out, nil
}

func TestOverflowCompactRetriesOnce(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.py"), []byte("print('hi')\n"), 0o644)

	fake := &overflowThenOKExec{}
	r := NewRunner(fake, ggagent.NewSharedState())
	r.Root = root
	r.MaxParallel = 1
	r.Timeout = time.Minute
	r.MaxRetries = 0
	r.PostWorkerSmoke = false
	r.StaticQuality = false
	r.ClaimsGate = false
	r.RequireSmoke = false
	r.WorkerCritique = false
	compactCalls := 0
	r.OnOverflowCompact = func(ctx context.Context) error {
		compactCalls++
		fake.mu.Lock()
		fake.compacted = true
		fake.mu.Unlock()
		return nil
	}
	var logs []string
	r.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Touch hello", Role: plan.RoleWorker,
		Acceptance: "file exists", Files: []string{"hello.py"},
		Column:      plan.ColReadyToDev,
		Description: "noop",
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatal(err)
	}
	if compactCalls != 1 {
		t.Fatalf("OnOverflowCompact calls=%d logs=%v", compactCalls, logs)
	}
	if fake.workerCalls < 2 {
		t.Fatalf("expected retry after overflow, workerCalls=%d", fake.workerCalls)
	}
	t1, _ := board.Get("T1")
	if t1.Column == plan.ColBlocked {
		t.Fatalf("task still blocked after overflow retry: err=%s logs=%v", t1.Error, logs)
	}
}
