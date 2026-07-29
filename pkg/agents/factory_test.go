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

func TestWorkerPromptAntiWander(t *testing.T) {
	if !strings.Contains(PromptWorker, "HARD SCOPE") {
		t.Fatal("worker prompt missing HARD SCOPE")
	}
	if !strings.Contains(PromptWorker, "main.go") {
		t.Fatal("worker prompt should mention main.go ban")
	}
	if !strings.Contains(PromptReviewer, "out of focus") && !strings.Contains(PromptReviewer, "outside focus") {
		t.Fatal("reviewer should reject out-of-focus paths")
	}
}
