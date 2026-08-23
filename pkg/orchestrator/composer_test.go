package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

func TestClearDynamicRunArtifacts(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{pipeline.DynamicFileName, composer.DynamicFileName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clearDynamicRunArtifacts(dir)
	for _, name := range []string{pipeline.DynamicFileName, composer.DynamicFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed: %v", name, err)
		}
	}
}

func TestCompositionMarkdown(t *testing.T) {
	c := composer.Composition{
		Summary: "assemble a Go worker",
		Handoff: []string{"Touch only main.go", "Verify with go test ./..."},
		Phases:  []composer.PhaseChoice{{ID: "execute", Agent: "worker", Enabled: true}},
		Execute: composer.ExecuteChoice{DefaultRole: "worker", Reviewer: "reviewer", Corrector: "corrector"},
		Team:    []composer.TeamMember{{Role: "worker", Skills: []string{"atomic-coding"}}},
	}
	md := compositionMarkdown(c)
	for _, want := range []string{"assemble a Go worker", "Touch only main.go", "execute", "worker", "atomic-coding", "reviewer", "corrector"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestCompositionBriefIncludesHandoffTeamAndLoop(t *testing.T) {
	c := composer.Composition{
		Summary: "small Go change",
		Handoff: []string{"Use pkg/calc/calc.go only", "Verify with go test ./..."},
		Execute: composer.ExecuteChoice{DefaultRole: "go-worker", Reviewer: "reviewer", Corrector: "corrector"},
		Team:    []composer.TeamMember{{Role: "go-worker", Skills: []string{"atomic-coding"}}},
	}
	brief := compositionBrief(c)
	for _, want := range []string{"Run collaboration contract", "pkg/calc/calc.go", "go-worker", "atomic-coding", "reviewer"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}

func TestEnsureCriticalCompositionRepairsUIAndRuntimeContract(t *testing.T) {
	comp := &composer.Composition{
		Summary: "bad omission",
		Phases:  []composer.PhaseChoice{{ID: "plan", Agent: "planner", Enabled: true}},
	}
	got := ensureCriticalComposition(comp, "go-worker", "go-tester")
	joined := strings.Join(got, ",")
	for _, want := range []string{"execute", "test"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in %v", want, got)
		}
	}
	agentsByPhase := map[string]string{}
	for _, p := range comp.Phases {
		agentsByPhase[p.ID] = p.Agent
	}
	if agentsByPhase["execute"] != "go-worker" || agentsByPhase["test"] != "go-tester" {
		t.Fatalf("phase agents=%+v", agentsByPhase)
	}
	if comp.Execute.DefaultRole != "go-worker" || comp.Execute.Reviewer != "reviewer" || comp.Execute.Corrector != "corrector" || comp.Execute.MaxWaves == 0 {
		t.Fatalf("execute loop=%+v", comp.Execute)
	}
}

func TestOrderCompositionPhasesCanonicalizesUIOrder(t *testing.T) {
	comp := &composer.Composition{
		Phases: []composer.PhaseChoice{
			{ID: "test", Agent: "go-tester", Enabled: true},
			{ID: "plan", Agent: "planner", Enabled: true},
			{ID: "execute", Agent: "go-worker", Enabled: true},
		},
	}
	orderCompositionPhases(comp)
	got := []string{comp.Phases[0].ID, comp.Phases[1].ID, comp.Phases[2].ID}
	if strings.Join(got, ",") != "plan,execute,test" {
		t.Fatalf("order=%v", got)
	}
}

func TestEnsureCompositionHandoffAndTeam(t *testing.T) {
	comp := &composer.Composition{
		Summary: "fix bug",
		Phases: []composer.PhaseChoice{
			{ID: "plan", Agent: "planner", Enabled: true},
			{ID: "execute", Agent: "go-worker", Enabled: true},
			{ID: "test", Agent: "go-tester", Enabled: true},
		},
		Execute: composer.ExecuteChoice{DefaultRole: "go-worker", Reviewer: "reviewer", Corrector: "corrector"},
	}
	skills := map[string]bool{
		"atomic-coding":        true,
		"multipass-quality":    true,
		"specialist-planner":   true,
		"specialist-reviewer":  true,
		"specialist-corrector": true,
	}
	ensureCompositionHandoff(comp, "fix the bug", []string{"go.mod", "pkg/x.go"}, "Go", "go-worker", "go-tester")
	ensureCompositionTeam(comp, skills)
	if len(comp.Handoff) == 0 || !strings.Contains(strings.Join(comp.Handoff, "\n"), "go test") {
		t.Fatalf("handoff=%v", comp.Handoff)
	}
	byRole := map[string]composer.TeamMember{}
	for _, member := range comp.Team {
		byRole[member.Role] = member
	}
	for _, want := range []string{"planner", "go-worker", "go-tester", "reviewer", "corrector"} {
		if byRole[want].Role == "" {
			t.Fatalf("missing team member %s in %+v", want, comp.Team)
		}
	}
	if got := strings.Join(byRole["go-worker"].Skills, ","); !strings.Contains(got, "atomic-coding") {
		t.Fatalf("worker skills=%v", byRole["go-worker"].Skills)
	}
	if got := strings.Join(byRole["go-tester"].Skills, ","); !strings.Contains(got, "multipass-quality") {
		t.Fatalf("tester skills=%v", byRole["go-tester"].Skills)
	}
}

func TestEnsureCompositionHandoffEnrichesWeakComposerOutput(t *testing.T) {
	comp := &composer.Composition{
		Summary: "weak local composition",
		Handoff: []string{
			"do the task",
			"do the task",
			strings.Repeat("x", 260),
		},
	}
	ensureCompositionHandoff(
		comp,
		"fix composer.go handoff",
		[]string{"pkg/orchestrator/composer.go", "pkg/server/server.go", "go.mod"},
		"Go",
		"go-worker",
		"go-tester",
	)
	joined := strings.Join(comp.Handoff, "\n")
	for _, want := range []string{
		"do the task",
		"Likely target files: pkg/orchestrator/composer.go",
		"Use only authoritative workspace paths",
		"Use go-worker for implementation and go-tester for verification",
		"Verify with go test ./... -count=1",
		"Keep changes scoped to this user request",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("handoff missing %q:\n%v", want, comp.Handoff)
		}
	}
	if strings.Count(joined, "do the task") != 1 {
		t.Fatalf("handoff should deduplicate existing bullets: %v", comp.Handoff)
	}
	for _, item := range comp.Handoff {
		if len(item) > 223 {
			t.Fatalf("handoff item was not bounded: %q", item)
		}
	}
}

func TestEnsureCompositionHandoffCapsButKeepsOperationalContract(t *testing.T) {
	comp := &composer.Composition{
		Handoff: []string{
			"custom note 1",
			"custom note 2",
			"custom note 3",
			"custom note 4",
			"custom note 5",
			"custom note 6",
			"custom note 7",
			"custom note 8",
		},
	}
	ensureCompositionHandoff(
		comp,
		"update README.md",
		[]string{"README.md", "go.mod"},
		"Go",
		"go-worker",
		"go-tester",
	)
	if len(comp.Handoff) != 8 {
		t.Fatalf("handoff length=%d items=%v", len(comp.Handoff), comp.Handoff)
	}
	joined := strings.Join(comp.Handoff, "\n")
	for _, want := range []string{"Likely target files: README.md", "Verify with go test ./... -count=1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("priority handoff item missing %q:\n%v", want, comp.Handoff)
		}
	}
}

func TestSanitizeCompositionDropsUnknownAgents(t *testing.T) {
	f := agents.NewFactory(nil, nil, "m", "p")
	o := &Orchestrator{factory: f}

	comp := &composer.Composition{
		Phases: []composer.PhaseChoice{
			{ID: "execute", Agent: "worker", Enabled: true},
			{ID: "test", Agent: "bogus-agent", Enabled: true},
			{ID: "invented-phase", Agent: "worker", Enabled: true},
		},
		Execute: composer.ExecuteChoice{DefaultRole: "worker", Reviewer: "nope", Corrector: "corrector"},
		Team: []composer.TeamMember{
			{Role: "worker", Skills: []string{"atomic-coding"}},
			{Role: "ghost", Skills: []string{"atomic-coding"}},
		},
		Slots: []pipeline.Slot{{ID: "slot-x", Agent: "also-bogus", After: "execute"}},
	}

	dropped := o.sanitizeComposition(comp)
	joined := strings.Join(dropped, ",")
	for _, want := range []string{"bogus-agent", "nope", "ghost", "also-bogus", "phase:invented-phase"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in dropped %v", want, dropped)
		}
	}

	if comp.Phases[1].Agent != "" {
		t.Fatalf("unknown phase agent should be cleared: %q", comp.Phases[1].Agent)
	}
	if comp.Execute.Reviewer != "" {
		t.Fatalf("unknown reviewer should be cleared: %q", comp.Execute.Reviewer)
	}
	if comp.Phases[0].Agent != "worker" || comp.Execute.Corrector != "corrector" {
		t.Fatalf("known agents must survive: %+v", comp)
	}
	if len(comp.Team) != 1 || comp.Team[0].Role != "worker" {
		t.Fatalf("unknown team member must be dropped: %+v", comp.Team)
	}
	if len(comp.Phases) != 2 {
		t.Fatalf("unknown phase must be dropped: %+v", comp.Phases)
	}
	if len(comp.Slots) != 0 {
		t.Fatalf("unknown-agent slot must be dropped: %+v", comp.Slots)
	}
}

func TestSanitizeCompositionDropsUnsafeSlotsBeforeApply(t *testing.T) {
	f := agents.NewFactory(nil, nil, "m", "p")
	o := &Orchestrator{factory: f}

	comp := &composer.Composition{
		Phases: []composer.PhaseChoice{
			{ID: "execute", Agent: "worker", Enabled: true},
			{ID: "test", Agent: "tester", Enabled: true},
		},
		Execute: composer.ExecuteChoice{DefaultRole: "worker", Reviewer: "reviewer", Corrector: "corrector"},
		Slots: []pipeline.Slot{
			{ID: " Keep ", Agent: " TESTER ", After: " EXECUTE "},
			{ID: "", Agent: "tester", After: "execute"},
			{ID: "bad-anchor", Agent: "tester", After: "made-up"},
			{ID: "no-anchor", Agent: "tester"},
			{ID: "bad-agent", Agent: "ghost", After: "execute"},
			{ID: "keep", Agent: "tester", Before: "test"},
		},
	}

	dropped := o.sanitizeComposition(comp)
	if len(comp.Slots) != 1 {
		t.Fatalf("expected only safe slot to survive, got %+v; dropped=%v", comp.Slots, dropped)
	}
	if comp.Slots[0].ID != "keep" || comp.Slots[0].Agent != "tester" || comp.Slots[0].After != "execute" {
		t.Fatalf("safe slot not normalized/preserved: %+v", comp.Slots[0])
	}
	joined := strings.Join(dropped, ",")
	for _, want := range []string{"slot:<empty>", "slot:bad-anchor", "slot:no-anchor", "ghost", "slot:bad-agent", "slot:keep"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in dropped %v", want, dropped)
		}
	}
	if _, err := composer.Apply(*comp); err != nil {
		t.Fatalf("sanitized composition should apply without slot validation fallback: %v", err)
	}
}

func TestEnsureCriticalPhases(t *testing.T) {
	cfg := pipeline.Default()
	// Disable execute + test the way an SLM composer omission would.
	enabled := false
	for _, id := range []string{"execute", "test"} {
		ps := cfg.Phases[id]
		ps.Enabled = &enabled
		ps.When = pipeline.WhenNever
		cfg.Phases[id] = ps
	}

	got := ensureCriticalPhases(&cfg)
	joined := strings.Join(got, ",")
	for _, want := range []string{"execute", "test"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in reenabled %v", want, got)
		}
	}
	if !cfg.PhaseEnabled("execute") || !cfg.PhaseEnabled("test") {
		t.Fatalf("critical phases still disabled: %+v", cfg.Phases)
	}
	if cfg.Phases["execute"].Agent != "worker" || cfg.Phases["test"].Agent != "tester" {
		t.Fatalf("critical phases lost default agents: %+v", cfg.Phases)
	}
}

func TestEnsureCriticalPhasesNoopWhenEnabled(t *testing.T) {
	cfg := pipeline.Default()
	if got := ensureCriticalPhases(&cfg); len(got) != 0 {
		t.Fatalf("expected no-op on default pipeline, got %v", got)
	}
}

func TestHeuristicCompositionSelectsBroadProductionPipeline(t *testing.T) {
	comp := heuristicComposition(
		"improve composer.go in the SLM harness production pipeline and agents",
		[]string{"pkg/orchestrator/composer.go", "pkg/agents/prompts.go", "README.md"},
		"Go",
		`{"summary":"broad repo change"}`,
		"",
	)
	phases := map[string]composer.PhaseChoice{}
	for _, p := range comp.Phases {
		phases[p.ID] = p
	}
	for _, want := range []string{"context", "explore", "architect", "plan", "split", "coord", "execute", "test", "learn", "polish", "memory"} {
		if !phases[want].Enabled {
			t.Fatalf("expected phase %s in %+v", want, comp.Phases)
		}
	}
	if phases["execute"].Agent != "go-worker" || phases["test"].Agent != "go-tester" {
		t.Fatalf("language specialists not selected: execute=%q test=%q", phases["execute"].Agent, phases["test"].Agent)
	}
	if !strings.Contains(strings.Join(comp.Handoff, "\n"), "pkg/orchestrator/composer.go") {
		t.Fatalf("handoff missing likely target file: %+v", comp.Handoff)
	}
}

func TestHeuristicCompositionKeepsTargetedEditLean(t *testing.T) {
	comp := heuristicComposition(
		"fix composer.go parse bug",
		[]string{"pkg/orchestrator/composer.go", "pkg/server/server.go"},
		"Go",
		`{"summary":"targeted"}`,
		"",
	)
	phases := map[string]bool{}
	for _, p := range comp.Phases {
		phases[p.ID] = p.Enabled
	}
	for _, want := range []string{"context", "plan", "split", "execute", "test", "memory"} {
		if !phases[want] {
			t.Fatalf("expected phase %s in %+v", want, comp.Phases)
		}
	}
	if phases["coord"] || phases["architect"] {
		t.Fatalf("targeted edit should stay lean: %+v", comp.Phases)
	}
}

func TestFilterKnownSpecialistPairDropsUnavailableHints(t *testing.T) {
	known := map[string]bool{"worker": true, "tester": true, "go-worker": true}
	w, tst := filterKnownSpecialistPair("rust-worker", "rust-tester", known)
	if w != "" || tst != "" {
		t.Fatalf("unknown hints must be cleared, got (%q,%q)", w, tst)
	}
	w, tst = filterKnownSpecialistPair("go-worker", "go-tester", known)
	if w != "go-worker" || tst != "" {
		t.Fatalf("known worker should survive and unknown tester clear, got (%q,%q)", w, tst)
	}
}

func TestPreviewCompositionDoesNotPersistOrMutatePipeline(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(root)
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := o.Pipeline().Execute.DefaultRole
	comp := o.PreviewComposition("fix go.mod configuration")
	if comp.Execute.DefaultRole != "go-worker" {
		t.Fatalf("preview role=%q", comp.Execute.DefaultRole)
	}
	if o.Pipeline().Execute.DefaultRole != before {
		t.Fatalf("preview mutated pipeline: before=%q after=%q", before, o.Pipeline().Execute.DefaultRole)
	}
	if _, err := os.Stat(filepath.Join(cfg.SlmDir(), composer.DynamicFileName)); !os.IsNotExist(err) {
		t.Fatalf("preview should not persist composition, stat err=%v", err)
	}
}

func TestActivateDynamicCompositionEmitsAnnotatedTelemetryWithoutPersistingHints(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".slmcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(root)
	cfg.DynamicPipeline = true
	cfg.Model = "qwen2.5-coder:7b"
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var data any
	o.OnEvent(func(e Event) {
		if e.Kind == stream.KindComposition {
			data = e.Data
		}
	})
	comp := &composer.Composition{
		Summary: "focused local run",
		Handoff: []string{
			"Touch pkg/a.go only",
			"Verify with go test ./pkg/...",
		},
		Phases: []composer.PhaseChoice{
			{ID: "execute", Agent: "worker", Enabled: true, When: pipeline.WhenAlways},
			{ID: "test", Agent: "tester", Enabled: true, When: pipeline.WhenAlways},
		},
		Execute: composer.ExecuteChoice{DefaultRole: "worker", Reviewer: "reviewer", Corrector: "corrector", MaxWaves: 1},
		Team:    []composer.TeamMember{{Role: "worker"}},
	}
	if err := o.activateDynamicComposition(comp, "fix package", []string{"pkg/a.go"}, true); err != nil {
		t.Fatal(err)
	}
	annotated, ok := data.(composer.AnnotatedComposition)
	if !ok {
		t.Fatalf("composition event data type=%T", data)
	}
	if annotated.Summary != "focused local run" || len(annotated.SLMFit) == 0 {
		t.Fatalf("annotated=%+v", annotated)
	}
	body, err := os.ReadFile(filepath.Join(cfg.SlmDir(), composer.DynamicFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "slm_fit") {
		t.Fatalf("persisted composition should not contain derived slm_fit: %s", string(body))
	}
}

func TestRankComposerInventoryPrioritizesTargetsAndManifests(t *testing.T) {
	inventory := []string{
		"pkg/zz_unused.go",
		"package.json",
		"pkg/orchestrator/composer.go",
		"web/src/App.tsx",
		"README.md",
	}
	got := rankComposerInventory("fix composer.go dynamic pipeline", inventory, 3)
	if len(got) != 3 {
		t.Fatalf("ranked=%v", got)
	}
	if got[0] != "pkg/orchestrator/composer.go" {
		t.Fatalf("target file should rank first: %v", got)
	}
	if !containsString(got, "package.json") {
		t.Fatalf("project manifest should stay in budget: %v", got)
	}
}

func TestComposerInventoryLimitFollowsSmallModelBudget(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Model = "qwen2.5-coder:7b"
	o := &Orchestrator{cfg: cfg}
	if got := o.composerInventoryLimit(); got != 24 {
		t.Fatalf("7b inventory limit=%d", got)
	}
	cfg.Model = "Qwen3-Coder-30B-A3B-Instruct"
	cfg.MaxContextKB = 16
	if got := o.composerInventoryLimit(); got != 48 {
		t.Fatalf("30b inventory limit=%d", got)
	}
	cfg.MaxContextKB = 8
	if got := o.composerInventoryLimit(); got != 24 {
		t.Fatalf("tight max_context_kb inventory limit=%d", got)
	}
}

func TestQueryLanguageSpecialists(t *testing.T) {
	cases := []struct {
		query  string
		worker string
		tester string
	}{
		{"Generate an HTML + JavaScript battleship game", "web-worker", "web-tester"},
		{"Add a Rust function using cargo", "rust-worker", "rust-tester"},
		{"Create a Java class with Maven", "java-worker", "java-tester"},
		{"Build a C++ project with CMake", "cpp-worker", "cpp-tester"},
		{"Write a Python FastAPI endpoint", "python-worker", "python-tester"},
		{"A TypeScript React component", "react-worker", "react-tester"},
		{"Improve the React frontend", "react-worker", "react-tester"},
		{"Add vanilla JavaScript to this webpage", "web-worker", "web-tester"},
		{"Refactor this mystery codebase", "", ""},
	}
	for _, c := range cases {
		w, tst := queryLanguageSpecialists(c.query)
		if w != c.worker || tst != c.tester {
			t.Fatalf("query %q → (%q,%q), want (%q,%q)", c.query, w, tst, c.worker, c.tester)
		}
	}
}

func TestQueryLanguageSpecialistsJavaVsJavaScript(t *testing.T) {
	if w, _ := queryLanguageSpecialists("Add a JavaScript module"); w == "java-worker" {
		t.Fatalf("javascript must not map to java-worker")
	}
	if w, _ := queryLanguageSpecialists("Create a Java class"); w != "java-worker" {
		t.Fatalf("java must map to java-worker, got %q", w)
	}
}

func TestProjectLanguageSpecialists(t *testing.T) {
	cases := map[string]string{
		"Go":     "go-worker",
		"Python": "python-worker",
		// A tsconfig.json says TypeScript, not React — the ts-* pack exists for
		// exactly this. React is selected by query keyword, which runs first.
		"TypeScript": "ts-worker",
		"JavaScript": "web-worker",
		"HTML":       "web-worker",
		"Rust":       "rust-worker",
		"Java":       "java-worker",
		"C++":        "cpp-worker",
		"C/Make":     "cpp-worker",
		// The six packs the repo ships that nothing could route to.
		"Kotlin": "kotlin-worker",
		"C#":     "dotnet-worker",
		"Ruby":   "ruby-worker",
		"PHP":    "php-worker",
		"Swift":  "swift-worker",
	}
	for lang, want := range cases {
		got, _ := projectLanguageSpecialists(lang)
		if got != want {
			t.Fatalf("%s -> %s, want %s", lang, got, want)
		}
	}
	if got, _ := projectLanguageSpecialists("unknown"); got != "" {
		t.Fatalf("unknown lang got %s", got)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
