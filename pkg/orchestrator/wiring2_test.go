package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/skills"
)

// ── B: mcp.ServerConfig.Writable ────────────────────────────────────────────

// mcp.ServerConfig.IsReadOnly() is `ReadOnly || !Writable`, so a translation
// that sets only ReadOnly leaves every server read-only and a user's explicit
// `read_only: false` does nothing.
func TestMCPServerConfigsHonorExplicitReadOnlyFalse(t *testing.T) {
	no, yes := false, true
	got := mcpServerConfigs([]config.MCPServerConfig{
		{Name: "writable", Command: "srv", ReadOnly: &no},
		{Name: "readonly", Command: "srv", ReadOnly: &yes},
		{Name: "unset", Command: "srv"},
	})
	if len(got) != 3 {
		t.Fatalf("translated %d servers, want 3", len(got))
	}
	if got[0].IsReadOnly() {
		t.Error("read_only: false is inert — the server is still read-only")
	}
	if !got[1].IsReadOnly() {
		t.Error("read_only: true was not honored")
	}
	if !got[2].IsReadOnly() {
		t.Error("an MCP server with no read_only must default to read-only")
	}
}

// ── C: scope-aware skill gating ─────────────────────────────────────────────

func writeScopedSkill(t *testing.T, root, name, frontmatter, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: " + name + "\n" + frontmatter + "---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A Rust skill must not load into a Python project. pkg/skills has implemented
// `paths:` gating for a while; nothing called the scoped resolver, so the
// frontmatter was decorative.
func TestSkillPackIsGatedOnTheTaskFileScope(t *testing.T) {
	o := testOrch(t, nil)
	skillRoot := t.TempDir()
	writeScopedSkill(t, skillRoot, "rust-errors",
		"description: rust error handling\ntriggers: error, result\npaths: \"**/*.rs, **/Cargo.toml\"\n",
		"Use thiserror.")
	writeScopedSkill(t, skillRoot, "python-typing",
		"description: python typing\ntriggers: error, typing\npaths: \"**/*.py\"\n",
		"Annotate everything.")
	o.skills = skills.NewLoader(skillRoot)

	py := o.skillPackForScoped(plan.RoleWorker, "fix the error handling", []string{"app/main.py"})
	if strings.Contains(py, "rust-errors") {
		t.Errorf("a Rust skill loaded into a Python-scoped task:\n%s", py)
	}
	if !strings.Contains(py, "python-typing") {
		t.Errorf("the in-scope skill was dropped:\n%s", py)
	}

	rs := o.skillPackForScoped(plan.RoleWorker, "fix the error handling", []string{"src/main.rs"})
	if !strings.Contains(rs, "rust-errors") {
		t.Errorf("the Rust skill was dropped from a Rust-scoped task:\n%s", rs)
	}

	// An empty scope must disable gating, not delete every gated skill —
	// that is what every phase before the board exists passes.
	none := o.skillPackForScoped(plan.RoleWorker, "fix the error handling", nil)
	if !strings.Contains(none, "rust-errors") || !strings.Contains(none, "python-typing") {
		t.Errorf("an empty scope must disable gating:\n%s", none)
	}
}

// taskFileScope prefers the task's own files and falls back to the run's, so a
// task that declares no files is not silently ungated.
func TestTaskFileScopeFallsBackToTheRunScope(t *testing.T) {
	o := testOrch(t, nil)
	o.boardStore = plan.NewLiveStore(o.cfg.SlmDir())
	if err := o.boardStore.Replace(plan.Board{Tasks: []plan.Task{
		{ID: "T1", Files: []string{"a.py"}},
		{ID: "T2", Files: []string{"b.py", "a.py"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := o.taskFileScope(plan.Task{ID: "T1", Files: []string{"x.rs"}}); len(got) != 1 || got[0] != "x.rs" {
		t.Fatalf("task scope = %v, want its own files", got)
	}
	got := o.taskFileScope(plan.Task{ID: "T3"})
	if len(got) != 2 {
		t.Fatalf("run scope = %v, want the deduped board union", got)
	}
}

// ── D: one language detector ────────────────────────────────────────────────

// The orchestrator's private marker list returned "" for five shipped packs —
// which keyed every one of those runs as `…|*` in the evolve bandit — and
// "C/Make" for CMake, a label no pack answers to.
func TestDetectProjectLangDelegatesToTheBlockRegistry(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		dirs  []string
		want  string
	}{
		{"cmake", map[string]string{"CMakeLists.txt": "project(x)\n"}, nil, "C++"},
		{"kotlin", map[string]string{"build.gradle.kts": ""}, []string{"src/main/kotlin"}, "Kotlin"},
		{"ruby", map[string]string{"Gemfile": "source 'x'\n"}, nil, "Ruby"},
		{"php", map[string]string{"composer.json": "{}"}, nil, "PHP"},
		{"swift", map[string]string{"Package.swift": "//\n"}, nil, "Swift"},
		{"dotnet", map[string]string{"Api.csproj": "<Project/>"}, nil, "C#"},
		{"go", map[string]string{"go.mod": "module x\n"}, nil, "Go"},
		{"typescript", map[string]string{"package.json": "{}", "tsconfig.json": "{}"}, nil, "TypeScript"},
		{"javascript", map[string]string{"package.json": "{}"}, nil, "JavaScript"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ResetLangCache(root)
			got := detectProjectLang(root)
			if got != tc.want {
				t.Fatalf("detectProjectLang = %q, want %q", got, tc.want)
			}
			// Every label the detector can produce must route to a real
			// specialist pair — that is the whole reason the label exists.
			if w, te := projectLanguageSpecialists(got); w == "" || te == "" {
				t.Fatalf("%q routes to no specialist pair", got)
			}
		})
	}
}

// ── E: engine strings carry no UI-specific remedy ───────────────────────────

// These strings are persisted into task notes, TASKS.md and saved sessions, so
// they outlive the renderer that could have translated them.
func TestEngineStringsNameNoSpecificUI(t *testing.T) {
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "x", Column: plan.ColToScope,
		Output: "out", Review: "nope",
	}}}
	plan.ApplyEscalateAction(board, "T1", plan.EscalateActionReScope, "")
	got, _ := board.Get("T1")
	if strings.Contains(got.Notes, "Studio") {
		t.Errorf("re_scope note names Studio: %q", got.Notes)
	}
	_, tasksMD := board.ToMarkdown()
	if !strings.Contains(tasksMD, "slmcode task show") {
		t.Errorf("TASKS.md offers no command every renderer has:\n%s", tasksMD)
	}
}

// ── the escalate retry cap defaults agree across packages ───────────────────

func TestEscalateRetryCapDefaultsAgree(t *testing.T) {
	if config.DefaultEscalateMaxRetries != plan.DefaultMaxGateRetries {
		t.Fatalf("config.DefaultEscalateMaxRetries=%d but plan.DefaultMaxGateRetries=%d",
			config.DefaultEscalateMaxRetries, plan.DefaultMaxGateRetries)
	}
	o := testOrch(t, nil)
	if got := o.maxGateRetries(); got != plan.DefaultMaxGateRetries {
		t.Fatalf("orchestrator cap = %d, want %d", got, plan.DefaultMaxGateRetries)
	}
	o2 := testOrch(t, func(c *config.Config) { c.EscalateMaxRetries = 5 })
	if got := o2.maxGateRetries(); got != 5 {
		t.Fatalf("configured cap = %d, want 5", got)
	}
}

// ── F: Workspace.AddSecrets is wired ────────────────────────────────────────

// A key that arrived as --api-key or as `api_key:` in a user-level config lives
// only in the resolved Config: the workspace scans the environment and
// .slmcode/auth.json and would never find it, so an agent that ran `env` or
// `cat` in a repo containing it got it back verbatim in a tool result.
func TestWorkspaceRedactsCredentialsFromTheResolvedConfig(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	const canary = "sk-flag-supplied-CANARY-0123456789"
	const embedCanary = "sk-embed-CANARY-9876543210"
	cfg.APIKey = canary
	cfg.EmbeddingAPIKey = embedCanary
	if err := InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if o.workspace == nil {
		t.Fatal("no workspace was constructed")
	}
	got := o.workspace.RedactSecrets("OPENAI_API_KEY=" + canary + "\nEMB=" + embedCanary)
	if strings.Contains(got, canary) {
		t.Errorf("the configured api_key survived a tool result: %s", got)
	}
	if strings.Contains(got, embedCanary) {
		t.Errorf("the configured embedding_api_key survived a tool result: %s", got)
	}
}

// ── the escalate gate refuses retry past the cap ────────────────────────────

func TestAutoEscalateStopsGrantingRetriesAtTheCap(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.EscalateAsk = "auto"
	if err := InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Role: plan.RoleWorker, Column: plan.ColToScope,
		Review: "still wrong", Error: "needs human",
	}}}
	o.persistBoard(board)
	for i := 0; i < o.maxGateRetries(); i++ {
		task, _ := board.Get("T1")
		if ans := o.runEscalateAsk(context.Background(), board, task, "detail"); ans.Action != plan.EscalateActionRetry {
			t.Fatalf("retry %d was refused early: %s", i+1, ans.Action)
		}
	}
	task, _ := board.Get("T1")
	ans := o.runEscalateAsk(context.Background(), board, task, "detail")
	if ans.Action != plan.EscalateActionReScope {
		t.Fatalf("auto-retry past the cap = %s, want re_scope", ans.Action)
	}
	got, _ := board.Get("T1")
	if got.GateRetries != o.maxGateRetries() {
		t.Fatalf("gate retries = %d, want the cap %d", got.GateRetries, o.maxGateRetries())
	}
	if got.Column != plan.ColToScope {
		t.Fatalf("task ended in %q, want the human backlog", got.Column)
	}
}

// ── A: the budget default that actually reaches the loop ────────────────────

// config.DefaultMaxTaskCalls is the value buildRunner hands the loop;
// loop.DefaultMaxTaskCalls is only the fallback for a zero-valued Runner. At 6
// a task got two correction rounds no matter what max_retries said.
func TestDefaultTaskCallBudgetPaysForTheDefaultRetries(t *testing.T) {
	def := config.Default(t.TempDir())
	need := loop.MaxTaskCallsFor(def.MaxRetries)
	if config.DefaultMaxTaskCalls < need {
		t.Fatalf("config.DefaultMaxTaskCalls=%d caps the default max_retries=%d, which needs %d",
			config.DefaultMaxTaskCalls, def.MaxRetries, need)
	}
	if def.MaxTaskCalls != config.DefaultMaxTaskCalls {
		t.Fatalf("config.Default().MaxTaskCalls = %d, want %d", def.MaxTaskCalls, config.DefaultMaxTaskCalls)
	}
	if got := maxCorrectionRounds(def.MaxTaskCalls); got < def.MaxRetries {
		t.Fatalf("the default budget buys %d correction rounds, max_retries is %d", got, def.MaxRetries)
	}
}

// ── B: the react_compact docstrings must not out-claim the wiring ───────────

// loop.LiveReactCompactionWired is the constant of record. While it is false,
// nothing that describes react_compact to an operator may promise mid-run or
// in-flight compaction.
func TestReactCompactDocsMatchWhatIsWired(t *testing.T) {
	if loop.LiveReactCompactionWired {
		t.Skip("live compaction is wired — the claims may say so")
	}
	field, ok := config.Field("react_compact")
	if !ok {
		t.Fatal("react_compact has no schema entry")
	}
	blob := strings.ToLower(field.Label + " " + field.Description)
	for _, overclaim := range []string{"mid-run", "in-flight", "in flight", "between iterations"} {
		if strings.Contains(blob, overclaim) {
			t.Errorf("the react_compact schema description claims %q while LiveReactCompactionWired is false: %q",
				overclaim, field.Description)
		}
	}
	if !strings.Contains(blob, "checkpoint") {
		t.Errorf("the react_compact description does not say WHEN it compacts: %q", field.Description)
	}
}
