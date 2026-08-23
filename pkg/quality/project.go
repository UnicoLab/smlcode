package quality

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CompletenessIssue is a project-level gap vs a strong reference bar
// (what a senior engineer would ship for the query).
type CompletenessIssue struct {
	Code   string `json:"code"` // missing_file | missing_dep | bad_import | placeholder | empty_package | weak_entrypoint | missing_graph | missing_tests
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
}

// CheckProjectCompleteness compares the workspace to a high-quality reference
// for the given query. Deterministic — no LLM. Used by finalize + eval.
//
// Every branch below used to end in a PYTHON scaffold check, so a Go, Rust or
// TypeScript workspace was walked with listPythonSources, matched against
// Python placeholder patterns, and told it was missing requirements.txt. The
// result was always empty for those languages, which meant the completeness bar
// — the check that stops "I created the files" from counting as done — simply
// did not exist outside Python. Routing on the workspace's actual language
// fixes that.
func CheckProjectCompleteness(root, query string) []CompletenessIssue {
	if root == "" {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	// Framework-specific reference bars win: they know the exact shape expected.
	switch {
	case strings.Contains(q, "langgraph") || strings.Contains(q, "langchain"):
		return checkLangGraphTemplate(root)
	case strings.Contains(q, "fastapi"):
		return checkFastAPIMinimal(root)
	case isPythonCLIQuery(q):
		return checkPythonCLI(root)
	}
	switch projectKind(root) {
	case "go":
		return checkGoScaffold(root, q)
	case "node":
		return checkNodeScaffold(root, q)
	case "rust":
		return checkRustScaffold(root, q)
	case "python":
		return checkGenericPythonScaffold(root, q)
	default:
		// An unrecognized workspace gets no bar rather than the Python one.
		return nil
	}
}

// projectKind names the workspace's primary language from its root markers.
// Deliberately narrow: it answers "which completeness bar applies", not "which
// quality pack to use" — pkg/blocks owns that, and importing it here would
// close an import cycle through pkg/skills.
func projectKind(root string) string {
	switch {
	case fileExists(filepath.Join(root, "go.mod")):
		return "go"
	case fileExists(filepath.Join(root, "Cargo.toml")):
		return "rust"
	case fileExists(filepath.Join(root, "package.json")):
		return "node"
	case fileExists(filepath.Join(root, "pyproject.toml")),
		fileExists(filepath.Join(root, "requirements.txt")),
		fileExists(filepath.Join(root, "setup.py")),
		fileExists(filepath.Join(root, "setup.cfg")):
		return "python"
	}
	// No manifest: fall back to what sources are actually present.
	switch {
	case hasGoSources(root):
		return "go"
	case hasPythonSources(root):
		return "python"
	}
	return ""
}

// wantsScaffold reports whether the query asked for something whose reference
// answer includes a manifest and tests, rather than a one-line edit.
func wantsScaffold(query string) bool {
	for _, w := range []string{"scaffold", "template", "project", "setup", "boilerplate", "starter"} {
		if strings.Contains(query, w) {
			return true
		}
	}
	return false
}

// wantsTests reports whether the query explicitly asked for tests.
func wantsTests(query string) bool {
	for _, w := range []string{"test", "spec", "coverage", "tdd"} {
		if strings.Contains(query, w) {
			return true
		}
	}
	return false
}

func checkGoScaffold(root, query string) []CompletenessIssue {
	var issues []CompletenessIssue
	sources := listSourcesByExt(root, ".go")
	if len(sources) == 0 {
		return nil
	}
	if !fileExists(filepath.Join(root, "go.mod")) && wantsScaffold(query) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_file", Path: "go.mod",
			Reason: "Go sources with no go.mod — the module does not build",
		})
	}
	hasTest := false
	for _, rel := range sources {
		if strings.HasSuffix(rel, "_test.go") {
			hasTest = true
			continue
		}
		if why := staticReason(readFile(filepath.Join(root, rel)), ".go"); why != "" {
			issues = append(issues, CompletenessIssue{Code: "placeholder", Path: rel, Reason: why})
		}
	}
	if !hasTest && (wantsTests(query) || wantsScaffold(query)) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_tests", Path: "*_test.go",
			Reason: "no _test.go anywhere — `go test ./...` verifies nothing",
		})
	}
	return dedupeIssues(issues)
}

func checkNodeScaffold(root, query string) []CompletenessIssue {
	var issues []CompletenessIssue
	pkgPath := filepath.Join(root, "package.json")
	if !fileExists(pkgPath) {
		return nil
	}
	body := readFile(pkgPath)
	if wantsTests(query) && !strings.Contains(body, `"test"`) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_file", Path: "package.json",
			Reason: `no "test" script — npm test cannot run, so the gate proves nothing`,
		})
	}
	hasTest := false
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs"} {
		for _, rel := range listSourcesByExt(root, ext) {
			low := strings.ToLower(rel)
			if strings.Contains(low, ".test.") || strings.Contains(low, ".spec.") ||
				strings.Contains(low, "__tests__/") || strings.HasPrefix(low, "test/") {
				hasTest = true
				continue
			}
			if why := staticReason(readFile(filepath.Join(root, rel)), ext); why != "" {
				issues = append(issues, CompletenessIssue{Code: "placeholder", Path: rel, Reason: why})
			}
		}
	}
	if !hasTest && wantsTests(query) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_tests", Path: "*.test.ts",
			Reason: "no *.test.* / *.spec.* file — the test runner collects nothing",
		})
	}
	return dedupeIssues(issues)
}

func checkRustScaffold(root, query string) []CompletenessIssue {
	var issues []CompletenessIssue
	if !fileExists(filepath.Join(root, "Cargo.toml")) {
		return nil
	}
	sources := listSourcesByExt(root, ".rs")
	if len(sources) == 0 {
		return []CompletenessIssue{{
			Code: "missing_file", Path: "src/main.rs",
			Reason: "Cargo.toml with no .rs sources — the crate has no code",
		}}
	}
	hasTest := false
	for _, rel := range sources {
		text := readFile(filepath.Join(root, rel))
		if strings.Contains(text, "#[test]") || strings.Contains(text, "#[cfg(test)]") ||
			strings.HasPrefix(rel, "tests"+string(filepath.Separator)) {
			hasTest = true
		}
		if strings.Contains(text, "todo!()") || strings.Contains(text, "unimplemented!()") {
			issues = append(issues, CompletenessIssue{
				Code: "placeholder", Path: rel,
				Reason: "contains todo!() / unimplemented!() on a path that was to be implemented",
			})
		}
	}
	if !hasTest && (wantsTests(query) || wantsScaffold(query)) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_tests", Path: "tests/",
			Reason: "no #[test] and no tests/ directory — cargo test runs nothing",
		})
	}
	return dedupeIssues(issues)
}

// listSourcesByExt walks the workspace for one extension, skipping dependency
// and build directories.
func listSourcesByExt(root, ext string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".slmcode", "node_modules", "vendor", "target", "dist",
				"build", "__pycache__", ".venv", "venv", ".next", "coverage":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ext) {
			return nil
		}
		if rel, err := filepath.Rel(root, path); err == nil {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// FormatCompletenessReport renders issues for agents / Studio / SCRATCH.
func FormatCompletenessReport(issues []CompletenessIssue) string {
	if len(issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Project completeness (reference bar)\n")
	b.WriteString(fmt.Sprintf("%d gap(s) vs expert-quality deliverable:\n", len(issues)))
	for _, is := range issues {
		if is.Path != "" {
			b.WriteString(fmt.Sprintf("- [%s] `%s` — %s\n", is.Code, is.Path, is.Reason))
		} else {
			b.WriteString(fmt.Sprintf("- [%s] %s\n", is.Code, is.Reason))
		}
	}
	b.WriteString("Do not mark success until these match a working, tested template.\n")
	return b.String()
}

func checkLangGraphTemplate(root string) []CompletenessIssue {
	var issues []CompletenessIssue

	reqPath := filepath.Join(root, "requirements.txt")
	if !fileExists(reqPath) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_file", Path: "requirements.txt",
			Reason: "reference ships requirements.txt with langgraph + langchain-core + pytest",
		})
	} else {
		body := strings.ToLower(readFile(reqPath))
		for _, dep := range []string{"langgraph", "langchain-core", "pytest"} {
			if !strings.Contains(body, dep) {
				issues = append(issues, CompletenessIssue{
					Code: "missing_dep", Path: "requirements.txt",
					Reason: "missing dependency: " + dep,
				})
			}
		}
	}

	mainPath := filepath.Join(root, "main.py")
	if !fileExists(mainPath) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_file", Path: "main.py",
			Reason: "reference has runnable main.py that constructs + invokes the agent",
		})
	} else {
		main := readFile(mainPath)
		ml := strings.ToLower(main)
		if !strings.Contains(ml, "invoke") && !strings.Contains(ml, "ainvoke") &&
			!strings.Contains(ml, "agent") && !strings.Contains(ml, "graph") {
			issues = append(issues, CompletenessIssue{
				Code: "weak_entrypoint", Path: "main.py",
				Reason: "main.py does not invoke/build an agent or graph",
			})
		}
		if rePlaceholder.MatchString(main) || reStubReturn.MatchString(main) {
			issues = append(issues, CompletenessIssue{
				Code: "placeholder", Path: "main.py",
				Reason: "entrypoint still has placeholder/stub code",
			})
		}
	}

	hasTests := dirExists(filepath.Join(root, "tests")) || hasPythonTests(root)
	if !hasTests {
		issues = append(issues, CompletenessIssue{
			Code: "missing_tests", Path: "tests/",
			Reason: "reference includes pytest smoke covering agent import/graph compile",
		})
	}

	pyFiles := listPythonSources(root)
	hasStateGraph := false
	hasAgentClass := false
	for _, rel := range pyFiles {
		text := readFile(filepath.Join(root, rel))
		if reBadLangGraphImport.MatchString(text) {
			issues = append(issues, CompletenessIssue{
				Code: "bad_import", Path: rel,
				Reason: "invalid `from langgraph import Graph` — use langgraph.graph.StateGraph",
			})
		}
		tl := strings.ToLower(text)
		if strings.Contains(tl, "stategraph") || strings.Contains(text, "langgraph.graph") {
			hasStateGraph = true
		}
		if strings.Contains(text, "class ") &&
			(strings.Contains(tl, "agent") || strings.Contains(tl, "build_graph") ||
				strings.Contains(tl, "stategraph")) {
			hasAgentClass = true
		}
		if why := staticReason(text, ".py"); why != "" && !strings.HasSuffix(rel, "__init__.py") {
			issues = append(issues, CompletenessIssue{
				Code: "placeholder", Path: rel, Reason: why,
			})
		}
	}
	if !hasStateGraph {
		issues = append(issues, CompletenessIssue{
			Code:   "missing_graph",
			Reason: "no langgraph.graph.StateGraph usage found — reference class agent must build a real graph",
		})
	}
	if !hasAgentClass {
		issues = append(issues, CompletenessIssue{
			Code:   "missing_graph",
			Reason: "no class-based agent with graph build/invoke found",
		})
	}
	issues = append(issues, emptyPackageIssues(root)...)
	return dedupeIssues(issues)
}

func checkFastAPIMinimal(root string) []CompletenessIssue {
	var issues []CompletenessIssue
	if !fileExists(filepath.Join(root, "main.py")) && !fileExists(filepath.Join(root, "app/main.py")) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_file", Path: "main.py",
			Reason: "FastAPI app entrypoint missing",
		})
	}
	found := false
	for _, rel := range listPythonSources(root) {
		text := readFile(filepath.Join(root, rel))
		if strings.Contains(text, "FastAPI") || strings.Contains(text, "fastapi") {
			found = true
		}
		if why := staticReason(text, ".py"); why != "" && !strings.HasSuffix(rel, "__init__.py") {
			issues = append(issues, CompletenessIssue{Code: "placeholder", Path: rel, Reason: why})
		}
	}
	if !found {
		issues = append(issues, CompletenessIssue{
			Code: "missing_graph", Reason: "no FastAPI() app instance found",
		})
	}
	if !dirExists(filepath.Join(root, "tests")) && !hasPythonTests(root) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_tests", Path: "tests/", Reason: "missing pytest coverage",
		})
	}
	return dedupeIssues(issues)
}

func checkPythonCLI(root string) []CompletenessIssue {
	var issues []CompletenessIssue
	main := filepath.Join(root, "main.py")
	if !fileExists(main) {
		// also accept hello.py / cli.py
		if !fileExists(filepath.Join(root, "cli.py")) && !hasAnyCLI(root) {
			issues = append(issues, CompletenessIssue{
				Code: "missing_file", Path: "main.py",
				Reason: "CLI entrypoint missing",
			})
		}
	} else {
		text := readFile(main)
		if why := staticReason(text, ".py"); why != "" {
			issues = append(issues, CompletenessIssue{Code: "placeholder", Path: "main.py", Reason: why})
		}
	}
	return dedupeIssues(issues)
}

func checkGenericPythonScaffold(root, query string) []CompletenessIssue {
	if !strings.Contains(query, "scaffold") && !strings.Contains(query, "template") &&
		!strings.Contains(query, "project") && !strings.Contains(query, "setup") {
		return nil
	}
	var issues []CompletenessIssue
	if hasPythonSources(root) && !fileExists(filepath.Join(root, "requirements.txt")) &&
		!fileExists(filepath.Join(root, "pyproject.toml")) {
		issues = append(issues, CompletenessIssue{
			Code: "missing_file", Path: "requirements.txt",
			Reason: "Python scaffold should declare dependencies",
		})
	}
	issues = append(issues, emptyPackageIssues(root)...)
	for _, rel := range listPythonSources(root) {
		if strings.HasSuffix(rel, "__init__.py") {
			continue
		}
		text := readFile(filepath.Join(root, rel))
		if why := staticReason(text, ".py"); why != "" {
			issues = append(issues, CompletenessIssue{Code: "placeholder", Path: rel, Reason: why})
		}
	}
	return dedupeIssues(issues)
}

func emptyPackageIssues(root string) []CompletenessIssue {
	var issues []CompletenessIssue
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		name := d.Name()
		switch name {
		case ".git", ".slmcode", "__pycache__", "node_modules", ".venv", "venv",
			"dist", "build", ".tox", "tests":
			if name != "tests" {
				return filepath.SkipDir
			}
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		hasInit := false
		substance := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if n == "__init__.py" {
				hasInit = true
				data, _ := os.ReadFile(filepath.Join(path, n))
				if len(strings.TrimSpace(string(data))) > 0 {
					substance++
				}
				continue
			}
			if strings.HasSuffix(n, ".py") {
				data, _ := os.ReadFile(filepath.Join(path, n))
				body := strings.TrimSpace(string(data))
				if len(body) > 40 && !rePlaceholder.MatchString(body) {
					substance++
				}
			}
		}
		if hasInit && substance == 0 {
			rel, _ := filepath.Rel(root, path)
			if rel == "." || rel == "" {
				return nil
			}
			// Only flag package dirs under src/ or named agent packages.
			rl := strings.ToLower(rel)
			if strings.HasPrefix(rl, "src"+string(filepath.Separator)) ||
				strings.Contains(rl, "agent") || strings.Contains(rl, "lg_") {
				issues = append(issues, CompletenessIssue{
					Code: "empty_package", Path: rel,
					Reason: "package has only empty __init__.py — reference ships real modules",
				})
			}
		}
		return nil
	})
	return issues
}

func isPythonCLIQuery(q string) bool {
	return (strings.Contains(q, "cli") || strings.Contains(q, "argparse") ||
		strings.Contains(q, "command line")) &&
		(strings.Contains(q, "python") || strings.Contains(q, "main.py") ||
			strings.Contains(q, "hello"))
}

func hasAnyCLI(root string) bool {
	for _, name := range []string{"hello.py", "app.py", "cli.py"} {
		if fileExists(filepath.Join(root, name)) {
			return true
		}
	}
	return false
}

func listPythonSources(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".slmcode", "__pycache__", "node_modules", ".venv", "venv", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".py") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	return out
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func dedupeIssues(in []CompletenessIssue) []CompletenessIssue {
	seen := map[string]bool{}
	var out []CompletenessIssue
	for _, is := range in {
		key := is.Code + "|" + is.Path + "|" + is.Reason
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, is)
	}
	return out
}
