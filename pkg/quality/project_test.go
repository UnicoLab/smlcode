package quality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckProjectCompletenessLangGraphGarbage(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "src/lg_agent/agents"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "src/lg_agent/__init__.py"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(root, "src/lg_agent/agents/__init__.py"), []byte(""), 0o644)
	garbage := "from langgraph import Graph\n\nclass BaseAgent:\n" +
		"    def run(self, x):\n        # Placeholder implementation\n" +
		"        return {\"output\": \"run_result\"}\n"
	_ = os.WriteFile(filepath.Join(root, "src/lg_agent/agents/agent.py"), []byte(garbage), 0o644)

	q := "setup a template folder structure for langgraph agent using class approach"
	issues := CheckProjectCompleteness(root, q)
	if len(issues) < 3 {
		t.Fatalf("expected many gaps for garbage fixture, got %#v", issues)
	}
	codes := map[string]bool{}
	for _, is := range issues {
		codes[is.Code] = true
	}
	for _, need := range []string{"missing_file", "placeholder", "bad_import", "missing_tests"} {
		if !codes[need] {
			t.Fatalf("expected code %s in %#v", need, issues)
		}
	}
}

func TestCheckProjectCompletenessLangGraphReference(t *testing.T) {
	root := t.TempDir()
	// Minimal reference that should pass (mirrors eval fixture shape).
	files := map[string]string{
		"requirements.txt": "langgraph>=0.2\nlangchain-core>=0.2\npytest>=8.0\n",
		"main.py": "from langgraph.graph import StateGraph, END\n" +
			"from typing import TypedDict, Any\n\n" +
			"class S(TypedDict):\n    x: str\n\n" +
			"class EchoAgent:\n" +
			"    def build_graph(self):\n" +
			"        g = StateGraph(S)\n" +
			"        g.add_node('n', lambda s: s)\n" +
			"        g.set_entry_point('n')\n" +
			"        g.add_edge('n', END)\n" +
			"        return g\n" +
			"    def invoke(self, inputs):\n" +
			"        return self.build_graph().compile().invoke(inputs)\n\n" +
			"if __name__ == '__main__':\n" +
			"    print(EchoAgent().invoke({'x': 'hi'}))\n",
		"src/lg_agent/__init__.py":        "\"\"\"pkg\"\"\"\n",
		"src/lg_agent/agents/__init__.py": "from .base import EchoAgent\n",
		"src/lg_agent/agents/base.py": "from langgraph.graph import StateGraph, END\n" +
			"from typing import TypedDict, Any\n\n" +
			"class S(TypedDict):\n    x: str\n\n" +
			"class EchoAgent:\n" +
			"    def build_graph(self):\n" +
			"        g = StateGraph(S)\n" +
			"        g.add_node('n', lambda s: {'x': s.get('x','')+'!'})\n" +
			"        g.set_entry_point('n')\n" +
			"        g.add_edge('n', END)\n" +
			"        return g\n" +
			"    def invoke(self, inputs: dict[str, Any]):\n" +
			"        return self.build_graph().compile().invoke(inputs)\n",
		"tests/test_smoke.py": "def test_ok():\n    assert True\n",
	}
	for rel, body := range files {
		abs := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(abs), 0o755)
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	q := "setup langgraph agent class template with langchain abstractions"
	issues := CheckProjectCompleteness(root, q)
	if len(issues) != 0 {
		t.Fatalf("reference scaffold must pass, got %#v", issues)
	}
}

func TestCheckProjectCompletenessRoutesByLanguage(t *testing.T) {
	write := func(t *testing.T, root, rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	codes := func(issues []CompletenessIssue) map[string]bool {
		out := map[string]bool{}
		for _, is := range issues {
			out[is.Code] = true
		}
		return out
	}

	t.Run("go project with no tests", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module x\n\ngo 1.23\n")
		write(t, root, "calc.go", "package calc\n\nfunc Sum(a, b int) int { return a + b }\n")
		got := codes(CheckProjectCompleteness(root, "add a Sum function with tests"))
		if !got["missing_tests"] {
			t.Error("a Go project asked for tests with no _test.go should be flagged")
		}
	})

	t.Run("go project with a panic TODO stub", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module x\n")
		write(t, root, "calc.go", "package calc\n\nfunc Sum(a, b int) int { panic(\"TODO\") }\n")
		write(t, root, "calc_test.go", "package calc\n")
		got := codes(CheckProjectCompleteness(root, "implement Sum"))
		if !got["placeholder"] {
			t.Error("panic(\"TODO\") should be reported as a placeholder")
		}
	})

	t.Run("go project is not judged against a python bar", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "go.mod", "module x\n")
		write(t, root, "main.go", "package main\n\nfunc main() { println(\"hi\") }\n")
		for _, is := range CheckProjectCompleteness(root, "scaffold a project") {
			if strings.Contains(is.Reason, "requirements.txt") || is.Path == "requirements.txt" {
				t.Errorf("python bar leaked into a Go workspace: %+v", is)
			}
		}
	})

	t.Run("node project without a test script", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "package.json", `{"name":"x","scripts":{"build":"tsc"}}`)
		write(t, root, "src/index.ts", "export const x = 1\n")
		got := codes(CheckProjectCompleteness(root, "add unit tests for the parser"))
		if !got["missing_file"] || !got["missing_tests"] {
			t.Errorf("expected a missing test script and missing test files, got %v", got)
		}
	})

	t.Run("rust project with todo macro", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "Cargo.toml", "[package]\nname = \"x\"\n")
		write(t, root, "src/lib.rs", "pub fn sum(a: i32, b: i32) -> i32 { todo!() }\n")
		got := codes(CheckProjectCompleteness(root, "implement sum with tests"))
		if !got["placeholder"] || !got["missing_tests"] {
			t.Errorf("expected placeholder + missing_tests, got %v", got)
		}
	})

	t.Run("unknown workspace gets no bar", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "notes.txt", "hello")
		if issues := CheckProjectCompleteness(root, "scaffold a project"); len(issues) != 0 {
			t.Errorf("expected no issues for a non-code workspace, got %+v", issues)
		}
	})
}
