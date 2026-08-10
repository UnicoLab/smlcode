package augment

import (
	"strings"
	"testing"
)

func TestSelectToolSkillsIntentAndInvariants(t *testing.T) {
	skills := SelectToolSkills("fix the login handler", DefaultToolSkills(), Options{SkillBudget: 400})
	names := map[string]bool{}
	for _, s := range skills {
		names[s.Target] = true
	}
	if !names["ws_edit"] {
		t.Fatalf("expected ws_edit for fix intent, got %#v", skills)
	}
	if !names["ws_write"] {
		t.Fatalf("expected ws_write invariant card, got %#v", skills)
	}
}

func TestSelectToolSkillsErrorRecoveryFirst(t *testing.T) {
	skills := SelectToolSkills("continue", DefaultToolSkills(), Options{
		SkillBudget:    150,
		LastFailedTool: "ws_patch",
	})
	if len(skills) == 0 || skills[0].Target != "ws_patch" {
		t.Fatalf("error recovery should be first, got %#v", skills)
	}
}

func TestSelectKnowledgeWorkspaceDocs(t *testing.T) {
	ks := SelectKnowledge("implement a feature from the spec", DefaultKnowledge(), 200)
	if len(ks) == 0 {
		t.Fatal("expected knowledge hit")
	}
	found := false
	for _, k := range ks {
		if k.Topic == "Workspace Documentation" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected workspace docs, got %#v", ks)
	}
}

func TestInjectForPromptContainsInvariants(t *testing.T) {
	block := InjectForPrompt("refactor the auth package", Options{})
	if !strings.Contains(block, "Tool Usage Guidance") {
		t.Fatal("missing tool guidance")
	}
	if !strings.Contains(block, "ws_write refuses") {
		t.Fatal("missing runtime invariants")
	}
}

func TestAlgorithmKnowledgeBinarySearch(t *testing.T) {
	ks := SelectKnowledge("implement binary search on a sorted monotonic predicate", DefaultKnowledge(), 300)
	found := false
	for _, k := range ks {
		if k.Topic == "Binary Search" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Binary Search card, got %#v", ks)
	}
}

func TestSelectKnowledgeLangGraphClassAgent(t *testing.T) {
	q := "setup a template folder structure for langgraph agent using class approach and langchain abstractions"
	ks := SelectKnowledge(q, DefaultKnowledge(), 400)
	found := false
	for _, k := range ks {
		if k.Topic == "LangGraph Class Agent" {
			found = true
			if !strings.Contains(k.Body, "StateGraph") {
				t.Fatalf("expected StateGraph guidance, got %q", k.Body)
			}
		}
	}
	if !found {
		t.Fatalf("expected LangGraph Class Agent card, got %#v", ks)
	}
}

func TestSelectKnowledgePythonAndGoBars(t *testing.T) {
	py := SelectKnowledge("add pytest coverage for the fastapi python service", DefaultKnowledge(), 400)
	goK := SelectKnowledge("fix go test failures in pkg/loop", DefaultKnowledge(), 400)
	assertTopic := func(ks []KnowledgeEntry, topic string) {
		for _, k := range ks {
			if k.Topic == topic {
				return
			}
		}
		t.Fatalf("expected %s card, got %#v", topic, ks)
	}
	assertTopic(py, "Python Project Bar")
	assertTopic(goK, "Go Project Bar")
}

func TestInjectForPromptLanguageFilterGoProjectDropsPythonCards(t *testing.T) {
	// A generic "test" query in a Go project must never pull python/LangGraph guidance.
	block := InjectForPrompt("fix the flaky test in pkg/loop", Options{Language: "go"})
	if strings.Contains(block, "Python Project Bar") {
		t.Fatal("Go project got Python Project Bar: " + block)
	}
	if strings.Contains(block, "LangGraph Class Agent") {
		t.Fatal("Go project got LangGraph Class Agent: " + block)
	}
	// Neutral cards stay.
	if !strings.Contains(block, "Workspace Documentation") {
		t.Fatal("language-neutral Workspace Documentation card must stay: " + block)
	}
}

func TestInjectForPromptLanguageFilterGoProjectExplicitPythonMentionWins(t *testing.T) {
	block := InjectForPrompt("port the python fastapi service using pytest", Options{Language: "go"})
	if !strings.Contains(block, "Python Project Bar") {
		t.Fatal("explicit python mention must allow Python Project Bar even in Go project: " + block)
	}
}

func TestInjectForPromptLanguageFilterPythonProjectDropsGoBar(t *testing.T) {
	// Bare "go test" mention is incidental — python project still gets python guidance.
	block := InjectForPrompt("add go test coverage", Options{Language: "python"})
	if strings.Contains(block, "Go Project Bar") {
		t.Fatal("Python project got Go Project Bar for incidental 'go test' mention: " + block)
	}
	// Explicit golang mention wins over the project default. "go.mod" is
	// included so the Go Bar scores ≥2 keywords and gets selected — the
	// filter's job is to keep it (not drop it) in a Python project.
	block = InjectForPrompt("migrate the golang module to python keeping go.mod valid", Options{Language: "python"})
	if !strings.Contains(block, "Go Project Bar") {
		t.Fatal("explicit golang mention must allow Go Project Bar in Python project: " + block)
	}
}

func TestInjectForPromptLanguageFilterJSProjectDropsAllLanguageBars(t *testing.T) {
	for _, lang := range []string{"javascript", "typescript"} {
		block := InjectForPrompt("fix the flaky test in src/components", Options{Language: lang})
		for _, banned := range []string{"Python Project Bar", "LangGraph Class Agent", "Go Project Bar"} {
			if strings.Contains(block, banned) {
				t.Fatalf("%s project got %s: %s", lang, banned, block)
			}
		}
	}
}

func TestFilterKnowledgeByLanguageDropsLangGraphInGoProject(t *testing.T) {
	// "class-based graph agent" scores the LangGraph card, but a Go project must not get it.
	hit := SelectKnowledge("build a class-based graph agent template", DefaultKnowledge(), 400)
	found := false
	for _, k := range hit {
		if k.Topic == "LangGraph Class Agent" {
			found = true
		}
	}
	if !found {
		t.Fatal("precondition: prompt must score the LangGraph card without the filter")
	}
	filtered := filterKnowledgeByLanguage(hit, "go", "build a class-based graph agent template")
	for _, k := range filtered {
		if k.Topic == "LangGraph Class Agent" {
			t.Fatal("Go project must not get LangGraph Class Agent even when it scores")
		}
	}
	// Empty language is a no-op.
	if got := filterKnowledgeByLanguage(hit, "", "anything"); len(got) != len(hit) {
		t.Fatalf("empty language must return entries unchanged, got %d != %d", len(got), len(hit))
	}
}

func TestWsShellCardIsLanguageNeutral(t *testing.T) {
	block := InjectForPrompt("run the tests", Options{})
	if strings.Contains(block, "Prefer: python -m py_compile PATH") {
		t.Fatal("ws_shell card must no longer single out python: " + block)
	}
	if !strings.Contains(block, "never mix languages") {
		t.Fatal("ws_shell card must carry the language-neutral guidance: " + block)
	}
}
