// Package augment ports little-coder's per-turn skill/knowledge injection.
//
// Guidance is appended at the *tail* of the worker prompt (not the system
// prompt) so the cached prefix stays intact — the KV-cache lesson from
// little-coder #73.
package augment

import (
	"fmt"
	"strings"
)

// ToolSkill is a short usage card for one workspace tool.
type ToolSkill struct {
	Target    string
	Body      string
	TokenCost int
}

// KnowledgeEntry is a scored domain cheat sheet.
type KnowledgeEntry struct {
	Topic         string
	Body          string
	TokenCost     int
	Keywords      []string
	RequiresTools []string
}

var intentMap = map[string][]string{
	"read": {"ws_read"}, "show": {"ws_read"}, "view": {"ws_read"},
	"write": {"ws_write"}, "create": {"ws_write", "ws_shell"},
	"implement": {"ws_write", "ws_read"}, "code": {"ws_write", "ws_read"},
	"edit": {"ws_edit"}, "change": {"ws_edit"}, "modify": {"ws_edit"},
	"fix": {"ws_edit"}, "update": {"ws_edit"}, "replace": {"ws_edit"},
	"add": {"ws_edit", "ws_write"}, "refactor": {"ws_edit", "ws_read"},
	"patch": {"ws_patch", "ws_edit"}, "diff": {"ws_patch"},
	"run": {"ws_shell"}, "execute": {"ws_shell"}, "install": {"ws_shell"},
	"build": {"ws_shell"}, "test": {"ws_shell"},
	"find": {"ws_glob", "ws_grep"}, "search": {"ws_grep"},
	"grep": {"ws_grep"}, "glob": {"ws_glob"},
	"rename": {"ws_mv", "ws_edit"}, "move": {"ws_mv"},
}

// DefaultToolSkills returns little-coder-style cards adapted to ws_* tools.
func DefaultToolSkills() []ToolSkill {
	return []ToolSkill{
		{
			Target: "ws_write", TokenCost: 110,
			Body: `Create a **new** file only. Creates parent dirs automatically.
REQUIRED: path (relative), content.
**Refused if the file already exists** — use ws_edit/ws_patch instead; do not retry ws_write.
WHEN TO USE: brand-new file from scratch.
WHEN NOT TO: any change to an existing file (bugfix, add function, rename symbol).`,
		},
		{
			Target: "ws_edit", TokenCost: 140,
			Body: `Default tool for changing any existing file. Exact old_str → new_str.
RULES: old_str must match EXACTLY (whitespace matters); must be unique (or replace_all:true);
file must be ws_read first this session.
RECOVERY: "not found" → ws_read, copy exact text, retry. Never fall back to ws_write.`,
		},
		{
			Target: "ws_patch", TokenCost: 120,
			Body: `Prefer for multi-hunk SLM edits. Unified diff or SEARCH/REPLACE blocks.
Existing files must be ws_read first. On failure: re-read, fix SEARCH text, retry — never rewrite whole file with ws_write.`,
		},
		{
			Target: "ws_read", TokenCost: 80,
			Body: `Read file contents before editing. Do this once per file at the start of a change;
do not re-read every turn unless the file changed. Absolute precision of old_str depends on a fresh read.`,
		},
		{
			Target: "ws_shell", TokenCost: 100,
			Body: `Run tests/builds/smoke checks. Do NOT use shell redirects (cat >, tee) to write source files —
those are refused when the file exists. Use ws_edit/ws_patch for code changes.
Prefer: python -m py_compile PATH · go test ./… -short · pytest.`,
		},
		{
			Target: "ws_glob", TokenCost: 60,
			Body: `Find files by pattern (e.g. **/*.go, **/test_*.py). Use before guessing paths.`,
		},
		{
			Target: "ws_grep", TokenCost: 70,
			Body: `Search file contents. Combine with ws_glob to locate symbols before editing.`,
		},
		{
			Target: "ws_mv", TokenCost: 70,
			Body: `Rename/move files (git mv when available). Prefer over rewrite+delete for renames.`,
		},
	}
}

// DefaultKnowledge returns scored domain cards (workspace docs + quality + algorithms).
func DefaultKnowledge() []KnowledgeEntry {
	base := []KnowledgeEntry{
		{
			Topic: "Workspace Documentation", TokenCost: 140,
			Keywords: []string{
				"implement", "build", "create", "fix", "feature", "spec",
				"requirements", "bug", "test", "failing", "refactor", "exercise",
			},
			RequiresTools: []string{"ws_read", "ws_glob"},
			Body: `Before writing code for a non-trivial task, surface local docs ONCE:
AGENTS.md / CLAUDE.md / README.md / SPEC.md / .docs/instructions.md / docs/*.md.
Use ws_glob then ws_read. Specs often contain exact format rules the tests assert.
Skip for pure read-only questions.`,
		},
		{
			Topic: "Task Decomposition", TokenCost: 120,
			Keywords: []string{
				"multi", "several", "refactor", "migrate", "implement", "architecture",
				"complex", "multiple files", "plan",
			},
			RequiresTools: []string{"ws_read"},
			Body: `GIVEN / UNKNOWN (≤2) / PLAN before tools. Resolve one unknown fully before the next.
Commit to one approach. Prefer tiny ws_edit/ws_patch. One atomic change → smoke → next.`,
		},
		{
			Topic: "Edit Recovery Loop", TokenCost: 100,
			Keywords:      []string{"edit", "patch", "fix", "replace", "old_str", "failed", "error"},
			RequiresTools: []string{"ws_read", "ws_edit"},
			Body: `When ws_edit/ws_patch fails: (1) ws_read the file, (2) copy exact numbered text into
old_str/SEARCH, (3) retry. Never escalate to ws_write on an existing file.
Include 2–3 lines of context so the match is unique.`,
		},
		{
			Topic: "Minimal Diff Discipline", TokenCost: 110,
			Keywords: []string{
				"fix", "bug", "patch", "change", "update", "modify", "refactor", "typo",
			},
			RequiresTools: []string{"ws_edit", "ws_read"},
			Body: `Change the smallest span that meets acceptance. No drive-by refactors, no new
helpers/files unless required, no comment/docstring churn. Match existing style
exactly (quotes, indentation, naming). Giant LLMs often over-edit — do the opposite.`,
		},
		{
			Topic: "Verify Before Done", TokenCost: 120,
			Keywords: []string{
				"implement", "create", "build", "fix", "test", "feature", "function", "class",
			},
			RequiresTools: []string{"ws_shell"},
			Body: `Before status=done: run real verification via ws_shell
(python -m py_compile PATH / go test ./pkg -short / node --check PATH / pytest).
Never claim done on stubs (pass / … / NotImplemented / TODO). Reading files ≠ proof.`,
		},
		{
			Topic: "No Hallucinated APIs", TokenCost: 100,
			Keywords: []string{
				"api", "import", "library", "framework", "sdk", "client", "endpoint", "function",
			},
			RequiresTools: []string{"ws_grep", "ws_read"},
			Body: `Only use symbols/imports you have seen via ws_read/ws_grep in THIS repo (or the
standard library of the declared language). Do not invent helpers, config keys,
or CLI flags. If unsure, grep first.`,
		},
		{
			Topic: "Greenfield Scaffold", TokenCost: 110,
			Keywords: []string{
				"create", "scaffold", "new project", "greenfield", "from scratch", "mvp", "cli",
				"setup", "template", "folder structure", "boilerplate",
			},
			RequiresTools: []string{"ws_write", "ws_shell"},
			Body: `Create only files listed in the task. Prefer a single entrypoint + minimal deps.
Add a tiny smoke path (pytest or py_compile / go test). No leftover placeholders.
After writes: install deps if needed, run smoke, then status=done.`,
		},
		{
			Topic: "LangGraph Class Agent", TokenCost: 140,
			Keywords: []string{
				"langgraph", "langchain", "stategraph", "class approach", "class-based",
				"agent template", "graph agent",
			},
			RequiresTools: []string{"ws_write", "ws_shell"},
			Body: `Expert bar for LangGraph class-agent templates (match or beat this):
- from langgraph.graph import StateGraph, END (NOT "from langgraph import Graph")
- state.py TypedDict; agents/base.py class with build_graph()→compile()→invoke()
- Real packages with substance (not empty __init__.py only): agents/, chains/, prompts/, memory/, tools/, config/
- LangChain: ChatPromptTemplate / Runnable chain + @tool registry
- Ship main.py demo invoke + tests/test_smoke.py + requirements.txt (langgraph, langchain-core, pytest)
- ZERO stub markers or fake {"output":"run_result"} returns
After writes: pip install -r requirements.txt && python -m pytest -q && python main.py`,
		},
	}
	return append(base, AlgorithmKnowledge()...)
}

// Options controls injection budgets.
// Budget 0 = use default; negative = disable that injector.
type Options struct {
	SkillBudget     int // default 300; <0 disables
	KnowledgeBudget int // default 200; <0 disables
	LastFailedTool  string
	RecentTools     []string // most-recent first
}

// SelectToolSkills picks cards: error recovery > recency > intent.
func SelectToolSkills(prompt string, skills []ToolSkill, opt Options) []ToolSkill {
	budget := opt.SkillBudget
	if budget <= 0 {
		budget = 300
	}
	byName := map[string]ToolSkill{}
	for _, s := range skills {
		byName[s.Target] = s
	}
	var selected []ToolSkill
	used := 0
	tryAdd := func(name string) {
		sk, ok := byName[name]
		if !ok {
			return
		}
		for _, s := range selected {
			if s.Target == name {
				return
			}
		}
		cost := sk.TokenCost
		if cost <= 0 {
			cost = 100
		}
		if used+cost > budget {
			return
		}
		selected = append(selected, sk)
		used += cost
	}
	if opt.LastFailedTool != "" {
		tryAdd(opt.LastFailedTool)
	}
	for _, name := range opt.RecentTools {
		if used >= budget {
			break
		}
		tryAdd(name)
	}
	for _, name := range predictTools(prompt) {
		if used >= budget {
			break
		}
		tryAdd(name)
	}
	// Always include write+edit cards for coding roles when budget allows —
	// this is the load-bearing invariant for SLMs.
	if used < budget {
		tryAdd("ws_write")
		tryAdd("ws_edit")
	}
	return selected
}

// SelectKnowledge scores entries against the prompt (word=1, phrase=2).
func SelectKnowledge(prompt string, entries []KnowledgeEntry, budget int) []KnowledgeEntry {
	if budget <= 0 {
		budget = 200
	}
	type scored struct {
		score float64
		e     KnowledgeEntry
	}
	var list []scored
	for _, e := range entries {
		s := scoreEntry(prompt, e)
		if s >= 2.0 {
			list = append(list, scored{s, e})
		}
	}
	// simple selection sort by score desc
	for i := 0; i < len(list); i++ {
		best := i
		for j := i + 1; j < len(list); j++ {
			if list[j].score > list[best].score {
				best = j
			}
		}
		list[i], list[best] = list[best], list[i]
	}
	var selected []KnowledgeEntry
	used := 0
	for _, item := range list {
		cost := item.e.TokenCost
		if cost <= 0 {
			cost = 100
		}
		if used+cost > budget {
			continue
		}
		selected = append(selected, item.e)
		used += cost
	}
	return selected
}

// RenderBlock builds the tail message for injection.
func RenderBlock(skills []ToolSkill, knowledge []KnowledgeEntry) string {
	if len(skills) == 0 && len(knowledge) == 0 {
		return ""
	}
	var b strings.Builder
	if len(skills) > 0 {
		b.WriteString("\n\n## Tool Usage Guidance\n")
		for _, s := range skills {
			b.WriteString(fmt.Sprintf("\n### %s\n%s\n", s.Target, s.Body))
		}
	}
	if len(knowledge) > 0 {
		b.WriteString("\n\n## Algorithm Reference\n")
		for _, e := range knowledge {
			b.WriteString(fmt.Sprintf("\n### %s\n%s\n", e.Topic, e.Body))
		}
	}
	b.WriteString("\n## Runtime invariants\n")
	b.WriteString("- ws_write refuses existing files — use ws_edit/ws_patch.\n")
	b.WriteString("- ws_edit/ws_patch require a prior ws_read of that file this session.\n")
	b.WriteString("- Shell redirects that overwrite existing files are refused — use tools.\n")
	return b.String()
}

// InjectForPrompt selects and renders guidance for a user/task prompt.
func InjectForPrompt(prompt string, opt Options) string {
	var skills []ToolSkill
	var knowledge []KnowledgeEntry
	if opt.SkillBudget >= 0 {
		if opt.SkillBudget == 0 {
			opt.SkillBudget = 300
		}
		skills = SelectToolSkills(prompt, DefaultToolSkills(), opt)
	}
	if opt.KnowledgeBudget >= 0 {
		kb := opt.KnowledgeBudget
		if kb == 0 {
			kb = 200
		}
		knowledge = SelectKnowledge(prompt, DefaultKnowledge(), kb)
		needed := map[string]bool{}
		for _, e := range knowledge {
			for _, t := range e.RequiresTools {
				needed[t] = true
			}
		}
		if len(needed) > 0 && opt.SkillBudget >= 0 {
			recent := append([]string{}, opt.RecentTools...)
			for t := range needed {
				recent = append(recent, t)
			}
			opt.RecentTools = recent
			skills = SelectToolSkills(prompt, DefaultToolSkills(), opt)
		}
	}
	return RenderBlock(skills, knowledge)
}

func predictTools(userText string) []string {
	words := strings.Fields(strings.ToLower(userText))
	set := map[string]bool{}
	var out []string
	for _, w := range words {
		w = strings.Trim(w, ".,:;!?()[]{}\"'")
		for _, tn := range intentMap[w] {
			if !set[tn] {
				set[tn] = true
				out = append(out, tn)
			}
		}
	}
	return out
}

func scoreEntry(userText string, e KnowledgeEntry) float64 {
	if len(e.Keywords) == 0 {
		return 0
	}
	lower := strings.ToLower(userText)
	words := map[string]bool{}
	for _, w := range strings.Fields(lower) {
		words[strings.Trim(w, ".,:;!?()[]{}\"'")] = true
	}
	var score float64
	for _, kw := range e.Keywords {
		kw = strings.ToLower(kw)
		if strings.Contains(kw, " ") {
			if strings.Contains(lower, kw) {
				score += 2
			}
		} else if words[kw] {
			score += 1
		}
	}
	return score
}
