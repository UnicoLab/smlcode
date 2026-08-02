package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/augment"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

const langGraphTemplateQuery = "I want you to setup a template folder structure for langgraph agent " +
	"using class approach and all the langchain abstractions to have scalable and maintainable code."

// TestLangGraphTemplateQueryPath is the harness regression for the query that
// previously produced empty packages + Placeholder stubs + false success.
func TestLangGraphTemplateQueryPath(t *testing.T) {
	// Typical weak splitter output from the failed TestSLMs run.
	tasks := []plan.Task{
		{ID: "T1", Title: "Create base directory structure", Role: plan.RoleWorker,
			Description: "Create packages with __init__.py",
			Files:       []string{"src/lg_agent/__init__.py", "src/lg_agent/agents/__init__.py"},
			Acceptance:  "All directories exist with __init__.py files"},
		{ID: "T2", Title: "Implement main LangGraph agent class", Role: plan.RoleWorker,
			Description: "Create agents/agent.py with a base LangGraph agent class",
			Files:       []string{"src/lg_agent/agents/agent.py"},
			Acceptance:  "agents/agent.py exists and contains a valid LangGraph agent class"},
		{ID: "T3", Title: "Setup LangChain abstractions", Role: plan.RoleWorker,
			Description: "Create chains, prompts, memory modules",
			Files: []string{
				"src/lg_agent/chains/chain_factory.py",
				"src/lg_agent/prompts/templates.py",
				"src/lg_agent/memory/memory_manager.py",
			},
			Acceptance: "All three files exist and contain valid LangChain abstractions"},
	}

	out := plan.SanitizeTasks(tasks, "", langGraphTemplateQuery)
	hasReq, hasMain, hasTestFile, hasTester := false, false, false, false
	for _, tsk := range out {
		blob := strings.ToLower(tsk.Title + " " + strings.Join(tsk.Files, " ") + " " + tsk.Role)
		if strings.Contains(blob, "requirements.txt") {
			hasReq = true
		}
		if strings.Contains(blob, "main.py") {
			hasMain = true
		}
		if strings.Contains(blob, "test_smoke") || strings.Contains(blob, "tests/") {
			hasTestFile = true
		}
		if tsk.Role == plan.RoleTester {
			hasTester = true
		}
	}
	if !hasReq || !hasMain || !hasTestFile || !hasTester {
		t.Fatalf("harness incomplete req=%v main=%v tests=%v tester=%v → %+v",
			hasReq, hasMain, hasTestFile, hasTester, out)
	}

	prd := plan.ScopePRD{
		Summary:    "LangGraph class-agent template",
		Language:   "python",
		Entrypoint: "main.py",
		Acceptance: []string{
			"Class-based LangGraph agent uses langgraph.graph.StateGraph",
			"main.py invokes the compiled graph once and exits 0",
			"python -m pytest -q passes; no Placeholder stubs",
			"requirements.txt lists langgraph + langchain-core + pytest",
		},
	}
	enriched := plan.EnsureTaskPRDs(out, prd, langGraphTemplateQuery)
	for _, tsk := range enriched {
		if tsk.Role != plan.RoleWorker && tsk.Role != "deep" {
			continue
		}
		ac := strings.ToLower(tsk.Acceptance)
		if ac == "done" || strings.Contains(ac, "step completed with tool evidence") ||
			(strings.Contains(ac, "exist") && strings.Contains(ac, "contain") &&
				!strings.Contains(ac, "pytest") && !strings.Contains(ac, "stategraph") &&
				!strings.Contains(ac, "invoke") && !strings.Contains(ac, "runs")) {
			// Existence-only leftovers after enrich are still too weak for implement tasks.
			if strings.Contains(strings.ToLower(tsk.Title+tsk.Description), "implement") ||
				strings.Contains(strings.ToLower(tsk.Title+tsk.Description), "langgraph") {
				if !strings.Contains(ac, "meets:") && !strings.Contains(ac, "pytest") &&
					!strings.Contains(ac, "stategraph") && !strings.Contains(ac, "invoke") {
					t.Fatalf("%s weak acceptance after PRD enrich: %q", tsk.ID, tsk.Acceptance)
				}
			}
		}
	}

	ks := augment.SelectKnowledge(langGraphTemplateQuery, augment.DefaultKnowledge(), 400)
	foundLG := false
	for _, k := range ks {
		if k.Topic == "LangGraph Class Agent" {
			foundLG = true
		}
	}
	if !foundLG {
		t.Fatal("expected LangGraph Class Agent knowledge injection")
	}

	root := t.TempDir()
	agentPath := filepath.Join(root, "src", "lg_agent", "agents")
	if err := os.MkdirAll(agentPath, 0o755); err != nil {
		t.Fatal(err)
	}
	garbage := "from langgraph import Graph\nfrom typing import Any, Dict\n\n" +
		"class BaseAgent:\n" +
		"    def run(self, input_data: Dict[str, Any]) -> Dict[str, Any]:\n" +
		"        # Placeholder implementation\n" +
		"        return {\"output\": \"run_result\"}\n"
	rel := "src/lg_agent/agents/agent.py"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(garbage), 0o644); err != nil {
		t.Fatal(err)
	}
	issues := quality.CheckStaticQuality(root, plan.Task{Files: []string{rel}})
	if len(issues) == 0 {
		t.Fatal("TestSLMs-style agent.py must fail static quality")
	}
	if !quality.IsWeakQACommand("python -m compileall -q .") {
		t.Fatal("compileall must be classified as weak QA")
	}
}
