package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

func llmMessage(role string) llm.Message { return llm.Message{Role: role, Content: "x"} }

// inflightExec is a fake model endpoint that measures the one thing the run
// gate exists to bound: how many requests are in flight AT ONCE. It answers
// every role the pipeline drives so a whole run can pass through it, and the
// worker really writes its file so the loop's evidence gates let the task
// finish.
type inflightExec struct {
	root string
	mu   sync.Mutex
	now  int
	max  int
	seen int
}

func (e *inflightExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	_ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.mu.Lock()
	e.now += len(reqs)
	e.seen += len(reqs)
	if e.now > e.max {
		e.max = e.now
	}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.now -= len(reqs)
		e.mu.Unlock()
	}()
	// Long enough that two concurrent requests overlap; short enough that a
	// whole run is still fast.
	select {
	case <-time.After(3 * time.Millisecond):
	case <-ctx.Done():
	}
	out := make([]ggagent.SubAgentResult, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, ggagent.SubAgentResult{AgentID: req.AgentID, TaskID: req.TaskID,
			Output: e.answer(req)})
	}
	return out, nil
}

func (e *inflightExec) stats() (max, seen int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.max, e.seen
}

func (e *inflightExec) answer(req ggagent.SubAgentRequest) string {
	role := strings.ToLower(req.AgentID)
	switch {
	case strings.Contains(role, "explorer"):
		return `{"summary":"tiny go module","relevant_files":["go.mod"],"key_symbols":[],"risks":[],"notes":""}`
	case strings.Contains(role, "planner"):
		return `{"summary":"two files","steps":["Create hello.go with func Hello","Create world.go with func World"],` +
			`"goals":[],"assumptions":[],"risks":[]}`
	case strings.Contains(role, "splitter"):
		return `{"tasks":[` +
			`{"id":"T1","title":"create hello.go","description":"Create hello.go with func Hello() string.",` +
			`"role":"worker","files":["hello.go"],"acceptance":"hello.go exists","depends_on":[]},` +
			`{"id":"T2","title":"create world.go","description":"Create world.go with func World() string.",` +
			`"role":"worker","files":["world.go"],"acceptance":"world.go exists","depends_on":[]}]}`
	case strings.Contains(role, "reviewer"):
		return `{"approved":true,"score":92,"summary":"file present","issues":[]}`
	case strings.Contains(role, "tester"):
		return "Observation: ws_shell `ls` exit status 0\n" +
			`{"passed":true,"commands":["ls"],"summary":"files present","failures":[]}`
	case strings.Contains(role, "architect"):
		return `{"approach":"two files","components":["hello.go"],"interfaces":[],"risks":[],"non_goals":[]}`
	case strings.Contains(role, "worker"), strings.Contains(role, "corrector"),
		strings.Contains(role, "deep"), strings.Contains(role, "editor"):
		name := "hello.go"
		fn := "Hello"
		if strings.EqualFold(req.TaskID, "T2") {
			name, fn = "world.go", "World"
		}
		if e.root != "" {
			_ = os.WriteFile(filepath.Join(e.root, name),
				[]byte("package main\n\nfunc "+fn+"() string { return \"hi\" }\n"), 0o600)
		}
		return "Observation: ws_write wrote " + name + "\n" +
			`{"status":"done","summary":"created ` + name + `","files_changed":["` + name + `"],"notes":""}`
	}
	return "- The project is a tiny Go module.\n"
}

// The gate itself: N slots, never more than N holders, cancellation releases.
func TestRunGateBoundsInFlightHolders(t *testing.T) {
	g := newRunGate(2)
	var mu sync.Mutex
	now, max := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := g.acquire(context.Background(), 1)
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			mu.Lock()
			now++
			if now > max {
				max = now
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			now--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if max > 2 {
		t.Fatalf("%d holders at once through a 2-slot gate", max)
	}

	// A batch wider than the gate is clamped, not deadlocked.
	release, err := g.acquire(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	release()

	// Waiting on a full gate honors cancellation and holds nothing after.
	hold, _ := g.acquire(context.Background(), 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := g.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire on a full gate returned %v, want the ctx deadline", err)
	}
	hold()
	release, err = g.acquire(context.Background(), 2)
	if err != nil {
		t.Fatalf("gate did not recover its slots after a canceled wait: %v", err)
	}
	release()

	// A nil gate admits everything.
	var none *runGate
	if _, err := none.acquire(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
}

// The phase pairs used to spawn one goroutine per phase unconditionally. At
// max_parallel=1 they run one after another, in order.
func TestPhasePairsRunSequentiallyAtMaxParallelOne(t *testing.T) {
	var mu sync.Mutex
	now, max := 0, 0
	var order []string
	phase := func(name string) func() phaseResult {
		return func() phaseResult {
			mu.Lock()
			now++
			if now > max {
				max = now
			}
			order = append(order, name)
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			now--
			mu.Unlock()
			return phaseResult{name: name}
		}
	}

	o := testOrch(t, func(c *config.Config) { c.SetMaxParallel(1) })
	res := o.runPhases(context.Background(), phase("context"), phase("explore"))
	if len(res) != 2 || res["context"].err != nil || res["explore"].err != nil {
		t.Fatalf("results = %+v", res)
	}
	if max != 1 {
		t.Fatalf("%d phases in flight at once with max_parallel=1", max)
	}
	if strings.Join(order, ",") != "context,explore" {
		t.Fatalf("order = %v, want the phases in the order given", order)
	}

	// Canceled before the second phase: reported under a stable key.
	ctx, cancel := context.WithCancel(context.Background())
	res = o.runPhases(ctx, func() phaseResult { cancel(); return phaseResult{name: "first"} }, phase("second"))
	if r, ok := res["canceled-1"]; !ok || r.err == nil {
		t.Fatalf("a phase that never started must be reported as canceled: %+v", res)
	}
}

// The headline: a WHOLE run — context, explore with its speculative digs,
// architect, plan, split, the execute waves with their reviews, the tester,
// memory — never has more requests in flight than max_parallel. Measured at
// the executor, which is where the endpoint would see them.
func TestFullRunNeverExceedsMaxParallel(t *testing.T) {
	for _, par := range []int{1, 2} {
		par := par
		t.Run(map[int]string{1: "max_parallel=1", 2: "max_parallel=2"}[par], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default(root)
			// InitWorkspace applies the Go language pack, which turns the QA
			// gate and the per-task smoke ON; the knobs below are set after
			// it so this run never shells out to a real toolchain.
			if err := InitWorkspace(root, cfg); err != nil {
				t.Fatal(err)
			}
			cfg.StructuredDecoding = "off"
			cfg.DynamicPipeline = false
			cfg.Squads = false
			cfg.ClarifyMode = "off"
			cfg.PlanApprove = "auto"
			cfg.ContinueAsk = "off"
			cfg.EscalateAsk = "off"
			cfg.QAGate = false
			cfg.PostWorkerSmoke = false
			cfg.RequireSmoke = false
			cfg.ScopeJudge = false
			cfg.PlaceholderPass = false
			// think_passes=2 turns ON the paths that used to add slots: the
			// speculative explore digs, the plan critique, the per-wave
			// coordinator and distillation.
			cfg.ThinkPasses = 2
			cfg.MaxRetries = 1
			cfg.TaskTimeout = 30 * time.Second
			cfg.SetMaxParallel(par)
			cfg.Normalize()
			o, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			exec := &inflightExec{root: root}
			o.executor = exec
			// The multipass runner talks to the provider directly; with no
			// provider in this fixture the planner and splitter take the
			// single-shot path, which routes them through exec like every
			// other role. The multipass path holds a gate slot of its own.
			o.think = nil

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			res, err := o.Run(ctx, "Create hello.go and world.go with one function each")
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			max, seen := exec.stats()
			t.Logf("%d requests, max %d in flight, success=%v: %s", seen, max, res.Success, res.Summary)
			if seen < 6 {
				t.Fatalf("only %d requests reached the executor — this did not exercise a full run", seen)
			}
			if max > par {
				t.Fatalf("%d requests in flight at once with max_parallel=%d", max, par)
			}
		})
	}
}

// llm_requests counts round-trips, not results: a worker whose ReAct loop made
// eight model calls is eight requests.
func TestLLMRequestsCountAssistantTurns(t *testing.T) {
	if n := llmRequestsIn(ggagent.SubAgentResult{}); n != 1 {
		t.Fatalf("a result with no transcript counts as %d, want 1", n)
	}
	res := ggagent.SubAgentResult{}
	for _, role := range []string{"system", "user", "assistant", "tool", "assistant", "assistant"} {
		res.Messages = append(res.Messages, llmMessage(role))
	}
	if n := llmRequestsIn(res); n != 3 {
		t.Fatalf("counted %d requests over three assistant turns", n)
	}
}
