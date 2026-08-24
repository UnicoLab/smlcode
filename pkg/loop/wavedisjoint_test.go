package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// Wave admission must not put two tasks that write the same file into one wave:
// they run as concurrent goroutines against ONE working tree, and the focus
// allowlist is the union of the wave, so it does not separate them from each
// other.
func TestAdmitDisjointDefersOverlappingFiles(t *testing.T) {
	task := func(id string, files ...string) plan.Task {
		return plan.Task{ID: id, Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: files}
	}
	cases := []struct {
		name  string
		ready []plan.Task
		maxP  int
		want  []string
	}{
		{
			name:  "disjoint files all fit",
			ready: []plan.Task{task("T1", "a.go"), task("T2", "b.go"), task("T3", "c.go")},
			maxP:  4,
			want:  []string{"T1", "T2", "T3"},
		},
		{
			name:  "identical file sets are split across waves",
			ready: []plan.Task{task("T1", "a.go"), task("T2", "a.go")},
			maxP:  4,
			want:  []string{"T1"},
		},
		{
			name: "overlapping-but-not-identical sets are split",
			ready: []plan.Task{
				task("T1", "a.go", "b.go"),
				task("T2", "b.go", "c.go"),
				task("T3", "d.go"),
			},
			maxP: 4,
			want: []string{"T1", "T3"},
		},
		{
			name:  "path spelling does not hide an overlap",
			ready: []plan.Task{task("T1", "pkg/a.go"), task("T2", "./pkg/a.go"), task("T3", "pkg//b.go")},
			maxP:  4,
			want:  []string{"T1", "T3"},
		},
		{
			name:  "a declared directory collides with a file inside it",
			ready: []plan.Task{task("T1", "src/"), task("T2", "src/main.go"), task("T3", "docs/x.md")},
			maxP:  4,
			want:  []string{"T1", "T3"},
		},
		{
			name:  "tasks with no declared files are never deferred",
			ready: []plan.Task{task("T1"), task("T2"), task("T3", "a.go")},
			maxP:  4,
			want:  []string{"T1", "T2", "T3"},
		},
		{
			name:  "MaxParallel still caps the wave",
			ready: []plan.Task{task("T1", "a.go"), task("T2", "b.go"), task("T3", "c.go")},
			maxP:  2,
			want:  []string{"T1", "T2"},
		},
		{
			name:  "a non-positive MaxParallel means one at a time, never zero",
			ready: []plan.Task{task("T1", "a.go"), task("T2", "b.go")},
			maxP:  0,
			want:  []string{"T1"},
		},
	}

	r := &Runner{Log: func(string, ...interface{}) {}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, task := range r.admitDisjoint(tc.ready, tc.maxP) {
				got = append(got, task.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wave=%v want %v", got, tc.want)
			}
			// Deterministic: the same input must always give the same wave.
			for i := 0; i < 20; i++ {
				var again []string
				for _, task := range r.admitDisjoint(tc.ready, tc.maxP) {
					again = append(again, task.ID)
				}
				if !reflect.DeepEqual(again, got) {
					t.Fatalf("run %d gave %v, first run gave %v — selection must not depend on map order",
						i, again, got)
				}
			}
		})
	}
}

// The deferred task must be deferred, not dropped: the next wave picks it up
// and the board still finishes with every task done.
func TestOverlappingTaskRunsInTheNextWave(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shared.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		if req.AgentID == plan.RoleReviewer || req.AgentID == roleReviewerStrict {
			return `{"approved":true,"score":90,"summary":"ok"}`
		}
		return "Observation: ws_edit edited shared.go (1 replacement(s))\n" +
			`{"status":"done","summary":"done","files_changed":["shared.go"]}`
	}

	r := defaultRunner(t, root, exec)
	r.MaxRetries = 0

	// Record the membership of every wave the runner actually dispatched.
	var mu sync.Mutex
	var waves [][]string
	r.AfterWave = func(_ context.Context, _ *plan.Board, finished []plan.Task) {
		mu.Lock()
		defer mu.Unlock()
		var ids []string
		for _, task := range finished {
			ids = append(ids, task.ID)
		}
		waves = append(waves, ids)
	}

	// T1 and T2 both write shared.go; T3 is independent. With MaxParallel=4 the
	// old scheduler dispatched all three at once.
	board := &plan.Board{QueryID: "q1", Tasks: []plan.Task{
		{ID: "T1", Title: "Edit shared", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update shared.go", Acceptance: "shared.go changed", Files: []string{"shared.go"}},
		{ID: "T2", Title: "Edit shared again", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update shared.go", Acceptance: "shared.go changed", Files: []string{"shared.go"}},
		{ID: "T3", Title: "Edit other", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "update other.go", Acceptance: "other.go changed", Files: []string{"other.go"}},
	}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(waves) < 2 {
		t.Fatalf("waves=%v — T1 and T2 share shared.go, so they cannot be one wave", waves)
	}
	for i, w := range waves {
		if contains(w, "T1") && contains(w, "T2") {
			t.Fatalf("wave %d = %v dispatched both writers of shared.go together", i, w)
		}
	}
	// Deferred, not dropped: every task must have run and reached done.
	for _, id := range []string{"T1", "T2", "T3"} {
		got, ok := board.Get(id)
		if !ok {
			t.Fatalf("%s vanished from the board", id)
		}
		if got.Column != plan.ColDone {
			t.Fatalf("%s column=%s want done — a deferred task must still run", id, got.Column)
		}
	}
}

// A failed task must not release the tasks that depend on it into the next
// wave. This is the scheduler-side proof of the plan-layer fix: the dependent
// is never dispatched, and it ends blocked with the upstream named.
func TestFailedDependencyDoesNotDispatchDependents(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	exec := &scriptedExec{}
	exec.answer = func(req ggagent.SubAgentRequest, _ int) string {
		if req.AgentID == plan.RoleReviewer || req.AgentID == roleReviewerStrict {
			// T1 can never pass review; it will exhaust its ceiling and park.
			return `{"approved":false,"score":5,"summary":"no evidence","issues":["nothing changed"]}`
		}
		return fmt.Sprintf(`{"status":"done","summary":"claimed","files_changed":["%s"]}`, "a.go")
	}

	r := defaultRunner(t, root, exec)
	r.MaxRetries = 0
	r.MaxTaskAttempts = 1

	board := &plan.Board{QueryID: "q1", Tasks: []plan.Task{
		{ID: "T1", Title: "Foundation", Role: plan.RoleWorker, Column: plan.ColBlocked,
			Description: "build a.go", Acceptance: "a.go changed", Files: []string{"a.go"},
			Error: "worker produced no evidence"},
		{ID: "T2", Title: "Built on top", Role: plan.RoleWorker, Column: plan.ColReadyToDev,
			Description: "extend b.go", Acceptance: "b.go changed", Files: []string{"b.go"},
			DependsOn: []string{"T1"}},
	}}
	if err := r.RunBoard(context.Background(), board); err != nil {
		t.Fatalf("RunBoard: %v", err)
	}

	for _, p := range exec.promptsFor(plan.RoleWorker) {
		if strings.Contains(p, "extend b.go") {
			t.Fatal("T2 was dispatched even though its dependency T1 failed")
		}
	}
	got, _ := board.Get("T2")
	if got.Column != plan.ColBlocked {
		t.Fatalf("T2 column=%s want blocked", got.Column)
	}
	if !strings.Contains(got.Error, "T1") {
		t.Fatalf("T2 Error=%q want it to name the failed upstream", got.Error)
	}
}

func contains(ids []string, id string) bool {
	for _, s := range ids {
		if s == id {
			return true
		}
	}
	return false
}
