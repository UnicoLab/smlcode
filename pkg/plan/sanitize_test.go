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

func TestExecutableSoftSkipBlockedDep(t *testing.T) {
	b := &Board{Tasks: []Task{
		{ID: "T1", Column: ColBlocked, Role: RoleExplorer},
		{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
	}}
	ready := b.ReadyTasks()
	if len(ready) != 1 || ready[0].ID != "T2" {
		t.Fatalf("%+v", ready)
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
