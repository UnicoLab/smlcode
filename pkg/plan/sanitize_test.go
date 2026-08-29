package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeTasksCollapsesTiny(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Title: "Locate Hello", Role: RoleExplorer, Column: ColReadyToDev},
		{ID: "T2", Title: "Verify", Role: RoleExplorer, DependsOn: []string{"T1"}, Files: []string{"path/to/file/containing/Hello"}, Column: ColReadyToDev},
		{ID: "T3", Title: "Add doc", Role: RoleWorker, DependsOn: []string{"T2"}, Column: ColReadyToDev},
		{ID: "T4", Title: "Validate", Role: RoleTester, DependsOn: []string{"T3"}, Column: ColReadyToDev},
	}
	out := SanitizeTasks(tasks, "Found hello.go with func Hello()", "Add a Doc comment to Hello(). Keep the change tiny.")
	if len(out) != 1 {
		t.Fatalf("expected collapse to 1 task for tiny query, got %d: %+v", len(out), out)
	}
	if out[0].Role != RoleWorker {
		t.Fatalf("role=%s", out[0].Role)
	}
	if len(out[0].Files) == 0 || out[0].Files[0] != "hello.go" {
		t.Fatalf("files=%v", out[0].Files)
	}
}

func TestSanitizeTasksKeepsGreenfieldAtomic(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Title: "Create pyproject.toml", Role: RoleWorker, Files: []string{"pyproject.toml"}},
		{ID: "T2", Title: "Create README.md", Role: RoleWorker, Files: []string{"README.md"}},
		{ID: "T3", Title: "Create graph.py", Role: RoleWorker, Files: []string{"src/lg_agent/graph.py"}},
		{ID: "T4", Title: "Create tools.py", Role: RoleWorker, Files: []string{"src/lg_agent/tools.py"}},
		{ID: "T5", Title: "Create llm.py", Role: RoleWorker, Files: []string{"src/lg_agent/llm.py"}},
		{ID: "T6", Title: "Create main.py", Role: RoleWorker, Files: []string{"src/lg_agent/main.py"}},
		{ID: "T7", Title: "Create tests", Role: RoleWorker, Files: []string{"tests/test_graph.py"}},
		{ID: "T8", Title: "Create __init__", Role: RoleWorker, Files: []string{"src/lg_agent/__init__.py"}},
	}
	q := "Create a minimal Python LangGraph agent project MVP with pyproject.toml and src/lg_agent/"
	out := SanitizeTasks(tasks, "", q)
	if len(out) < 6 {
		t.Fatalf("greenfield must not collapse to mega-task, got %d: %+v", len(out), out)
	}
	hasTester := false
	for _, tsk := range out {
		if tsk.Role == RoleTester {
			hasTester = true
		}
	}
	if !hasTester {
		t.Fatal("greenfield should auto-append a tester task")
	}
}

func TestSanitizeTasksDropsHallucinatedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "math.go"), []byte("package main\nfunc Add(a,b int) int { return a+b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks := []Task{{
		ID: "T1", Title: "Add doc", Role: RoleWorker, Column: ColReadyToDev,
		Files: []string{"internal/calculator/calculator.go"},
	}}
	out := SanitizeTasksIn(tasks, "math.go", "Add a doc comment to Add(). Keep tiny.", root)
	if len(out) != 1 {
		t.Fatalf("got %d", len(out))
	}
	if len(out[0].Files) != 1 || out[0].Files[0] != "math.go" {
		t.Fatalf("files=%v want [math.go]", out[0].Files)
	}
}

func TestReconcileFiles(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644)
	got := ReconcileFiles(root, []string{"missing/x.go"}, []string{"a.go"})
	if len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("%v", got)
	}
}

func TestReconcileFilesKeepsGreenfieldCreates(t *testing.T) {
	root := t.TempDir()
	got := ReconcileFiles(root, []string{"src/lg_agent/graph.py", "pyproject.toml"}, nil)
	if len(got) < 2 {
		t.Fatalf("expected create targets kept, got %v", got)
	}
}

// The Python conventions were all present and the Go and web ones were not, so
// a greenfield Go or full-stack request lost its create targets here.
//
// Measured end to end on a live 30B, before the fix: "create basic HTTP server
// in main.go" reached the worker with an empty file list, and a worker whose
// prompt forbids inventing paths had nothing it was permitted to write. The run
// finished 0/6 tasks with nothing on disk.
func TestReconcileFilesKeepsGoAndWebGreenfieldCreates(t *testing.T) {
	root := t.TempDir()
	for _, claimed := range []string{
		"main.go",            // the Go entrypoint; main.py was already accepted
		"cmd/server/main.go", // canonical Go layout
		"web/src/App.jsx",    // the front-end tree inside a backend repo
		"index.html",
		"api/handlers.go",
		// `pkg/` is as canonical in Go as `src/` is in Python, and a live
		// composer asked for exactly this path on a greenfield Go request.
		"pkg/tasks/store.go",
	} {
		if got := ReconcileFiles(root, []string{claimed}, nil); len(got) != 1 || got[0] != claimed {
			t.Errorf("ReconcileFiles(%q) = %v, want it kept as a create target", claimed, got)
		}
	}
}

// The other half: broadening the allowlist must not re-open the fabrication
// hole. `internal/...` is the shape a model invents when guessing at a layout,
// and the worker prompt names it explicitly, so it stays rejected.
func TestReconcileFilesStillRejectsInventedLayouts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, claimed := range []string{"internal/nope.go", "path/to/file.go", "placeholder.go"} {
		got := ReconcileFiles(root, []string{claimed}, []string{"real.go"})
		if len(got) != 1 || got[0] != "real.go" {
			t.Errorf("ReconcileFiles(%q) = %v, want the discovered file instead", claimed, got)
		}
	}
}

func TestListWorkspaceFilesPrioritizesManifestsAndSkipsGeneratedUI(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                            "module example.com/x\n\ngo 1.22\n",
		"cmd/slmcode/ui/assets/app.js":      "generated",
		"cmd/slmcode/main.go":               "package main\n",
		"pkg/worker/worker.go":              "package worker\n",
		"web/src/App.tsx":                   "export default function App() { return null }\n",
		"node_modules/pkg/index.js":         "ignored",
		".slmcode/composition.dynamic.json": "{}",
	}
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := ListWorkspaceFiles(root, 3)
	if len(got) != 3 {
		t.Fatalf("inventory=%v", got)
	}
	if got[0] != "go.mod" {
		t.Fatalf("manifest should rank first: %v", got)
	}
	for _, path := range got {
		if strings.Contains(path, "cmd/slmcode/ui") || strings.Contains(path, "node_modules") || strings.Contains(path, ".slmcode") {
			t.Fatalf("generated/hidden path leaked into inventory: %v", got)
		}
	}
}

func TestInferCreateFiles(t *testing.T) {
	got := InferCreateFiles("Create src/lg_agent/graph.py with StateGraph")
	found := false
	for _, f := range got {
		if f == "src/lg_agent/graph.py" {
			found = true
		}
	}
	if !found {
		t.Fatalf("%v", got)
	}
}

// This used to assert that a blocked dependency was SOFT-SKIPPED — that T2 ran
// even though the locate task it depends on failed. That was the bug, not the
// feature: T2 then edits files T1 never found. A failed dependency now blocks
// its dependents, which is what puts them in front of a human. See
// deps_test.go for the full matrix.
func TestExecutableBlockedDepBlocksDependent(t *testing.T) {
	b := &Board{Tasks: []Task{
		{ID: "T1", Column: ColBlocked, Role: RoleExplorer},
		{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
	}}
	if ready := b.ReadyTasks(); len(ready) != 0 {
		t.Fatalf("ready=%+v want none — T1 failed, so T2 has nothing to build on", ready)
	}
	t2, _ := b.Get("T2")
	if t2.Column != ColBlocked {
		t.Fatalf("T2 column=%s want blocked", t2.Column)
	}
	if !strings.Contains(t2.Error, "T1") {
		t.Fatalf("T2 Error=%q want it to name the failed upstream", t2.Error)
	}
}

func TestEnsureGreenfieldHarness(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Title: "Create main.py", Role: RoleWorker, Files: []string{"main.py"}},
	}
	q := "Create a Python CLI project with main.py"
	out := EnsureGreenfieldHarness(tasks, q)
	hasReq, hasTest := false, false
	for _, tsk := range out {
		blob := tsk.Title + " " + strings.Join(tsk.Files, " ")
		if strings.Contains(blob, "requirements.txt") {
			hasReq = true
		}
		if strings.Contains(blob, "test_smoke") || strings.Contains(blob, "pytest") {
			hasTest = true
		}
	}
	if !hasReq {
		t.Fatalf("expected requirements.txt task, got %+v", out)
	}
	if !hasTest {
		t.Fatalf("expected pytest smoke task, got %+v", out)
	}
	// Non-greenfield / non-python stays unchanged.
	out2 := EnsureGreenfieldHarness(tasks, "Rename Hello to Greet in hello.go")
	if len(out2) != 1 {
		t.Fatalf("non-python should be unchanged: %+v", out2)
	}
}

func TestEnsureGreenfieldHarnessLangGraphTemplateSetup(t *testing.T) {
	// User phrasing that previously skipped harness ("setup a template folder…").
	q := "I want you to setup a template folder structure for langgraph agent using class approach " +
		"and all the langchain abstractions to have scalable and maintainable code."
	tasks := []Task{
		{ID: "T1", Title: "Create base directory structure", Role: RoleWorker,
			Files: []string{"src/lg_agent/__init__.py"}},
		{ID: "T2", Title: "Implement LangGraph agent class", Role: RoleWorker,
			Files: []string{"src/lg_agent/agents/agent.py"}},
	}
	if !isPythonGreenfieldQuery(q) {
		t.Fatal("langgraph template setup must count as python greenfield")
	}
	out := EnsureGreenfieldHarness(tasks, q)
	hasReq, hasMain, hasTest := false, false, false
	for _, tsk := range out {
		blob := strings.ToLower(tsk.Title + " " + strings.Join(tsk.Files, " ") + " " + tsk.Acceptance)
		if strings.Contains(blob, "requirements.txt") {
			hasReq = true
			if !strings.Contains(strings.ToLower(tsk.Description), "langgraph") {
				t.Fatalf("langgraph reqs description expected: %q", tsk.Description)
			}
		}
		if strings.Contains(blob, "main.py") {
			hasMain = true
		}
		if strings.Contains(blob, "test_smoke") || strings.Contains(blob, "pytest") {
			hasTest = true
		}
	}
	if !hasReq || !hasMain || !hasTest {
		t.Fatalf("expected req+main+test harness, got %+v", out)
	}
}

func TestEnsureHTMLEntrypointInjectsIndexHTML(t *testing.T) {
	// Regression: a static-web query split into .js files must gain an HTML page.
	tasks := []Task{
		{ID: "T1", Title: "Create game logic", Role: RoleWorker, Files: []string{"game.js"}},
		{ID: "T2", Title: "Create board renderer", Role: RoleWorker, Files: []string{"board.js"}},
	}
	out := ensureHTMLEntrypoint(tasks, "Generate an HTML + JavaScript battleship game")
	found := false
	for _, t := range out {
		for _, f := range t.Files {
			if f == "index.html" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected index.html injected, got %+v", out)
	}
}

func TestEnsureHTMLEntrypointNoopWhenHTMLPresent(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Title: "Create page", Role: RoleWorker, Files: []string{"index.html", "style.css", "game.js"}},
	}
	out := ensureHTMLEntrypoint(tasks, "Generate an HTML game")
	// No duplicate injection; the first worker keeps its existing files.
	if len(out[0].Files) != 3 {
		t.Fatalf("expected no injection when index.html present, got %+v", out[0].Files)
	}
}

func TestEnsureHTMLEntrypointNoopForNonWeb(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Title: "Create lib", Role: RoleWorker, Files: []string{"main.go"}},
	}
	out := ensureHTMLEntrypoint(tasks, "Add a doc comment to Hello() in Go")
	for _, tsk := range out {
		for _, f := range tsk.Files {
			if f == "index.html" {
				t.Fatalf("must not inject index.html for non-web query: %+v", out)
			}
		}
	}
}

func TestShouldCollapseSameFile(t *testing.T) {
	// The pathological shape: many workers editing one self-contained file.
	same := []Task{
		{ID: "T1", Title: "HTML structure", Role: RoleWorker, Files: []string{"index.html"}},
		{ID: "T2", Title: "CSS styling", Role: RoleWorker, Files: []string{"index.html"}},
		{ID: "T3", Title: "JS logic", Role: RoleWorker, Files: []string{"index.html"}},
	}
	if !shouldCollapseSameFile(same) {
		t.Fatal("same-file workers must collapse")
	}

	// Different files per task must NOT collapse.
	diff := []Task{
		{ID: "T1", Title: "main.py", Role: RoleWorker, Files: []string{"main.py"}},
		{ID: "T2", Title: "requirements.txt", Role: RoleWorker, Files: []string{"requirements.txt"}},
	}
	if shouldCollapseSameFile(diff) {
		t.Fatal("different-file workers must not collapse")
	}

	// A single worker must not collapse.
	if shouldCollapseSameFile([]Task{{ID: "T1", Role: RoleWorker, Files: []string{"index.html"}}}) {
		t.Fatal("single worker must not collapse")
	}
}

// Collapse must not silently disarm executable acceptance. Measured against a
// live 30B: the splitter emitted criteria with `go test ./...` as the verify
// command, every task targeted the same file set, and the collapsed survivor
// reached the reviewer with no criteria at all — so VerifyCriteria ran nothing
// and "the harness did not check" was indistinguishable from a pass.
func TestCollapseCarriesCriteria(t *testing.T) {
	tasks := []Task{
		{ID: "T1", Title: "Fix even case", Role: RoleWorker, Files: []string{"stats.go"},
			Criteria: []Criterion{
				{ID: "C1", Text: "Median returns the truncated mean for even input", Priority: "must", Verify: "go test ./..."},
			}},
		{ID: "T2", Title: "Keep odd case", Role: RoleWorker, Files: []string{"stats.go"},
			Criteria: []Criterion{
				// Same command, different condition: the command must not be
				// the thing that dedupes them.
				{ID: "C1", Text: "Odd-length input is unchanged", Priority: "should", Verify: "go test ./..."},
				// An exact repeat of T1's criterion — this one must dedupe.
				{ID: "C2", Text: "Median returns the truncated mean for even input", Priority: "must", Verify: "go test ./..."},
			}},
	}
	out := collapseToWorker(tasks, nil, "fix Median")
	if len(out) != 1 {
		t.Fatalf("expected one collapsed task, got %d", len(out))
	}
	got := out[0].Criteria
	if len(got) != 2 {
		t.Fatalf("expected 2 criteria after dedupe, got %d: %+v", len(got), got)
	}
	ids := map[string]bool{}
	for _, c := range got {
		if c.ID == "" {
			t.Errorf("criterion has no ID after collapse: %+v", c)
		}
		if ids[c.ID] {
			t.Errorf("duplicate criterion ID %q survived collapse: %+v", c.ID, got)
		}
		ids[c.ID] = true
		if c.Verify != "go test ./..." {
			t.Errorf("verify command lost: %+v", c)
		}
	}
	// Priority must survive: a demoted "must" is a requirement that ships unmet.
	if got[0].Priority != PriorityMust {
		t.Errorf("blocking priority lost: %+v", got[0])
	}
}

// A collapse of tasks that carry no criteria must stay exactly as it was —
// nil, not an empty non-nil slice that would read as "checked nothing".
func TestCollapseWithoutCriteriaStaysNil(t *testing.T) {
	out := collapseToWorker([]Task{
		{ID: "T1", Role: RoleWorker, Files: []string{"a.go"}},
		{ID: "T2", Role: RoleWorker, Files: []string{"a.go"}},
	}, nil, "do a thing")
	if len(out) != 1 {
		t.Fatalf("expected one collapsed task, got %d", len(out))
	}
	if out[0].Criteria != nil {
		t.Fatalf("expected nil criteria, got %+v", out[0].Criteria)
	}
}

// MaxCriteria is the ceiling on the survivor too: a wide split must not smuggle
// an unbounded checklist onto one task.
func TestCollapseCapsCriteria(t *testing.T) {
	var tasks []Task
	for i := 0; i < MaxCriteria+4; i++ {
		tasks = append(tasks, Task{
			ID: "T" + string(rune('1'+i)), Role: RoleWorker, Files: []string{"a.go"},
			Criteria: []Criterion{{Text: "condition " + string(rune('a'+i)), Verify: "go test ./..."}},
		})
	}
	out := collapseToWorker(tasks, nil, "do a thing")
	if n := len(out[0].Criteria); n > MaxCriteria {
		t.Fatalf("collapse carried %d criteria, over the %d cap", n, MaxCriteria)
	}
}
