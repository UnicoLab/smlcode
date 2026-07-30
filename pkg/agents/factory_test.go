package agents

import (
	"strings"
	"testing"
)

func TestSpecsRoster(t *testing.T) {
	specs := Specs()
	if len(specs) < 12 {
		t.Fatalf("expected rich roster, got %d", len(specs))
	}
	want := map[string]bool{
		"coordinator": true, "worker": true, "reviewer": true,
		"corrector": true, "tester": true, "explorer": true, "deep": true,
	}
	for _, s := range specs {
		delete(want, s.ID)
		if strings.TrimSpace(s.SystemPrompt) == "" {
			t.Fatalf("empty prompt for %s", s.ID)
		}
		if s.MaxIter <= 0 {
			t.Fatalf("max_iter for %s", s.ID)
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing specs: %v", want)
	}
}

func TestPublicSpecs(t *testing.T) {
	pub := PublicSpecs()
	if len(pub) != len(Specs()) {
		t.Fatalf("public=%d specs=%d", len(pub), len(Specs()))
	}
	for _, m := range pub {
		if _, ok := m["id"]; !ok {
			t.Fatal("missing id")
		}
		if _, ok := m["system"]; ok {
			t.Fatal("public specs must not leak system prompts")
		}
	}
}

func TestPublicSpecsMergesBuiltinOverride(t *testing.T) {
	dir := t.TempDir()
	_, err := WriteCustom(dir, CustomSpec{
		ID: "worker", Provider: "ollama", Model: "qwen2.5-coder:7b", MaxIter: 22,
	})
	if err != nil {
		t.Fatal(err)
	}
	custom, err := LoadCustomSpecs(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub := PublicSpecsWithCustom(custom)
	found := false
	for _, m := range pub {
		if m["id"] == "worker" {
			found = true
			if m["override"] != true {
				t.Fatal("expected override flag")
			}
			if m["provider"] != "ollama" || m["model"] != "qwen2.5-coder:7b" {
				t.Fatalf("%v", m)
			}
			if m["max_iter"] != 22 {
				t.Fatalf("max_iter=%v", m["max_iter"])
			}
		}
	}
	if !found {
		t.Fatal("worker missing")
	}
}

func TestWorkerPromptAntiWander(t *testing.T) {
	if !strings.Contains(PromptWorker, "HARD SCOPE") {
		t.Fatal("worker prompt missing HARD SCOPE")
	}
	if !strings.Contains(PromptWorker, "main.go") {
		t.Fatal("worker prompt should mention main.go ban")
	}
	if !strings.Contains(PromptWorker, "ANTI-WANDER") {
		t.Fatal("worker prompt missing ANTI-WANDER")
	}
	if !strings.Contains(PromptReviewer, "out of focus") && !strings.Contains(PromptReviewer, "outside focus") {
		t.Fatal("reviewer should reject out-of-focus paths")
	}
	if !strings.Contains(PromptReviewer, "Disk evidence") {
		t.Fatal("reviewer should trust Disk evidence")
	}
}
