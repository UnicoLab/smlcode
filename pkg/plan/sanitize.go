package plan

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	pathLike = regexp.MustCompile(`(?i)\b([a-zA-Z0-9_./-]+\.(?:go|ts|tsx|js|jsx|py|md|json|yaml|yml|toml|rs|java|css|html))\b`)
	fakePath = regexp.MustCompile(`(?i)path/to/|placeholder|/file/containing|example\.com|TODO_PATH`)
)

// ExtractFilePaths pulls likely repo-relative file paths from free text (exploration, notes).
func ExtractFilePaths(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range pathLike.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		p := strings.TrimSpace(m[1])
		p = strings.TrimPrefix(p, "./")
		if fakePath.MatchString(p) || strings.Contains(p, "..") {
			continue
		}
		// Prefer short relative paths
		if strings.Count(p, "/") > 6 {
			continue
		}
		base := filepath.Base(p)
		if base == p && !strings.Contains(p, ".") {
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// SanitizeTasks cleans SLM splitter output for reliable execution.
// root, when set, drops paths that do not exist on disk (kills SLM hallucinations).
// - drops invented placeholder paths
// - injects exploration-discovered files
// - collapses trivial multi-step locate→edit chains into a single worker task when helpful
func SanitizeTasks(tasks []Task, exploration string, query string) []Task {
	return SanitizeTasksIn(tasks, exploration, query, "")
}

// SanitizeTasksIn is SanitizeTasks with an optional workspace root for existence checks.
func SanitizeTasksIn(tasks []Task, exploration, query, root string) []Task {
	known := ExtractFilePaths(exploration)
	known = append(known, ExtractFilePaths(query)...)
	if root != "" {
		known = FilterExisting(root, known)
		if len(known) == 0 {
			known = DiscoverRelevantFiles(root, query, exploration)
		}
	}

	for i := range tasks {
		tasks[i].Normalize()
		var files []string
		for _, f := range tasks[i].Files {
			f = strings.TrimSpace(f)
			if f == "" || fakePath.MatchString(f) || strings.HasPrefix(f, "/") {
				// Drop absolute / invented paths; prefer exploration hits
				continue
			}
			files = append(files, f)
		}
		// Keep planned create targets even when they do not exist yet (greenfield).
		// Only fall back to discovered files when the task listed nothing useful.
		if len(files) == 0 && len(known) > 0 {
			files = append(files, known...)
		}
		tasks[i].Files = ReconcileFiles(root, files, known)
		EnrichTaskFilesForRename(&tasks[i], query)
		// Infer focus files from title/description for create/scaffold tasks.
		if len(tasks[i].Files) == 0 || onlyRootManifest(tasks[i].Files) {
			if inferred := InferCreateFiles(tasks[i].Title + " " + tasks[i].Description + " " + tasks[i].Acceptance); len(inferred) > 0 {
				tasks[i].Files = uniq(append(inferred, tasks[i].Files...))
			}
		}
	}

	if shouldCollapse(tasks, query) {
		return collapseToWorker(tasks, known, query)
	}

	// Drop pure "locate" explorer tasks when we already know files
	if len(known) > 0 {
		var kept []Task
		removed := map[string]bool{}
		for _, t := range tasks {
			title := strings.ToLower(t.Title + " " + t.Description)
			if t.Role == RoleExplorer && (strings.Contains(title, "locate") || strings.Contains(title, "find ") || strings.Contains(title, "search")) {
				removed[t.ID] = true
				continue
			}
			kept = append(kept, t)
		}
		if len(kept) > 0 && len(kept) < len(tasks) {
			for i := range kept {
				var deps []string
				for _, d := range kept[i].DependsOn {
					if !removed[d] {
						deps = append(deps, d)
					}
				}
				kept[i].DependsOn = deps
			}
			tasks = kept
		}
	}

	for i := range tasks {
		tasks[i].Normalize()
	}
	tasks = ensureHTMLEntrypoint(tasks, query)
	// Collapse many worker tasks that all edit the SAME file set into one task.
	// A splitter that emits T1..T6 all editing index.html causes endless
	// "old_str not found" correction loops (each worker's context is stale after
	// the previous edit) — one worker on one self-contained file is far cheaper.
	if shouldCollapseSameFile(tasks) {
		tasks = collapseToWorker(tasks, nil, query)
	}
	tasks = EnsureGreenfieldHarness(tasks, query)
	return EnsureTesterTask(tasks, query)
}

// shouldCollapseSameFile reports whether every worker/deep task targets the exact
// same non-empty file set (the pathological "pile of tasks on one file" shape).
func shouldCollapseSameFile(tasks []Task) bool {
	var files []string
	workers := 0
	for _, t := range tasks {
		if t.Role != RoleWorker && t.Role != "deep" {
			continue
		}
		workers++
		cur := append([]string{}, t.Files...)
		sort.Strings(cur)
		if files == nil {
			files = cur
			continue
		}
		if strings.Join(files, "\x00") != strings.Join(cur, "\x00") {
			return false
		}
	}
	return workers >= 2 && len(files) > 0
}

// ensureHTMLEntrypoint fixes the "pile of .js files with no HTML" failure for
// static-web queries: when the splitter emitted .js/.css assets but no .html
// page, inject index.html into the first worker so the deliverable is loadable.
func ensureHTMLEntrypoint(tasks []Task, query string) []Task {
	if !isHTMLOrWebQuery(strings.ToLower(query)) {
		return tasks
	}
	hasHTML, hasWebAsset := false, false
	for _, t := range tasks {
		for _, f := range t.Files {
			switch strings.ToLower(filepath.Ext(f)) {
			case ".html", ".htm":
				hasHTML = true
			case ".js", ".mjs", ".css":
				hasWebAsset = true
			}
		}
	}
	if hasHTML || !hasWebAsset {
		return tasks
	}
	for i := range tasks {
		if tasks[i].Role == RoleWorker || tasks[i].Role == "deep" {
			tasks[i].Files = uniq(append([]string{"index.html"}, tasks[i].Files...))
			return tasks
		}
	}
	return tasks
}

// EnsureTesterTask appends a final tester task for greenfield / multi-file code
// work when the splitter forgot one. Tiny one-file edits stay worker-only
// (finalize QA gate still runs).
func EnsureTesterTask(tasks []Task, query string) []Task {
	if len(tasks) == 0 {
		return tasks
	}
	hasTester := false
	hasCodeWorker := false
	var lastWorkerID string
	workers := 0
	for _, t := range tasks {
		switch t.Role {
		case RoleTester:
			hasTester = true
		case RoleWorker, "deep":
			hasCodeWorker = true
			workers++
			lastWorkerID = t.ID
		}
	}
	if hasTester || !hasCodeWorker {
		return tasks
	}
	q := strings.ToLower(query)
	// Skip trivial edits — finalize tester + QA gate still cover them.
	// Still inject a tester when the user explicitly asked for pytest/tests.
	wantsTests := strings.Contains(q, "pytest") || strings.Contains(q, "unit test") ||
		strings.Contains(q, "test smoke") || strings.Contains(q, "with a test") ||
		strings.Contains(q, "with tests")
	if !wantsTests && (strings.Contains(q, "tiny") || strings.Contains(q, "doc comment") ||
		strings.Contains(q, "one-line") || strings.Contains(q, "one line")) {
		return tasks
	}
	greenfield := strings.Contains(q, "scaffold") || strings.Contains(q, "greenfield") ||
		strings.Contains(q, "langgraph") || strings.Contains(q, "fastapi") ||
		strings.Contains(q, "pyproject") || strings.Contains(q, "project") ||
		(strings.Contains(q, "create") && (strings.Contains(q, "python") ||
			strings.Contains(q, "package") || strings.Contains(q, "src/") ||
			strings.Contains(q, "agent"))) ||
		workers >= 3 || countDistinctCreatePaths(tasks) >= 3
	if !greenfield {
		return tasks
	}
	id := fmt.Sprintf("T%d", len(tasks)+1)
	for _, t := range tasks {
		if t.ID == id {
			id = "T-test"
			break
		}
	}
	deps := []string{}
	if lastWorkerID != "" {
		deps = []string{lastWorkerID}
	}
	tasks = append(tasks, Task{
		ID:    id,
		Title: "Verify with real execution",
		Description: "Verify the deliverable with the project's ACTUAL language via ws_shell: " +
			"Go → go build/vet/test; Python → pytest/py_compile; JS/TS → npm test/tsc; " +
			"static web → index.html usable, asset refs resolve, node --check each .js. " +
			"passed=true only when the checks exit 0.",
		Role:       RoleTester,
		Column:     ColReadyToDev,
		DependsOn:  deps,
		Acceptance: "Verification commands exit 0; no placeholders; acceptance criteria met",
	})
	return tasks
}

// isPythonGreenfieldQuery detects Python scaffold / template work that needs a
// runnable harness (requirements + entrypoint + pytest). Includes "setup a
// template folder…" phrasing (TestSLMs / LangGraph class-agent requests).
func isPythonGreenfieldQuery(query string) bool {
	q := strings.ToLower(query)
	pythonish := strings.Contains(q, "python") || strings.Contains(q, ".py") ||
		strings.Contains(q, "pytest") || strings.Contains(q, "fastapi") ||
		strings.Contains(q, "langgraph") || strings.Contains(q, "langchain") ||
		strings.Contains(q, "flask") || strings.Contains(q, "django")
	if !pythonish {
		return false
	}
	return strings.Contains(q, "create") || strings.Contains(q, "scaffold") ||
		strings.Contains(q, "greenfield") || strings.Contains(q, "project") ||
		strings.Contains(q, "build") || strings.Contains(q, "mvp") ||
		strings.Contains(q, "setup") || strings.Contains(q, "template") ||
		strings.Contains(q, "folder structure") || strings.Contains(q, "boilerplate") ||
		strings.Contains(q, "langgraph") || strings.Contains(q, "langchain")
}

// EnsureGreenfieldHarness adds worker tasks for requirements.txt, main.py, and
// a minimal pytest smoke when the query is a Python greenfield scaffold without them.
func EnsureGreenfieldHarness(tasks []Task, query string) []Task {
	if len(tasks) == 0 || !isPythonGreenfieldQuery(query) {
		return tasks
	}
	q := strings.ToLower(query)
	langgraphish := strings.Contains(q, "langgraph") || strings.Contains(q, "langchain")

	hasReq, hasTest, hasMain := false, false, false
	var lastID string
	for _, t := range tasks {
		lastID = t.ID
		blob := strings.ToLower(t.Title + " " + t.Description + " " + strings.Join(t.Files, " "))
		if strings.Contains(blob, "requirements.txt") {
			hasReq = true
		}
		if strings.Contains(blob, "test_") || strings.Contains(blob, "tests/") ||
			strings.Contains(blob, "pytest") {
			hasTest = true
		}
		if strings.Contains(blob, "main.py") {
			hasMain = true
		}
	}
	n := len(tasks)
	nextID := func() string {
		n++
		return fmt.Sprintf("T%d", n)
	}
	deps := []string{}
	if lastID != "" {
		deps = []string{lastID}
	}
	if !hasReq {
		id := nextID()
		reqDesc := "Create requirements.txt listing runtime deps (or a comment if none). Keep minimal."
		if langgraphish {
			reqDesc = "Create requirements.txt with langgraph, langchain-core, and pytest " +
				"(pin loosely, e.g. langgraph>=0.2). No invented packages."
		}
		tasks = append(tasks, Task{
			ID: id, Title: "Add requirements.txt",
			Description: reqDesc,
			Role:        RoleWorker, Column: ColReadyToDev, DependsOn: deps,
			Files:      []string{"requirements.txt"},
			Acceptance: "requirements.txt exists, non-empty, and lists real installable packages",
		})
		deps = []string{id}
		lastID = id
	}
	// LangGraph: ensure a real agent module task exists (not only empty __init__.py).
	hasAgentModule := false
	for _, t := range tasks {
		blob := strings.ToLower(t.Title + " " + t.Description + " " + strings.Join(t.Files, " "))
		if strings.Contains(blob, "agent.py") || strings.Contains(blob, "base.py") ||
			strings.Contains(blob, "stategraph") || strings.Contains(blob, "build_graph") {
			hasAgentModule = true
			break
		}
	}
	if langgraphish && !hasAgentModule {
		id := nextID()
		tasks = append(tasks, Task{
			ID: id, Title: "Implement class-based LangGraph agent",
			Description: "Create src/lg_agent/state.py (TypedDict) and src/lg_agent/agents/base.py with a " +
				"BaseAgent/EchoAgent using langgraph.graph.StateGraph + compile() + invoke(). " +
				"Wire at least one node and END. No Placeholder stubs; no `from langgraph import Graph`.",
			Role: RoleWorker, Column: ColReadyToDev, DependsOn: deps,
			Files: []string{"src/lg_agent/state.py", "src/lg_agent/agents/base.py"},
			Acceptance: "Agent class builds StateGraph, compile+invoke works; " +
				"python -c import succeeds; no placeholders",
		})
		deps = []string{id}
		lastID = id
	}

	if !hasMain {
		id := nextID()
		mainDesc := "Create main.py that imports the package and runs a tiny demo / argparse entrypoint."
		mainFiles := []string{"main.py"}
		mainAC := "python main.py exits 0 (or prints a clear usage) without placeholders"
		if langgraphish {
			mainDesc = "Create main.py that constructs the class-based LangGraph agent " +
				"(StateGraph / compiled graph), invokes it once with a sample input, and prints " +
				"the result. Use real langgraph.graph.StateGraph APIs — no Placeholder stubs."
			mainAC = "python main.py runs a sample invoke and exits 0; no Placeholder code"
		}
		tasks = append(tasks, Task{
			ID: id, Title: "Add runnable main.py entrypoint",
			Description: mainDesc,
			Role:        RoleWorker, Column: ColReadyToDev, DependsOn: deps,
			Files:      mainFiles,
			Acceptance: mainAC,
		})
		deps = []string{id}
		lastID = id
		hasMain = true
	}
	if !hasTest && hasMain {
		id := nextID()
		testDesc := "Create tests/test_smoke.py that imports/runs a basic assertion against main.py " +
			"(e.g. subprocess or importlib). Keep it tiny and deterministic."
		testAC := "python -m pytest -q passes for tests/test_smoke.py"
		if langgraphish {
			testDesc = "Create tests/test_smoke.py that imports the agent class, builds/compiles " +
				"the graph (or mocks the LLM), and asserts a non-empty structured result. " +
				"Also assert Placeholder markers are absent from agent source."
			testAC = "python -m pytest -q exits 0; agent module imports; no Placeholder stubs"
		}
		tasks = append(tasks, Task{
			ID: id, Title: "Add pytest smoke test",
			Description: testDesc,
			Role:        RoleWorker, Column: ColReadyToDev, DependsOn: deps,
			Files:      []string{"tests/test_smoke.py"},
			Acceptance: testAC,
		})
	}
	return tasks
}

func onlyRootManifest(files []string) bool {
	if len(files) == 0 {
		return true
	}
	for _, f := range files {
		base := strings.ToLower(filepath.Base(f))
		switch base {
		case "pyproject.toml", "package.json", "go.mod", "cargo.toml", "readme.md", "requirements.txt":
			if strings.Contains(f, "/") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// InferCreateFiles pulls intended create paths from free text (titles like
// "Create src/lg_agent/graph.py").
func InferCreateFiles(text string) []string {
	found := ExtractFilePaths(text)
	lower := strings.ToLower(text)
	// Heuristics for common scaffold names mentioned without extensions in some titles.
	if strings.Contains(lower, "graph.py") {
		found = append(found, "src/lg_agent/graph.py")
	}
	if strings.Contains(lower, "tools.py") {
		found = append(found, "src/lg_agent/tools.py")
	}
	if strings.Contains(lower, "llm.py") {
		found = append(found, "src/lg_agent/llm.py")
	}
	if strings.Contains(lower, "__init__.py") {
		found = append(found, "src/lg_agent/__init__.py")
	}
	if strings.Contains(lower, "test_graph.py") || (strings.Contains(lower, "pytest") && strings.Contains(lower, "test")) {
		found = append(found, "tests/test_graph.py")
	}
	if strings.Contains(lower, "readme") {
		found = append(found, "README.md")
	}
	if strings.Contains(lower, "main.py") || strings.Contains(lower, "entrypoint") {
		found = append(found, "main.py", "src/lg_agent/main.py")
	}
	if strings.Contains(lower, "pyproject") {
		found = append(found, "pyproject.toml")
	}
	// Static web / browser requests always need an HTML entrypoint — otherwise
	// a splitter can emit a pile of disconnected .js files with no page to load them.
	if isHTMLOrWebQuery(lower) {
		found = append(found, "index.html")
		if !strings.Contains(lower, "single file") && !strings.Contains(lower, "one file") &&
			!strings.Contains(lower, "self-contained") {
			found = append(found, "style.css", "script.js")
		}
	}
	return uniq(found)
}

// isHTMLOrWebQuery reports whether a query targets a static browser deliverable
// (vanilla HTML/CSS/JS) as opposed to a Node/React/Go/Python program.
func isHTMLOrWebQuery(lower string) bool {
	htmlish := strings.Contains(lower, "html") || strings.Contains(lower, ".htm") ||
		strings.Contains(lower, "web page") || strings.Contains(lower, "webpage") ||
		strings.Contains(lower, "website") || strings.Contains(lower, "browser") ||
		strings.Contains(lower, "frontend") || strings.Contains(lower, "front-end") ||
		strings.Contains(lower, "vanilla js")
	gameish := (strings.Contains(lower, "game") || strings.Contains(lower, "battleship") ||
		strings.Contains(lower, "battle ship") || strings.Contains(lower, "puzzle") ||
		strings.Contains(lower, "snake") || strings.Contains(lower, "tetris")) &&
		(strings.Contains(lower, "js") || strings.Contains(lower, "javascript") ||
			strings.Contains(lower, "html"))
	return htmlish || gameish
}

func shouldCollapse(tasks []Task, query string) bool {
	if len(tasks) <= 1 {
		return false
	}
	q := strings.ToLower(query)
	// Never collapse greenfield / multi-file scaffold work into one mega-task.
	// "minimal MVP" projects still need atomic file tasks for SLMs.
	if strings.Contains(q, "scaffold") || strings.Contains(q, "greenfield") ||
		strings.Contains(q, "project") || strings.Contains(q, "package") ||
		strings.Contains(q, "pyproject") || strings.Contains(q, "langgraph") ||
		strings.Contains(q, "create a ") || strings.Contains(q, "src/") ||
		strings.Contains(q, "tests/") || countDistinctCreatePaths(tasks) >= 3 {
		return false
	}
	tiny := strings.Contains(q, "tiny") || strings.Contains(q, "doc comment") ||
		strings.Contains(q, "one-line") ||
		(strings.Contains(q, "add") && strings.Contains(q, "comment"))
	if !tiny {
		return false
	}
	// Tiny request → single worker task (avoid research chains)
	return true
}

func countDistinctCreatePaths(tasks []Task) int {
	seen := map[string]bool{}
	for _, t := range tasks {
		for _, f := range t.Files {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			seen[f] = true
		}
	}
	return len(seen)
}

func collapseToWorker(tasks []Task, known []string, query string) []Task {
	files := known
	for _, t := range tasks {
		files = append(files, t.Files...)
	}
	files = uniq(files)
	desc := "Complete the user request in one shot. Use workspace tools (ws_read/ws_edit/ws_write). Stay minimal.\n" +
		"ONLY edit files that exist — never invent paths like internal/... unless listed below.\n\nRequest:\n" + query
	if len(files) > 0 {
		desc += "\n\nKnown files (authoritative):\n- " + strings.Join(files, "\n- ")
	}
	return []Task{{
		ID:          "T1",
		Title:       "Implement request",
		Description: desc,
		Role:        RoleWorker,
		Column:      ColReadyToDev,
		Files:       files,
		Acceptance:  "Change matches the request; keep scope tiny; target files must exist and be edited via tools",
	}}
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
