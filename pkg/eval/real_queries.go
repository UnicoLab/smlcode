package eval

import (
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/augment"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
)

// RealQuery is a production-shaped user request with an expert reference bar.
// Offline checks compare harness planning + workspace quality to that bar.
type RealQuery struct {
	ID          string
	Query       string
	Description string // what a senior engineer would ship
	// HarnessMustContain substrings that must appear across sanitized task board.
	HarnessMustContain []string
	// KnowledgeTopic expected from augment.SelectKnowledge.
	KnowledgeTopic string
	// ExpectPRDAcceptance substrings required in default/enriched acceptance.
	ExpectPRDAcceptance []string
	// Live case fields (optional RUN_E2E).
	ExpectFiles  []string
	ExpectSubstr map[string]string
	Timeout      time.Duration
}

// RealQueries returns the regression suite of real user queries.
func RealQueries() []RealQuery {
	return []RealQuery{
		{
			ID: "langgraph-class-template",
			Query: "I want you to setup a template folder structure for langgraph agent " +
				"using class approach and all the langchain abstractions to have " +
				"scalable and maintainable code.",
			Description: "Class-based LangGraph agent (StateGraph), langchain prompts/tools/memory, " +
				"requirements.txt, runnable main.py, pytest smoke — zero placeholders.",
			HarnessMustContain: []string{
				"requirements.txt", "main.py", "tests/", "langgraph",
			},
			KnowledgeTopic: "LangGraph Class Agent",
			ExpectPRDAcceptance: []string{
				"StateGraph", "pytest", "main.py",
			},
			ExpectFiles: []string{"requirements.txt", "main.py", "tests/test_smoke.py"},
			ExpectSubstr: map[string]string{
				"main.py": "StateGraph",
			},
			Timeout: 20 * time.Minute,
		},
		{
			ID: "fastapi-minimal",
			Query: "Create a minimal FastAPI project with main.py exposing GET /health " +
				"returning {\"status\":\"ok\"}, requirements.txt, and a pytest using TestClient.",
			Description: "Working FastAPI app + health route + TestClient test + deps.",
			HarnessMustContain: []string{
				"requirements.txt", "main.py", "tests/",
			},
			ExpectPRDAcceptance: []string{"main.py"},
			ExpectFiles:         []string{"main.py", "requirements.txt"},
			ExpectSubstr: map[string]string{
				"main.py": "FastAPI",
			},
			Timeout: 12 * time.Minute,
		},
		{
			ID: "python-cli-hello",
			Query: "Create a tiny Python CLI in main.py that prints hello and supports --help. " +
				"Keep it minimal with a pytest smoke test.",
			Description: "argparse CLI (stdlib --help), main.py, tests/test_smoke.py.",
			HarnessMustContain: []string{
				"main.py", "tests/",
			},
			ExpectFiles: []string{"main.py"},
			Timeout:     10 * time.Minute,
		},
		{
			ID:          "static-web-battleship",
			Query:       "Generate an HTML + JavaScript battleship game that works in the browser.",
			Description: "Playable vanilla HTML/CSS/JS game with an index.html entrypoint.",
			// The harness must inject an index.html entrypoint even when a weak
			// splitter only emitted .js assets (regression: "pile of .js, no HTML").
			HarnessMustContain: []string{"index.html"},
			ExpectFiles:        []string{"index.html"},
			ExpectSubstr:       map[string]string{"index.html": "<html"},
			Timeout:            12 * time.Minute,
		},
	}
}

// HarnessPlanResult is the offline planning-quality score for one real query.
type HarnessPlanResult struct {
	ID           string   `json:"id"`
	OK           bool     `json:"ok"`
	Gaps         []string `json:"gaps,omitempty"`
	TaskCount    int      `json:"task_count"`
	HasTester    bool     `json:"has_tester"`
	KnowledgeOK  bool     `json:"knowledge_ok"`
	AcceptanceOK bool     `json:"acceptance_ok"`
}

// EvaluateHarnessPlan checks sanitize/PRD/knowledge against the reference bar
// without calling an LLM. Weak splitter output is used as the starting board.
func EvaluateHarnessPlan(rq RealQuery) HarnessPlanResult {
	res := HarnessPlanResult{ID: rq.ID}
	weak := weakSplitterTasks(rq)
	tasks := plan.SanitizeTasks(weak, "", rq.Query)
	res.TaskCount = len(tasks)

	blob := strings.Builder{}
	for _, t := range tasks {
		blob.WriteString(t.Title + " " + t.Description + " " + t.Acceptance + " " +
			strings.Join(t.Files, " ") + " " + t.Role + "\n")
		if plan.IsTesterRole(t.Role) {
			res.HasTester = true
		}
	}
	boardText := strings.ToLower(blob.String())
	for _, need := range rq.HarnessMustContain {
		if !strings.Contains(boardText, strings.ToLower(need)) {
			res.Gaps = append(res.Gaps, "harness missing: "+need)
		}
	}
	if !res.HasTester {
		res.Gaps = append(res.Gaps, "missing tester task")
	}

	if rq.KnowledgeTopic != "" {
		ks := augment.SelectKnowledge(rq.Query, augment.DefaultKnowledge(), 400)
		for _, k := range ks {
			if k.Topic == rq.KnowledgeTopic {
				res.KnowledgeOK = true
				break
			}
		}
		if !res.KnowledgeOK {
			res.Gaps = append(res.Gaps, "knowledge card missing: "+rq.KnowledgeTopic)
		}
	} else {
		res.KnowledgeOK = true
	}

	prd := plan.ScopePRD{}
	if strings.Contains(strings.ToLower(rq.Query), "langgraph") ||
		strings.Contains(strings.ToLower(rq.Query), "langchain") {
		prd = plan.ScopePRD{
			Summary:    "LangGraph class-agent template",
			Language:   "python",
			Entrypoint: "main.py",
		}
	}
	enriched := plan.EnsureTaskPRDs(tasks, prd, rq.Query)
	acBlob := strings.Builder{}
	for _, t := range enriched {
		acBlob.WriteString(t.Acceptance + "\n")
		for _, c := range t.Checklist {
			acBlob.WriteString(c.Text + "\n")
		}
	}
	// Also fold default acceptance from query.
	for _, a := range defaultAC(rq.Query, prd) {
		acBlob.WriteString(a + "\n")
	}
	acText := strings.ToLower(acBlob.String())
	res.AcceptanceOK = true
	for _, need := range rq.ExpectPRDAcceptance {
		if !strings.Contains(acText, strings.ToLower(need)) {
			res.AcceptanceOK = false
			res.Gaps = append(res.Gaps, "acceptance missing: "+need)
		}
	}

	res.OK = len(res.Gaps) == 0
	return res
}

func defaultAC(query string, prd plan.ScopePRD) []string {
	// Mirror plan.defaultAcceptanceFromQuery via EnsureTaskPRDs side-effect:
	// empty PRD acceptance → defaults injected into tasks. Also call through
	// a throwaway enrich when PRD has no acceptance.
	tasks := []plan.Task{{
		ID: "Tprobe", Title: "Implement", Role: plan.RoleWorker,
		Description: "implement the request with real working code",
		Files:       []string{"main.py"},
	}}
	out := plan.EnsureTaskPRDs(tasks, prd, query)
	var ac []string
	for _, t := range out {
		if t.Acceptance != "" {
			ac = append(ac, t.Acceptance)
		}
		for _, c := range t.Checklist {
			ac = append(ac, c.Text)
		}
	}
	return ac
}

// EvaluateWorkspaceAgainstReference runs project completeness + static gates
// on a workspace for the real query (offline).
func EvaluateWorkspaceAgainstReference(root string, rq RealQuery) []quality.CompletenessIssue {
	return quality.CheckProjectCompleteness(root, rq.Query)
}

// weakSplitterTasks mimics the thin/bad splitter board from the TestSLMs failure
// (or a minimal board for other queries) so harness injection is actually tested.
func weakSplitterTasks(rq RealQuery) []plan.Task {
	switch rq.ID {
	case "langgraph-class-template":
		return []plan.Task{
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
	case "fastapi-minimal":
		return []plan.Task{
			{ID: "T1", Title: "Create FastAPI app", Role: plan.RoleWorker,
				Description: "Create main.py with FastAPI app",
				Files:       []string{"main.py"},
				Acceptance:  "main.py exists"},
		}
	case "static-web-battleship":
		// The exact failure mode: a splitter that emitted .js files but no HTML.
		return []plan.Task{
			{ID: "T1", Title: "Create game logic", Role: plan.RoleWorker,
				Description: "Create the game logic",
				Files:       []string{"game.js"},
				Acceptance:  "game.js exists"},
			{ID: "T2", Title: "Create board renderer", Role: plan.RoleWorker,
				Description: "Create the board renderer",
				Files:       []string{"board.js"},
				Acceptance:  "board.js exists"},
		}
	default:
		return []plan.Task{
			{ID: "T1", Title: "Implement request", Role: plan.RoleWorker,
				Description: "Create the requested Python files",
				Files:       []string{"main.py"},
				Acceptance:  "done"},
		}
	}
}

// RealQueryCases maps RealQueries into live eval.Case values.
func RealQueryCases() []Case {
	var out []Case
	for _, rq := range RealQueries() {
		out = append(out, Case{
			ID:           rq.ID,
			Query:        rq.Query,
			ExpectFiles:  rq.ExpectFiles,
			ExpectSubstr: rq.ExpectSubstr,
			Timeout:      rq.Timeout,
		})
	}
	return out
}
