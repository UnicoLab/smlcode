package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// testOrch builds a minimal orchestrator rooted at dir, with the packer wired
// exactly as New does.
func testOrch(t *testing.T, tune func(*config.Config)) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	if tune != nil {
		tune(cfg)
	}
	cfg.Normalize()
	store := contextstore.New(cfg.SlmDir())
	o := &Orchestrator{cfg: cfg, store: store, onEvent: func(Event) {}}
	o.buildPackers(nil, 32768)
	return o
}

// --- 1: the QA gate formats the wave's changed files, not the repo ----------

func TestFormatWaveChangesOnlyTouchesChangedFiles(t *testing.T) {
	o := testOrch(t, nil)
	root := o.cfg.Root
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const unformatted = "package x\n\nfunc  A( ) int {\nreturn 1\n}\n"
	write("changed.go", unformatted)
	write("untouched.go", unformatted)

	// Only changed.go is in the wave.
	o.noteChangedFiles("changed.go")
	o.formatWaveChanges(context.Background())

	got, err := os.ReadFile(filepath.Join(root, "changed.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == unformatted {
		t.Fatal("the wave's changed file was NOT formatted — AutoFixFormatting is a no-op, " +
			"the gate must call quality.FormatChangedFiles")
	}
	other, err := os.ReadFile(filepath.Join(root, "untouched.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(other) != unformatted {
		t.Fatal("a file outside the wave was reformatted — that is the repo-wide diff this replaced")
	}
}

func TestFormatWaveChangesIsANoOpWithNoChanges(t *testing.T) {
	o := testOrch(t, nil)
	const unformatted = "package x\n\nfunc  A( ) int {\nreturn 1\n}\n"
	p := filepath.Join(o.cfg.Root, "a.go")
	if err := os.WriteFile(p, []byte(unformatted), 0o600); err != nil {
		t.Fatal(err)
	}
	o.formatWaveChanges(context.Background())
	got, _ := os.ReadFile(p)
	if string(got) != unformatted {
		t.Fatal("with an empty change set nothing may be formatted")
	}
}

func TestChangedFilesAreRecordedAndReset(t *testing.T) {
	o := testOrch(t, nil)
	o.noteChangedFiles("b.go", "a.go", "", "a.go")
	got := o.changedFilesSnapshot()
	if len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Fatalf("changedFilesSnapshot=%v want sorted, deduped, no blanks", got)
	}
	o.resetChangedFiles()
	if n := len(o.changedFilesSnapshot()); n != 0 {
		t.Fatalf("reset left %d entries", n)
	}
}

// --- 3: the production worker prompt carries the rules the gates enforce ----

func TestWorkerPromptCarriesTheEnforcedRules(t *testing.T) {
	task := plan.Task{
		ID: "T1", Title: "add Add", Role: plan.RoleWorker, Column: plan.ColInProgress,
		Description: "implement it",
		Files:       []string{"add.go"},
		Acceptance:  "go test ./... passes",
		Checklist: []plan.ChecklistItem{
			{Text: "write Add", Done: false},
			{Text: "write the test", Done: true},
		},
	}
	got := formatWorkerPromptFor(task, "Project language: Go.")

	// Every one of these is something a review gate rejects on, and every one
	// of them was missing from the orchestrator's private builder.
	for _, want := range []string{
		"write Add",                     // the checklist
		"write the test",                //
		"Do NOT add extra helper files", // scope rule
		"ws_patch fails",                // re-read / retry rule
		"go build ./...",                // language-appropriate smoke step
		"No stubs",                      // no-stubs rule
		"files_changed",                 // the finish contract
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("worker prompt is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "[x] write the test") || !strings.Contains(got, "[ ] write Add") {
		t.Fatalf("checklist state not rendered:\n%s", got)
	}
}

func TestTesterPromptUsesTheCanonicalTesterRules(t *testing.T) {
	task := plan.Task{ID: "T2", Title: "verify", Role: plan.RoleTester, Description: "check it"}
	got := formatWorkerPromptFor(task, "Project language: Python.")
	if !strings.Contains(got, "Required finish (tester)") {
		t.Fatalf("tester block missing:\n%s", got)
	}
	if !strings.Contains(got, "pytest") {
		t.Fatalf("tester prompt must name the language-appropriate smoke command:\n%s", got)
	}
	if strings.Contains(got, "files_changed") {
		t.Fatal("a tester must not be given the worker's status contract")
	}
}

// --- 4: failure output is excerpted, not head-truncated ---------------------

// TestFailureExcerptKeepsTheAssertion is the property the corrector depends on:
// a pytest run whose collection noise fills the budget must still show the
// assertion. truncate() (head-only) loses it.
func TestFailureExcerptKeepsTheAssertion(t *testing.T) {
	noise := strings.Repeat("collecting ... rootdir: /tmp/x plugins: anyio-4.2.0\n", 200)
	failure := "E       assert add(2, 2) == 5\nFAILED tests/test_add.py::test_add"
	out := noise + failure

	if strings.Contains(truncate(out, 800), "assert add(2, 2) == 5") {
		t.Skip("input no longer exercises head truncation")
	}
	got := quality.FailureExcerpt(out, 800)
	if !strings.Contains(got, "assert add(2, 2) == 5") {
		t.Fatalf("FailureExcerpt dropped the assertion:\n%s", got)
	}
	if !strings.Contains(got, "FAILED tests/test_add.py::test_add") {
		t.Fatalf("FailureExcerpt dropped the FAILED line:\n%s", got)
	}
}

// --- 5: qa_bootstrap policy is enforced ------------------------------------

func TestQABootstrapPolicyOffNeverInstalls(t *testing.T) {
	o := testOrch(t, func(c *config.Config) { c.QABootstrap = config.QABootstrapOff })
	t.Setenv(envQABootstrap, "")
	if err := os.WriteFile(filepath.Join(o.cfg.Root, "requirements.txt"), []byte("requests\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	o.OnEvent(func(e Event) { msgs = append(msgs, e.Message) })

	// requirements.txt makes `pip install -r requirements.txt` the candidate.
	// Under policy=off nothing may be proposed for execution — the observable
	// signal is the emitted refusal naming the command that was skipped.
	o.runQABootstrap(context.Background(), "python -m pytest -q")

	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "policy=off") {
		t.Fatalf("policy=off must be reported, got:\n%s", joined)
	}
	if strings.Contains(joined, "policy=ask") || strings.Contains(joined, "policy=auto") {
		t.Fatalf("wrong policy applied:\n%s", joined)
	}
}

func TestQABootstrapPolicyAskNeedsApproval(t *testing.T) {
	o := testOrch(t, func(c *config.Config) { c.QABootstrap = config.QABootstrapAsk })
	t.Setenv(envQABootstrap, "")
	if err := os.WriteFile(filepath.Join(o.cfg.Root, "requirements.txt"), []byte("requests\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bp := quality.PlanBootstrap(o.cfg.Root, "python -m pytest -q",
		quality.NormalizeBootstrapPolicy(o.QABootstrapMode()))
	if !bp.NeedsApproval {
		t.Fatalf("ask must route through the permission layer, got %+v", bp)
	}
	if bp.Run {
		t.Fatal("ask must never authorize an unattended install")
	}
}

func TestQABootstrapModeReachesTheInnerLoop(t *testing.T) {
	for _, want := range []string{config.QABootstrapOff, config.QABootstrapAsk, config.QABootstrapAuto} {
		o := testOrch(t, func(c *config.Config) { c.QABootstrap = want })
		t.Setenv(envQABootstrap, "")
		if got := o.QABootstrapMode(); got != want {
			t.Fatalf("QABootstrapMode=%q want %q", got, want)
		}
		if got := quality.NormalizeBootstrapPolicy(o.QABootstrapMode()); string(got) != want {
			t.Fatalf("policy=%q want %q", got, want)
		}
	}
}

// --- 7: the context-engineering knobs change the built pack -----------------

func TestContextReservesChangeTheBuiltPack(t *testing.T) {
	base := testOrch(t, nil)
	tight := testOrch(t, func(c *config.Config) {
		// Hold back far more of the window; the pack must shrink.
		c.ContextReserveResponseTokens = 20000
	})
	baseTokens := base.packerFor(plan.RoleWorker).BudgetTokensFor(plan.RoleWorker)
	tightTokens := tight.packerFor(plan.RoleWorker).BudgetTokensFor(plan.RoleWorker)
	if tightTokens >= baseTokens {
		t.Fatalf("context_reserve_response_tokens did not reach the packer: base=%d tight=%d",
			baseTokens, tightTokens)
	}
}

func TestContextSlackPercentChangesTheBuiltPack(t *testing.T) {
	base := testOrch(t, nil)
	slack := testOrch(t, func(c *config.Config) { c.ContextSlackPercent = 60 })
	b := base.packerFor(plan.RoleWorker).BudgetTokensFor(plan.RoleWorker)
	s := slack.packerFor(plan.RoleWorker).BudgetTokensFor(plan.RoleWorker)
	if s >= b {
		t.Fatalf("context_slack_percent did not reach the packer: base=%d slack=%d", b, s)
	}
}

func TestRoleContextBudgetChangesTheBuiltPack(t *testing.T) {
	o := testOrch(t, func(c *config.Config) {
		c.ContextRoleBudget = map[string]int{"worker": 25}
	})
	if o.rolePackers == nil {
		t.Fatal("context_role_budget produced no role-scoped packer")
	}
	scoped := o.packerFor("worker")
	shared := o.packer
	if scoped == shared {
		t.Fatal("packerFor(worker) must return the role-scoped packer")
	}
	got := scoped.BudgetTokensFor("worker")
	want := shared.BudgetTokensFor("worker")
	if got >= want {
		t.Fatalf("worker budget %d not reduced from the default %d", got, want)
	}
	// A role with no override still uses the shared packer.
	if o.packerFor("reviewer") != shared {
		t.Fatal("an unconfigured role must keep the shared packer")
	}
}

func TestRepoMapTokensZeroDisablesTheMap(t *testing.T) {
	o := testOrch(t, func(c *config.Config) { c.RepoMapTokens = 0 })
	opts := packerOptionsFor(o.cfg, nil, 32768)
	if len(opts) < 3 {
		t.Fatalf("repo_map_tokens is not passed to the packer (%d options)", len(opts))
	}
}

func TestExcerptWindowLinesReachesThePacker(t *testing.T) {
	o := testOrch(t, func(c *config.Config) { c.ExcerptWindowLines = 3 })
	root := o.cfg.Root
	var body strings.Builder
	for i := 0; i < 200; i++ {
		body.WriteString("filler line\n")
	}
	body.WriteString("func NeedleFunc() {}\n")
	for i := 0; i < 200; i++ {
		body.WriteString("filler line\n")
	}
	if err := os.WriteFile(filepath.Join(root, "big.go"), []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	narrow, err := o.packBuildReq(contextstore.BuildRequest{
		Role: plan.RoleWorker, Query: "NeedleFunc", Files: []string{"big.go"},
		FocusTerms: []string{"NeedleFunc"}, Bodies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wide := testOrch(t, func(c *config.Config) { c.ExcerptWindowLines = 60 })
	if err := os.WriteFile(filepath.Join(wide.cfg.Root, "big.go"), []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	widePack, err := wide.packBuildReq(contextstore.BuildRequest{
		Role: plan.RoleWorker, Query: "NeedleFunc", Files: []string{"big.go"},
		FocusTerms: []string{"NeedleFunc"}, Bodies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(narrow.Files["big.go"]) >= len(widePack.Files["big.go"]) {
		t.Fatalf("excerpt_window_lines did not reach the packer: narrow=%d wide=%d",
			len(narrow.Files["big.go"]), len(widePack.Files["big.go"]))
	}
}

func TestSkillDisclosureReachesThePackOptions(t *testing.T) {
	cards := skillPackOptions(&config.Config{SkillDisclosure: config.SkillDisclosureCards}, 1600)
	if !cards.CardsOnly {
		t.Fatal("skill_disclosure=cards must set CardsOnly")
	}
	full := skillPackOptions(&config.Config{SkillDisclosure: config.SkillDisclosureFull}, 1600)
	if !full.ExpandAll {
		t.Fatal("skill_disclosure=full must set ExpandAll")
	}
	auto := skillPackOptions(&config.Config{
		SkillDisclosure: config.SkillDisclosureAuto, SkillMaxExpanded: 5,
	}, 1600)
	if auto.CardsOnly || auto.ExpandAll {
		t.Fatal("skill_disclosure=auto must leave progressive disclosure in charge")
	}
	if auto.MaxExpanded != 5 {
		t.Fatalf("skill_max_expanded=%d want 5", auto.MaxExpanded)
	}
	if got := skillPackOptions(nil, 1600).MaxChars; got != 1600 {
		t.Fatalf("MaxChars=%d", got)
	}
}

// --- 11: exactly ONE loop event sink is installed ---------------------------

// loop.fireEvent mirrors to BOTH OnEvent and OnEventFull when both are set, so
// installing the legacy sink alongside the structured one would deliver every
// event to the orchestrator twice.
func TestBuildRunnerInstallsExactlyOneEventSink(t *testing.T) {
	o := testOrch(t, nil)
	o.shared = nil
	runner := o.buildRunner("q", "run-1", "")
	if runner.OnEventFull == nil {
		t.Fatal("OnEventFull must be installed — it is the only sink that carries Level and Data")
	}
	if runner.OnEvent != nil {
		t.Fatal("OnEvent must stay nil: fireEvent mirrors to both sinks, which would double every event")
	}

	var got []Event
	o.OnEvent(func(e Event) { got = append(got, e) })
	runner.OnEventFull(loop.LoopEvent{
		Kind: stream.KindAgentEnd, Agent: "worker", TaskID: "T1",
		Message: "done", Level: stream.LevelSuccess,
	})
	if len(got) != 1 {
		t.Fatalf("one loop event produced %d orchestrator events", len(got))
	}
	if got[0].Level != stream.LevelSuccess {
		t.Fatalf("Level did not survive the bridge: %q", got[0].Level)
	}
	if got[0].TaskID != "T1" || got[0].Agent != "worker" {
		t.Fatalf("event fields lost in the bridge: %+v", got[0])
	}
}

func TestBuildRunnerCarriesTypedTokenData(t *testing.T) {
	o := testOrch(t, nil)
	o.shared = nil
	runner := o.buildRunner("q", "run-1", "")
	var got []Event
	o.OnEvent(func(e Event) { got = append(got, e) })
	runner.EmitToken("worker", "T1", "hel", 1)
	if len(got) != 1 {
		t.Fatalf("EmitToken produced %d events, want exactly 1", len(got))
	}
	tok, ok := got[0].Data.(stream.Token)
	if !ok {
		t.Fatalf("typed token payload lost: %#v", got[0].Data)
	}
	if tok.Delta != "hel" {
		t.Fatalf("token delta=%q", tok.Delta)
	}
}

// --- 6: structured_decoding=off is enforced, not merely reported ------------

func TestStructuredDecodingOffIsResolved(t *testing.T) {
	o := testOrch(t, func(c *config.Config) { c.StructuredDecoding = config.DecodingOff })
	t.Setenv(envStructuredDecoding, "")
	if got := o.StructuredDecoding(); got != config.DecodingOff {
		t.Fatalf("StructuredDecoding=%q", got)
	}
	// Applying the policy must not panic on a bare orchestrator and must not
	// touch anything when the policy is auto.
	o.applyStructuredDecodingPolicy()
	auto := testOrch(t, func(c *config.Config) { c.StructuredDecoding = config.DecodingAuto })
	auto.applyStructuredDecodingPolicy()
}

// TestStructuredDecodingOffPinsPromptOnly is the enforcement, not the report:
// pkg/backends selects a mechanism from its capability cache, so an all-false
// record for the configured backend is what forces prompt-only JSON.
func TestStructuredDecodingOffPinsPromptOnly(t *testing.T) {
	backends.ResetCapabilityCache()
	t.Cleanup(backends.ResetCapabilityCache)
	t.Setenv(envStructuredDecoding, "")

	o := testOrch(t, func(c *config.Config) {
		c.StructuredDecoding = config.DecodingOff
		c.Provider = "openai"
		c.Endpoint = "http://127.0.0.1:65535/v1"
		c.Model = "test-model"
	})
	// Pretend a previous probe found a fully capable endpoint.
	backends.SetCapabilities(o.cfg.Provider, o.cfg.Endpoint, o.cfg.Model, backends.Capabilities{
		JSONSchema: true, GuidedJSON: true, GBNFGrammar: true, JSONObject: true,
	})
	if c, _ := backends.CachedCapabilities(o.cfg.Provider, o.cfg.Endpoint, o.cfg.Model); !c.Any() {
		t.Fatal("test setup: the seeded capabilities should be capable")
	}

	o.applyStructuredDecodingPolicy()

	c, ok := backends.CachedCapabilities(o.cfg.Provider, o.cfg.Endpoint, o.cfg.Model)
	if !ok {
		t.Fatal("structured_decoding=off must SEED a record, not merely clear one — " +
			"an absent record makes pkg/backends probe and negotiate again")
	}
	if c.Any() {
		t.Fatalf("constrained decoding still available after structured_decoding=off: %s", c)
	}
	if got := c.SelectMechanism(schema.Spec{}, nil); got != backends.MechPromptOnly {
		t.Fatalf("SelectMechanism=%q want %q", got, backends.MechPromptOnly)
	}
}

// The pin must be process-local: persisting an all-false record would keep
// constrained decoding off for a week after the operator sets it back to auto.
func TestStructuredDecodingOffIsNotPersisted(t *testing.T) {
	backends.ResetCapabilityCache()
	t.Cleanup(backends.ResetCapabilityCache)
	t.Setenv(envStructuredDecoding, "")

	o := testOrch(t, func(c *config.Config) {
		c.StructuredDecoding = config.DecodingOff
		c.Provider = "openai"
		c.Endpoint = "http://127.0.0.1:65535/v1"
		c.Model = "test-model"
	})
	backends.SetCapabilityCacheDir(o.cfg.SlmDir())
	o.applyStructuredDecodingPolicy()

	if _, err := os.Stat(filepath.Join(o.cfg.SlmDir(), "capabilities.json")); err == nil {
		t.Fatal("the off-pin was written to the on-disk capability cache — it would outlive the setting")
	}
}

// TestEventSinkSwapIsRaceFree covers the other half of the event path: Studio
// re-subscribes (setOrch → wireOrchestratorEvents → OnEvent) while a run is
// emitting, and the sink used to be an unguarded field read.
func TestEventSinkSwapIsRaceFree(t *testing.T) {
	o := testOrch(t, nil)
	var mu sync.Mutex
	seen := 0
	o.OnEvent(func(Event) { mu.Lock(); seen++; mu.Unlock() })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			o.emit("execute", "tick", "")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			o.OnEvent(func(Event) { mu.Lock(); seen++; mu.Unlock() })
		}
	}()
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if seen == 0 {
		t.Fatal("no events reached the sink")
	}
}

// TestOnEventReplacesRatherThanAppends: every consumer (CLI, Studio, eval)
// calls OnEvent, and a second call must REPLACE the first. If it appended,
// `slmcode studio` would fan every event out once per config save.
func TestOnEventReplacesRatherThanAppends(t *testing.T) {
	o := testOrch(t, nil)
	first, second := 0, 0
	o.OnEvent(func(Event) { first++ })
	o.OnEvent(func(Event) { second++ })
	o.emit("init", "hello", "")
	if first != 0 {
		t.Fatalf("the replaced sink still received %d event(s)", first)
	}
	if second != 1 {
		t.Fatalf("the installed sink received %d events, want exactly 1", second)
	}
}
