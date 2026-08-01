package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

// approveExec always returns approved:true so we can assert smoke override.
type approveExec struct{}

func (approveExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	out := make([]ggagent.SubAgentResult, len(reqs))
	for i, req := range reqs {
		switch req.AgentID {
		case plan.RoleReviewer:
			out[i] = ggagent.SubAgentResult{Output: `{"approved":true,"score":90,"summary":"looks fine"}`}
		case plan.RoleCorrector:
			out[i] = ggagent.SubAgentResult{Output: `{"status":"done","summary":"noop","files_changed":["bad.py"]}`}
		default:
			out[i] = ggagent.SubAgentResult{Output: `{"status":"done"}`}
		}
	}
	return out, nil
}

func TestReviewRejectsDeterministicSmokeFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bad.py")
	if err := os.WriteFile(target, []byte("def broken(\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sr := quality.RunPostWorkerSmoke(context.Background(), dir, plan.Task{
		Role: plan.RoleWorker, Files: []string{"bad.py"},
	}, time.Minute)
	if sr.OK {
		t.Fatal("expected py_compile fail")
	}
	sec := quality.FormatSmokeSection(sr)
	r := &Runner{
		Root: dir, Executor: approveExec{}, Shared: ggagent.NewSharedState(),
		MaxParallel: 1, Timeout: time.Minute, PostWorkerSmoke: true,
		MaxRetries: 0, // reject without corrector loop
	}
	var logs []string
	r.Log = func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	task := plan.Task{
		ID: "T1", Title: "Create bad.py", Role: plan.RoleWorker,
		Acceptance: "file exists", Files: []string{"bad.py"},
		Column: plan.ColInReview,
		Output: `{"status":"done","summary":"wrote bad.py","files_changed":["bad.py"]}` +
			"\n\n## Disk evidence\n- modified: bad.py\n" + sec +
			"\nObservation: exit error: deterministic smoke failed\n",
	}
	baseline := map[string]string{"bad.py": "0:0"}
	board := &plan.Board{Tasks: []plan.Task{task}}
	_ = r.reviewAndCorrect(context.Background(), board, task, baseline)
	got, ok := board.Get("T1")
	if !ok {
		t.Fatal("missing task")
	}
	joined := strings.ToLower(got.Review + "\n" + strings.Join(logs, "\n"))
	if got.Column == plan.ColDone {
		t.Fatalf("smoke-failed worker must not be Done: review=%q", got.Review)
	}
	if !strings.Contains(joined, "smoke") && !strings.Contains(joined, "shell failure") {
		t.Fatalf("expected smoke/shell reject, got col=%s review=%q logs=%v",
			got.Column, got.Review, logs)
	}
}
