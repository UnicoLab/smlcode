package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// incompleteFirstExec returns incomplete worker JSON, then a complete corrector fix.
type incompleteFirstExec struct {
	mu             sync.Mutex
	workerCalls    int
	correctorCalls int
}

func (f *incompleteFirstExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		switch req.AgentID {
		case plan.RoleReviewer:
			out[i] = ggagent.SubAgentResult{Output: `{"approved":true,"score":90,"summary":"ok"}`}
		case plan.RoleCorrector:
			f.correctorCalls++
			out[i] = ggagent.SubAgentResult{
				Output: `{"status":"done","summary":"refined hello","files_changed":["hello.py"]}`,
			}
		default:
			f.workerCalls++
			// Incomplete: no status JSON — think_passes should force critique.
			out[i] = ggagent.SubAgentResult{Output: "I started but did not finish the JSON."}
		}
	}
	return out, nil
}

func TestThinkPassesForcesWorkerCritique(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.py"), []byte("print('hi')\n"), 0o644)

	fake := &incompleteFirstExec{}
	r := NewRunner(fake, ggagent.NewSharedState())
	r.Root = root
	r.MaxParallel = 1
	r.Timeout = time.Minute
	r.MaxRetries = 0
	r.PostWorkerSmoke = false
	r.WorkerCritique = false // only think_passes should trigger
	r.ThinkPasses = 2
	r.StaticQuality = false
	r.ClaimsGate = false
	r.RequireSmoke = false
	var logs []string
	r.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Touch hello", Role: plan.RoleWorker,
		Acceptance: "file exists", Files: []string{"hello.py"},
		Column:      plan.ColReadyToDev,
		Description: "noop touch",
	}}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatal(err)
	}
	if fake.correctorCalls < 1 {
		t.Fatalf("expected think_passes critique, correctorCalls=%d logs=%v",
			fake.correctorCalls, logs)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "worker-critique") {
		t.Fatalf("expected critique log, got %v", logs)
	}
}

func TestThinkPassesOneSkipsCritiqueWhenWorkerCritiqueOff(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.py"), []byte("print('hi')\n"), 0o644)

	fake := &incompleteFirstExec{}
	r := NewRunner(fake, ggagent.NewSharedState())
	r.Root = root
	r.MaxParallel = 1
	r.Timeout = time.Minute
	r.MaxRetries = 0
	r.PostWorkerSmoke = false
	r.WorkerCritique = false
	r.ThinkPasses = 1
	r.StaticQuality = false
	r.ClaimsGate = false

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "Touch hello", Role: plan.RoleWorker,
		Acceptance: "file exists", Files: []string{"hello.py"},
		Column: plan.ColReadyToDev,
	}}}
	_ = r.RunBoard(context.Background(), board)
	if fake.correctorCalls != 0 {
		t.Fatalf("think_passes=1 must not force critique, got %d", fake.correctorCalls)
	}
}
