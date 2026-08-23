package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// observerFixture is a real workspace + tool registry + evolve engine + inner
// loop Runner, wired together exactly as Orchestrator.New/buildRunner wire
// them, but with no LLM anywhere. That absence is the point: everything this
// test exercises must happen with zero model calls.
type observerFixture struct {
	orch   *Orchestrator
	runner *loop.Runner
	reg    *tools.ToolRegistry
	root   string
}

func newObserverFixture(t *testing.T) *observerFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.Root = dir
	cfg.Evolve = true
	cfg.Normalize()

	eng, err := evolve.OpenWith(cfg.Root, filepath.Join(dir, "userhome"),
		evolve.EngineOptions{Deterministic: true})
	if err != nil {
		t.Fatalf("open evolve engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	reg := tools.NewToolRegistry()
	ws, _, err := workspace.RegisterCodingToolsWithWorkspace(reg, dir, workspace.ToolOpts{
		Permission:      "auto",
		ShellPermission: "deny",
		SlmDir:          cfg.SlmDir(),
	})
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}

	o := &Orchestrator{
		cfg:       cfg,
		store:     contextstore.New(cfg.SlmDir()),
		workspace: ws,
		evolve:    eng,
		onEvent:   func(Event) {},
	}
	o.buildPackers(nil, 32768)

	runner := loop.NewRunner(nil, nil)
	runner.Root = dir
	runner.SlmDir = cfg.SlmDir()
	runner.Evolve = eng
	o.activeRunner = runner
	o.installToolObserver()

	return &observerFixture{orch: o, runner: runner, reg: reg, root: dir}
}

// call invokes a tool exactly the way an agent does — through the registry,
// with JSON arguments — so the whole wrap chain (cap, hooks, observer, loop
// guard) is in play.
func (f *observerFixture) call(t *testing.T, name string, args map[string]interface{}) string {
	t.Helper()
	tool, ok := f.reg.GetTool(name)
	if !ok {
		t.Fatalf("tool %s is not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(context.Background(), string(raw))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	s, _ := out.(string)
	return s
}

// TestLineNumberedEditIsRepairedAndRetriedWithoutAModelCall is the end-to-end
// proof for the tool-layer → self-improvement wiring.
//
// A model that pastes ws_read's output straight into old_str produces
// "   3|\tb := 2", which can never match the file. That is the single most
// common small-model edit failure, there is a seeded repair rule for it
// (TransformStripLineNumbers), and before this wiring existed the rule never
// fired because nothing outside pkg/loop ever called ReportToolFailure.
func TestLineNumberedEditIsRepairedAndRetriedWithoutAModelCall(t *testing.T) {
	f := newObserverFixture(t)
	const original = "package x\n\nfunc A() int {\n\tb := 2\n\treturn b\n}\n"
	target := filepath.Join(f.root, "a.go")
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	// read-before-edit: the file must be read in this session.
	read := f.call(t, "ws_read", map[string]interface{}{"path": "a.go"})
	if !strings.Contains(read, "|") {
		t.Fatalf("ws_read did not emit a line-number gutter: %q", read)
	}

	// The failure mode: old_str copied verbatim out of the ws_read result,
	// gutter and all.
	out := f.call(t, "ws_edit", map[string]interface{}{
		"path":    "a.go",
		"old_str": "     4|\tb := 2",
		"new_str": "\tb := 42",
	})

	// 1. The edit landed, in the SAME tool call, with no model round-trip.
	if !strings.Contains(out, "edited a.go") {
		t.Fatalf("line-numbered old_str was not repaired and retried; result was:\n%s", out)
	}
	got, err := os.ReadFile(target) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "b := 42") {
		t.Fatalf("file was not edited:\n%s", got)
	}

	// 2. The failure was recorded AND resolved from memory, not from an LLM.
	report := evolve.RunReport{
		RunID: "t1", PlannedTasks: 1, CompletedTasks: 1,
		Failures: f.runner.DrainFailureEvents(),
	}
	if len(report.Failures) == 0 {
		t.Fatal("the tool refusal never reached the evolve engine")
	}
	ref := evolve.Reflect(report)
	if ref.ResolvedFromMemory == 0 {
		t.Fatalf("ResolvedFromMemory = 0; failures=%+v", report.Failures)
	}
	if ref.ResolvedFromLLM != 0 {
		t.Fatalf("a repair with no model call was credited to the LLM: %+v", report.Failures)
	}

	// 3. And it shows up in the metrics row `slmcode metrics` reads.
	m := evolve.MetricsFor(report, ref)
	if m.ResolvedFromMemory == 0 {
		t.Fatal("metrics row reports ResolvedFromMemory = 0")
	}
	if m.RepairHits == 0 {
		t.Fatal("metrics row reports no repair-rule hits")
	}
}

// TestSuccessfulToolCallsReachWorkingMemory proves RecordToolEvent is wired:
// the tool counters the run report reads come from working memory, and before
// this observer nothing fed them from the tool layer at all.
func TestSuccessfulToolCallsReachWorkingMemory(t *testing.T) {
	f := newObserverFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, "a.go"),
		[]byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.call(t, "ws_read", map[string]interface{}{"path": "a.go"})
	f.call(t, "ws_list", map[string]interface{}{"path": "."})

	w := f.orch.evolve.Memory().Working()
	calls, _, _ := w.Counters()
	if calls < 2 {
		t.Fatalf("working memory saw %d tool calls, want >= 2", calls)
	}
	snap := w.Snapshot()
	seen := map[string]bool{}
	for _, e := range snap.Events {
		seen[e.Tool] = true
	}
	if !seen["ws_read"] {
		t.Fatalf("ws_read never reached working memory: %+v", snap.Events)
	}
}

// TestObserverIsInertWithoutARunner: the tool layer must keep working when
// there is no inner loop yet (a bare `slmcode` CLI tool call, Studio's
// workspace API) — the observer is an addition, not a dependency.
func TestObserverIsInertWithoutARunner(t *testing.T) {
	f := newObserverFixture(t)
	f.orch.mu.Lock()
	f.orch.activeRunner = nil
	f.orch.mu.Unlock()
	if err := os.WriteFile(filepath.Join(f.root, "a.go"),
		[]byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := f.call(t, "ws_read", map[string]interface{}{"path": "a.go"})
	if !strings.Contains(out, "package x") {
		t.Fatalf("ws_read broke with no runner installed: %q", out)
	}
}
