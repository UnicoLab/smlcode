package quality

import (
	"os"
	"path/filepath"
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
