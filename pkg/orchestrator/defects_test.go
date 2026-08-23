package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// --- P5: QA gate must not report GREEN when tests actually fail -------------

func TestQALooksLikeNoTests(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "pure no-test repo",
			output: "?   \tgithub.com/x/pkg/a\t[no test files]\n?   \tgithub.com/x/pkg/b\t[no test files]\n",
			want:   true,
		},
		{
			// The regression: a mixed repo prints [no test files] AND FAIL.
			name: "mixed repo with a real failure",
			output: "?   \tgithub.com/x/pkg/a\t[no test files]\n" +
				"--- FAIL: TestThing (0.00s)\nFAIL\tgithub.com/x/pkg/b\t0.01s\n",
			want: false,
		},
		{
			name:   "tab-question marker only, no no-test phrase",
			output: "?   \tgithub.com/x/pkg/a\t0.01s\n",
			want:   false,
		},
		{
			name:   "build failure",
			output: "?   \tgithub.com/x/pkg/a\t[no test files]\n# github.com/x/pkg/b\nundefined: Foo\n",
			want:   false,
		},
		{
			name:   "cannot find package",
			output: "no test files\ncannot find package \"x\"\n",
			want:   false,
		},
		{
			name:   "pytest collected nothing",
			output: "collected 0 items\n",
			want:   true,
		},
		{
			name:   "pytest failure",
			output: "collected 0 items\nE   Error: boom\n",
			want:   false,
		},
		{
			name:   "empty",
			output: "",
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := qaLooksLikeNoTests(tc.output); got != tc.want {
				t.Fatalf("qaLooksLikeNoTests()=%v want %v", got, tc.want)
			}
		})
	}
}

// --- P6: the QA repair loop must be reachable under the shipped default -----

func TestQAGateRoundsFloor(t *testing.T) {
	cases := []struct {
		name       string
		configured int
		want       int
	}{
		{"shipped default of 1 is floored", 1, DefaultQAGateRounds},
		{"zero is floored", 0, DefaultQAGateRounds},
		{"a larger explicit value wins", 7, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &Orchestrator{cfg: &config.Config{QAGateMaxRounds: tc.configured}}
			if got := o.qaGateRounds(); got != tc.want {
				t.Fatalf("qaGateRounds()=%d want %d", got, tc.want)
			}
			if got := o.qaGateRounds(); got < 2 {
				t.Fatal("the diagnose/fix pass is unreachable with fewer than 2 rounds")
			}
		})
	}
}

// --- P8: the escalate HITL timeout must not starve a human -----------------

func TestEscalateAskTimeoutFloor(t *testing.T) {
	cases := []struct {
		name       string
		configured time.Duration
		want       time.Duration
	}{
		{"the shipped 30s default is raised", config.DefaultEscalateAskTimeout, DefaultEscalateAskTimeout},
		{"zero is raised", 0, DefaultEscalateAskTimeout},
		{"a longer explicit value is honored", 20 * time.Minute, 20 * time.Minute},
		{"a deliberately short explicit value is honored", 80 * time.Millisecond, 80 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &Orchestrator{cfg: &config.Config{EscalateAskTimeout: tc.configured}}
			if got := o.escalateAskTimeout(); got != tc.want {
				t.Fatalf("escalateAskTimeout()=%s want %s", got, tc.want)
			}
		})
	}
}

// --- P10: a listed phase with no explicit "enabled" is ENABLED --------------

// TestParseDefaultsOmittedEnabled pins the repair at the parse boundary where
// it now lives.
//
// The orchestrator used to re-scan the composer's RAW text with a regex and
// flip any phase that had no "enabled" key (applyOmittedEnabledDefault). That
// workaround is deleted: composer.PhaseChoice.UnmarshalJSON defaults a missing
// key to true, so composer.Parse — the production path — already returns
// Enabled=true. This test asserts the property against Parse itself, which is
// what actually protects planning and splitting from a silent kill.
func TestParseDefaultsOmittedEnabled(t *testing.T) {
	raw := `{"summary":"s","phases":[{"id":"plan"},{"id":"split"},` +
		`{"id":"execute","enabled":true},{"id":"explore","enabled":false}]}`
	comp, err := composer.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := map[string]bool{}
	for _, p := range comp.Phases {
		got[p.ID] = p.Enabled
	}
	want := map[string]bool{
		"plan": true, "split": true, "execute": true,
		// An EXPLICIT false is still respected — the default applies to
		// presence, not to value.
		"explore": false,
	}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("phase %s enabled=%v want %v (all: %v)", id, got[id], w, got)
		}
	}
}

func TestEnsureCriticalCompositionProtectsPlanAndSplit(t *testing.T) {
	comp := &composer.Composition{Phases: []composer.PhaseChoice{
		{ID: "plan", Enabled: false, When: pipeline.WhenNever},
		{ID: "execute", Enabled: true},
	}}
	reenabled := ensureCriticalComposition(comp, "", "")
	got := map[string]composer.PhaseChoice{}
	for _, p := range comp.Phases {
		got[p.ID] = p
	}
	for _, id := range []string{"plan", "split", "execute", "test"} {
		p, ok := got[id]
		if !ok {
			t.Fatalf("critical phase %s missing from composition", id)
		}
		if !p.Enabled || p.When == pipeline.WhenNever {
			t.Fatalf("critical phase %s left disabled: %+v", id, p)
		}
	}
	if len(reenabled) == 0 {
		t.Fatal("re-enabling critical phases must be reported")
	}
}

func TestEnsureCriticalPhasesCoversPlanAndSplit(t *testing.T) {
	cfg := pipeline.Default()
	cfg.Normalize()
	for _, id := range []string{"plan", "split", "execute", "test"} {
		ps := cfg.Phases[id]
		off := false
		ps.Enabled = &off
		ps.When = pipeline.WhenNever
		cfg.Phases[id] = ps
	}
	reenabled := ensureCriticalPhases(&cfg)
	if len(reenabled) != 4 {
		t.Fatalf("reenabled=%v want all four critical phases", reenabled)
	}
	for _, id := range []string{"plan", "split", "execute", "test"} {
		if !cfg.Phases[id].EnabledOrDefault() {
			t.Fatalf("phase %s still disabled", id)
		}
	}
}

// --- P13: KindAgentEnd must always carry a decisive Level ------------------

func TestAgentEndEventsCarryLevel(t *testing.T) {
	var got []Event
	o := &Orchestrator{cfg: &config.Config{}}
	o.OnEvent(func(e Event) { got = append(got, e) })

	cases := []struct {
		name  string
		emit  func()
		level string
	}{
		{"slot error", func() {
			o.emitFullL("plan", stream.KindAgentEnd, "a", "s1", "slot error: boom", "", "", stream.LevelError)
		}, stream.LevelError},
		{"slot ok", func() {
			o.emitFullL("plan", stream.KindAgentEnd, "a", "s1", "slot finished", "", "", stream.LevelSuccess)
		}, stream.LevelSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got = nil
			tc.emit()
			if len(got) != 1 {
				t.Fatalf("events=%d", len(got))
			}
			if got[0].Level != tc.level {
				t.Fatalf("level=%q want %q", got[0].Level, tc.level)
			}
		})
	}
}

// --- P12: the engine must never print to stdout ----------------------------

func TestVerboseDoesNotPrint(t *testing.T) {
	o := &Orchestrator{cfg: &config.Config{Verbose: true}}
	var seen []Event
	o.OnEvent(func(e Event) { seen = append(seen, e) })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	o.emit("execute", "hello from the engine", "")
	if cerr := w.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	os.Stdout = old

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	if n > 0 {
		t.Fatalf("engine wrote to stdout: %q — the CLI is the sole renderer", string(buf[:n]))
	}
	if len(seen) != 1 || seen[0].Level == "" {
		t.Fatalf("expected one leveled event, got %+v", seen)
	}
}

// --- P14: plan approval must not fail open with a listener attached --------

func TestPlanApproveOnTimeoutPolicy(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"default is auto", "", PlanTimeoutAuto},
		{"explicit approve", PlanTimeoutApprove, PlanTimeoutApprove},
		{"explicit reject", PlanTimeoutReject, PlanTimeoutReject},
		{"garbage falls back to auto", "banana", PlanTimeoutAuto},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envPlanApproveTimeout, tc.env)
			o := &Orchestrator{cfg: &config.Config{}}
			if got := o.planApproveOnTimeout(); got != tc.want {
				t.Fatalf("planApproveOnTimeout()=%q want %q", got, tc.want)
			}
		})
	}
}

func TestSubscribedTracksRegistration(t *testing.T) {
	o := &Orchestrator{cfg: &config.Config{}}
	if o.Subscribed() {
		t.Fatal("a fresh orchestrator has no listener")
	}
	o.OnEvent(func(Event) {})
	if !o.Subscribed() {
		t.Fatal("OnEvent must mark the orchestrator subscribed")
	}
	o2 := &Orchestrator{cfg: &config.Config{}}
	o2.OnPlanApprove(func(context.Context, plan.PlanApproveAsk) (plan.PlanApproveAnswer, error) {
		return plan.PlanApproveAnswer{}, nil
	})
	if !o2.Subscribed() {
		t.Fatal("OnPlanApprove must mark the orchestrator subscribed")
	}
}

// --- P15: runPhaseParallel must honor ctx ---------------------------------

func TestRunPhaseParallelCancellation(t *testing.T) {
	t.Run("canceled before start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ran := false
		res := runPhaseParallel(ctx, func() phaseResult {
			ran = true
			return phaseResult{name: "a"}
		})
		if ran {
			t.Fatal("a phase must not start on an already-canceled context")
		}
		if err := canceledPhase(res); err == nil {
			t.Fatal("cancellation must surface in the result set")
		}
	})

	t.Run("canceled during the run", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		res := runPhaseParallel(ctx, func() phaseResult {
			cancel()
			return phaseResult{name: "a"}
		})
		if err := canceledPhase(res); err == nil {
			t.Fatal("a phase that finished after cancellation must report it")
		}
	})

	t.Run("clean run keeps every result", func(t *testing.T) {
		res := runPhaseParallel(context.Background(),
			func() phaseResult { return phaseResult{name: "a", output: "1"} },
			func() phaseResult { return phaseResult{name: "b", output: "2"} },
		)
		if len(res) != 2 || res["a"].output != "1" || res["b"].output != "2" {
			t.Fatalf("res=%+v", res)
		}
		if err := canceledPhase(res); err != nil {
			t.Fatalf("unexpected cancellation: %v", err)
		}
	})
}

// --- P16: language detection must be cached --------------------------------

func TestDetectProjectLangCached(t *testing.T) {
	root := t.TempDir()
	ResetLangCache(root)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := detectProjectLang(root); got != "Go" {
		t.Fatalf("got %q", got)
	}
	// Remove the marker: a cached answer must survive.
	if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if got := detectProjectLang(root); got != "Go" {
		t.Fatalf("cache miss: got %q", got)
	}
	ResetLangCache(root)
	if got := detectProjectLang(root); got == "Go" {
		t.Fatal("ResetLangCache must drop the memoised answer")
	}
}

// --- item 14: the re-tasked agents must select the right schema contract ---

func TestSchemaContractForRetaskedRoles(t *testing.T) {
	tasksMD := "| ID | Title |\n|--|--|\n| T1 | do it |\n\n" +
		`### T1 output: {"status":"done","summary":"ok","files_changed":["a.go"]}`
	cases := []struct {
		name   string
		prompt string
		hint   string
		want   string
	}{
		{
			// scope.go re-tasks the PLANNER agent with the clarify interview.
			name:   "clarify interview on the planner agent",
			prompt: agents.PromptClarifier + "\n\n## Query\nbuild a cli\n\nReturn STRICT JSON interview object.",
			hint:   plan.RolePlanner,
			want:   schema.RoleClarify,
		},
		{
			// scope.go re-tasks the REVIEWER agent with the scope judge.
			name: "scope judge on the reviewer agent",
			prompt: agents.PromptScopeJudge + "\n\n## Query\nbuild a cli\n\n## Tasks\n" +
				sanitizeForContract(tasksMD),
			hint: plan.RoleReviewer,
			want: schema.RoleScopeJudge,
		},
		{
			// Without sanitizing, the worker JSON that Board.ToMarkdown embeds
			// under "#### Output" hijacks the contract selection.
			name:   "unsanitised board output hijacks the contract",
			prompt: agents.PromptScopeJudge + "\n\n## Query\nbuild a cli\n\n## Tasks\n" + tasksMD,
			hint:   plan.RoleReviewer,
			want:   "",
		},
		{
			name:   "an ordinary review stays review",
			prompt: `Return STRICT JSON: {"approved":true|false,"score":0,"issues":[],"summary":""}`,
			hint:   plan.RoleReviewer,
			want:   schema.RoleReview,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, ok := schema.DetectRole(tc.prompt, tc.hint)
			if !ok {
				t.Fatal("no contract selected")
			}
			if tc.want == "" {
				if spec.Name == schema.RoleScopeJudge {
					t.Fatal("expected the unsanitised prompt to be hijacked — " +
						"if this now passes, sanitizeForContract may no longer be needed")
				}
				return
			}
			if spec.Name != tc.want {
				t.Fatalf("contract=%q want %q", spec.Name, tc.want)
			}
		})
	}
}

// --- plan/orchestrator constant must not drift -----------------------------

func TestSmokeSectionHeaderMatchesQuality(t *testing.T) {
	if plan.SmokeSectionHeader != quality.SmokeSectionHeader {
		t.Fatalf("plan.SmokeSectionHeader=%q quality.SmokeSectionHeader=%q — the tester "+
			"evidence gate keys on this string", plan.SmokeSectionHeader, quality.SmokeSectionHeader)
	}
}

// --- P3 call site: a task with unresolvable targets must BLOCK -------------

func TestBlockUnscopedTask(t *testing.T) {
	tk := plan.Task{ID: "T1", Title: "edit the parser", Role: plan.RoleWorker, Column: plan.ColReadyToDev}
	blockUnscopedTask(&tk)
	if tk.Column != plan.ColToScope {
		t.Fatalf("column=%q want %q", tk.Column, plan.ColToScope)
	}
	if !strings.Contains(tk.Notes, unscopedTaskNote) {
		t.Fatalf("notes=%q", tk.Notes)
	}
	if strings.TrimSpace(tk.Error) == "" {
		t.Fatal("an unscoped task must carry an error so boardHasEscalated sees it")
	}
	if !boardHasEscalated(&plan.Board{Tasks: []plan.Task{tk}}) {
		t.Fatal("an unscoped task must block run success")
	}
}

// --- item 3: task packs must be built from the task, not the query alone ---

func TestBuildTaskInputUsesTaskFields(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	if err := os.MkdirAll(slm, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "parser.go")
	body := "package main\n\n// UnrelatedHelper is noise.\nfunc UnrelatedHelper() {}\n\n" +
		"// ParseHeader is the thing the task is about.\nfunc ParseHeader(s string) string { return s }\n"
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store := contextstore.New(slm)
	if err := store.Init("demo"); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{
		cfg:    config.Default(root),
		store:  store,
		packer: contextstore.NewPackerWithBudget(store, root, 8192),
	}
	build := o.buildTaskInput("do the work")
	out := build(plan.Task{
		ID: "T1", Title: "fix ParseHeader", Role: plan.RoleWorker,
		Description: "ParseHeader drops the trailing newline",
		Acceptance:  "ParseHeader keeps the newline",
		Files:       []string{"parser.go"},
	})
	if !strings.Contains(out, "T1") || !strings.Contains(out, "fix ParseHeader") {
		t.Fatalf("task identity missing from prompt:\n%s", truncate(out, 400))
	}
	if !strings.Contains(out, "parser.go") {
		t.Fatalf("focus file missing from prompt:\n%s", truncate(out, 400))
	}
}

func TestFocusTermsForUsesTaskText(t *testing.T) {
	terms := focusTermsFor(plan.Task{
		Title:      "fix ParseHeader in parser.go",
		Acceptance: "ParseHeader keeps the newline",
		Files:      []string{"parser.go"},
	})
	if len(terms) == 0 {
		t.Fatal("expected focus terms from the task text")
	}
	joined := strings.ToLower(strings.Join(terms, " "))
	if !strings.Contains(joined, "parse") {
		t.Fatalf("terms=%v want the task's identifiers", terms)
	}
}

// --- item 17: the architect/editor pair is opt-in and scoped --------------

func TestArchitectEditorApplies(t *testing.T) {
	cases := []struct {
		name string
		task plan.Task
		want bool
	}{
		{"worker with files", plan.Task{Role: plan.RoleWorker, Files: []string{"a.go"}}, true},
		{"corrector with files", plan.Task{Role: plan.RoleCorrector, Files: []string{"a.go"}}, true},
		{"worker without files", plan.Task{Role: plan.RoleWorker}, false},
		{"tester", plan.Task{Role: plan.RoleTester, Files: []string{"a.go"}}, false},
		{"explorer", plan.Task{Role: plan.RoleExplorer, Files: []string{"a.go"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := architectEditorApplies(tc.task); got != tc.want {
				t.Fatalf("architectEditorApplies()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestArchitectEditorOffByDefault(t *testing.T) {
	t.Setenv(envArchitectEditor, "")
	o := &Orchestrator{cfg: &config.Config{}}
	board := &plan.Board{Tasks: []plan.Task{{ID: "T1", Role: plan.RoleWorker, Files: []string{"a.go"}}}}
	if n := o.applyArchitectEditorRoles(board); n != 0 {
		t.Fatalf("retagged %d tasks — the pair must default OFF", n)
	}
	if board.Tasks[0].Role != plan.RoleWorker {
		t.Fatalf("role=%q", board.Tasks[0].Role)
	}
}

// --- item 19-23: evolve wiring is nil-safe ---------------------------------

func TestEvolveWiringIsNilSafe(t *testing.T) {
	o := &Orchestrator{cfg: config.Default(t.TempDir())}
	o.OnEvent(func(Event) {})
	// Nothing below has an engine; none of it may panic.
	o.startEvolveRun("run-1", "q")
	o.applyRoleModelPolicy()
	o.recordGate("qa_gate", true, "")
	o.runRegressionChecks(context.Background(), "pre-gate")
	o.finishEvolveRun(context.Background(), &Result{ID: "run-1"}, &plan.Board{}, nil)
	o.closeEvolve()
	if got := o.choose("explore_phase", "on", "off"); got != "on" {
		t.Fatalf("choose without an engine must return the first arm, got %q", got)
	}
	if err := o.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMemoryBlockEmptyWithoutEngine(t *testing.T) {
	o := &Orchestrator{cfg: &config.Config{}}
	if got := o.memoryBlockFor(plan.RoleWorker); got != "" {
		t.Fatalf("memoryBlockFor()=%q want empty (no heading around nothing)", got)
	}
}

// --- item 6: project instructions must reach the pack prefix ---------------

func TestSkillPackForCarriesProjectInstructions(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	if err := os.MkdirAll(slm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"),
		[]byte("# Rules\n\nNever run bare go; use ./gg.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := contextstore.New(slm)
	o := &Orchestrator{
		cfg:    config.Default(root),
		store:  store,
		packer: contextstore.NewPackerWithBudget(store, root, 8192),
	}
	if instr := o.refreshProjectInstructions(nil); instr == "" {
		t.Fatal("AGENTS.md was not loaded")
	}
	pack := o.skillPackFor(plan.RoleWorker, "do the work")
	if !strings.Contains(pack, "./gg") {
		t.Fatalf("project instructions missing from the pack prefix:\n%s", pack)
	}
	if !strings.HasPrefix(strings.TrimSpace(pack), "## Project instructions") {
		t.Fatalf("instructions must lead the STABLE PREFIX, got:\n%s", truncate(pack, 200))
	}
}

// --- loop contract: every field this layer owns is set ---------------------

func TestApplyLoopContract(t *testing.T) {
	called := ""
	r := loop.NewRunner(nil, nil)
	applyLoopContract(r, loopContract{
		ContextLimitTokens: 32768,
		MaxTaskCalls:       6,
		MemoryTokens:       300,
		ResolveRole:        func(s string) string { return s + "-mapped" },
		OnTaskStart:        func(id string) { called = id },
	})
	if r.ContextLimitTokens != 32768 {
		t.Fatalf("ContextLimitTokens=%d", r.ContextLimitTokens)
	}
	if r.MaxTaskCalls != 6 {
		t.Fatalf("MaxTaskCalls=%d", r.MaxTaskCalls)
	}
	if r.MemoryTokens != 300 {
		t.Fatalf("MemoryTokens=%d", r.MemoryTokens)
	}
	if r.ResolveRole == nil || r.ResolveRole("go-worker") != "go-worker-mapped" {
		t.Fatal("ResolveRole not wired")
	}
	if r.OnTaskStart == nil {
		t.Fatal("OnTaskStart not wired")
	}
	r.OnTaskStart("T1")
	if called != "T1" {
		t.Fatalf("OnTaskStart callback not invoked, called=%q", called)
	}
	if r.Evolve != nil {
		t.Fatal("a nil engine must stay nil")
	}
}

func TestLoopContractNilSafe(t *testing.T) {
	applyLoopContract(nil, loopContract{}) // must not panic
	f, d := drainRunnerEvolve(nil)
	if f != nil || d != nil {
		t.Fatal("drain must be zero-valued for a nil runner")
	}
}

func TestBuildRunnerAppliesContract(t *testing.T) {
	root := t.TempDir()
	slm := filepath.Join(root, ".slmcode")
	if err := os.MkdirAll(slm, 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(root)
	cfg.ModelProfiles = map[string]config.ModelProfile{
		"default": {ContextLimit: 32768},
	}
	store := contextstore.New(slm)
	o := &Orchestrator{
		cfg:        cfg,
		store:      store,
		boardStore: plan.NewLiveStore(slm),
		packer:     contextstore.NewPackerWithBudget(store, root, 32768),
	}
	o.OnEvent(func(Event) {})
	r := o.buildRunner("q", "run-1", "")
	if r.ContextLimitTokens <= 0 {
		t.Fatal("the runner must learn the model's real context window")
	}
	if r.MaxTaskCalls != DefaultMaxTaskCalls {
		t.Fatalf("MaxTaskCalls=%d want %d", r.MaxTaskCalls, DefaultMaxTaskCalls)
	}
	if r.MemoryTokens != DefaultMemoryTokens {
		t.Fatalf("MemoryTokens=%d want %d", r.MemoryTokens, DefaultMemoryTokens)
	}
	if r.ResolveRole == nil || r.OnTaskStart == nil {
		t.Fatal("ResolveRole / OnTaskStart must be wired")
	}
	r.OnTaskStart("T1") // nil tracker + nil engine must not panic
	if o.lastRunner() != r {
		t.Fatal("buildRunner must record the active runner for the run report")
	}
}

// --- item 18: telemetry renders without panicking on empty counters -------

func TestTelemetrySummaryIsSafeWhenEmpty(t *testing.T) {
	o := &Orchestrator{cfg: &config.Config{}}
	o.OnEvent(func(Event) {})
	o.emitTelemetrySummary() // must not panic with no store and no counters
	if h := telemetryHeadline(); h == "" {
		t.Fatal("headline must never be empty")
	}
}
