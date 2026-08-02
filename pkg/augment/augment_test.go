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
