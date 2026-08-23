package evolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/memory"
)

func sampleReport() RunReport {
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	return RunReport{
		RunID:     "run_1",
		StartedAt: start,
		EndedAt:   start.Add(90 * time.Second),
		Query:     "add retry with backoff to the HTTP client",
		Language:  "go",
		Model:     "qwen2.5-coder:14b",
		Provider:  "ollama",

		PlannedTasks: 2, CompletedTasks: 2,
		Gates: []GateResult{{Name: "go test", Passed: true}, {Name: "go vet", Passed: true}},
		Failures: []FailureEvent{
			{
				Signal:     Signal{Tool: "ws_edit", Language: "go", Message: "old_str not found in pkg/http/client.go"},
				RuleID:     "rule_seeded",
				ResolvedBy: "rule:rule_seeded",
				Resolution: "re-read then retried with a smaller anchor",
				Attempts:   2,
			},
			{
				Signal:     Signal{Tool: "ws_shell", Language: "go", Message: "./pkg/http/client.go:12:2: undefined: backoffFor"},
				ResolvedBy: "llm",
				Resolution: "added the missing helper",
				Repair: &Repair{
					Kind: RepairAction, Action: ActionRereadFile,
					Guidance: "grep for the symbol before inventing it",
				},
				Recheck: &Check{Kind: CheckFileContains, Path: "pkg/http/client.go", Substring: "func backoffFor"},
			},
			{
				Signal:   Signal{Tool: "ws_shell", Language: "go", Message: "context deadline exceeded"},
				Attempts: 1,
			},
		},
		Decisions: []DecisionRecord{{
			Key:     Key{Decision: DecEditFormat, ModelFamily: "qwen2.5-coder", Language: "go"},
			Arm:     "search_replace",
			Outcome: Outcome{Applied: true, GateRan: true, GatePassed: true},
		}},
		Retries: 2, LLMCalls: 11, TokensIn: 41000, TokensOut: 3100,
		ToolCalls: 24, ToolErrors: 3, RedundantCalls: 1,
		FilesChanged: []string{"pkg/http/client.go", "pkg/http/client_test.go"},
		ToolsUsed:    []string{"ws_read", "ws_edit", "ws_shell"},
		Commands:     []memory.Command{{Cmd: "go test ./...", OK: true}},
		EditFormat:   "search_replace", EditsAttempted: 8, EditsApplied: 7,
		Summary: "added retry with exponential backoff",
	}
}

func TestReflectIsDeterministic(t *testing.T) {
	r := sampleReport()
	a, b := Reflect(r), Reflect(r)
	if a.Markdown != b.Markdown {
		t.Fatal("Reflect is not deterministic")
	}
	if a.ResolvedFromMemory != b.ResolvedFromMemory || len(a.Candidates) != len(b.Candidates) {
		t.Fatal("Reflect produced different structure on identical input")
	}
}

func TestReflectPartitionsFailures(t *testing.T) {
	ref := Reflect(sampleReport())
	if ref.ResolvedFromMemory != 1 {
		t.Errorf("resolved from memory = %d, want 1", ref.ResolvedFromMemory)
	}
	if ref.ResolvedFromLLM != 1 {
		t.Errorf("resolved from LLM = %d, want 1", ref.ResolvedFromLLM)
	}
	if ref.Unresolved != 1 {
		t.Errorf("unresolved = %d, want 1", ref.Unresolved)
	}
	// A rule that fixed something must be credited; a rule-less fix must
	// become a candidate rule.
	if ok, seen := ref.RuleOutcomes["rule_seeded"]; !seen || !ok {
		t.Errorf("the applied rule was not credited: %v", ref.RuleOutcomes)
	}
	if len(ref.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1 (the LLM-resolved failure)", len(ref.Candidates))
	}
	if len(ref.Checks) != 1 || ref.Checks[0].Kind != CheckFileContains {
		t.Fatalf("regression checks = %+v", ref.Checks)
	}
	if ref.Checks[0].Fingerprint == "" {
		t.Error("the regression check was not bound to a fingerprint")
	}
}

func TestReflectEpisodeCarriesTheFacts(t *testing.T) {
	ref := Reflect(sampleReport())
	e := ref.Episode
	if !e.Success {
		t.Error("a run with every task done and every gate passed should be a success")
	}
	if e.EditsApplied != 7 || e.EditsAttempted != 8 || e.EditFormat != "search_replace" {
		t.Errorf("edit stats lost: %+v", e)
	}
	if e.WallMS != 90000 {
		t.Errorf("wall = %d ms, want 90000", e.WallMS)
	}
	if len(e.Failures) != 3 {
		t.Errorf("failures = %d", len(e.Failures))
	}
	if !e.Failures[0].FromMemory() {
		t.Error("the rule-resolved failure should be marked as fixed from memory")
	}
	if e.Failures[2].Resolved() {
		t.Error("the unresolved failure should not be marked resolved")
	}
}

func TestRunReportSuccessRules(t *testing.T) {
	tests := []struct {
		name string
		r    RunReport
		want bool
	}{
		{"all done, gates pass", RunReport{PlannedTasks: 2, CompletedTasks: 2, Gates: []GateResult{{Passed: true}}}, true},
		{"blocked task", RunReport{PlannedTasks: 2, CompletedTasks: 2, BlockedTasks: 1}, false},
		{"incomplete", RunReport{PlannedTasks: 3, CompletedTasks: 2}, false},
		{"gate failed", RunReport{PlannedTasks: 1, CompletedTasks: 1, Gates: []GateResult{{Passed: false}}}, false},
		{"nothing happened", RunReport{}, false},
		{"files changed with no plan", RunReport{FilesChanged: []string{"a.go"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.Success(); got != tc.want {
				t.Errorf("Success() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReflectMarkdownIsUseful(t *testing.T) {
	ref := Reflect(sampleReport())
	for _, want := range []string{
		"# Reflection", "Intent vs outcome", "Tasks planned → done | 2 → 2",
		"Edits applied | 7/8 (87%)", "Failures and how each was handled",
		"fixed from memory", "unresolved", "New repair rules learned",
		"Choices made and what they earned", "Regression checks added",
	} {
		if !strings.Contains(ref.Markdown, want) {
			t.Errorf("REFLECTION.md missing %q:\n%s", want, ref.Markdown)
		}
	}
	if len(ref.Markdown) > MaxReflectionBytes {
		t.Errorf("reflection is %d bytes, cap is %d", len(ref.Markdown), MaxReflectionBytes)
	}
}

func TestReflectEmptyReport(t *testing.T) {
	ref := Reflect(RunReport{})
	if ref.Markdown == "" {
		t.Error("even an empty run should produce a report")
	}
	if len(ref.Candidates) != 0 || len(ref.Checks) != 0 {
		t.Error("an empty run invented rules or checks")
	}
}

func TestReflectBoundsFailures(t *testing.T) {
	r := sampleReport()
	r.Failures = nil
	for i := 0; i < 200; i++ {
		r.Failures = append(r.Failures, FailureEvent{
			Signal: Signal{Tool: "ws_edit", Message: "old_str not found in f" + itoa(i) + ".go"},
		})
	}
	ref := Reflect(r)
	if len(ref.Episode.Failures) > memory.MaxEpisodeFailures {
		t.Errorf("episode carries %d failures, cap is %d", len(ref.Episode.Failures), memory.MaxEpisodeFailures)
	}
	if len(ref.Markdown) > MaxReflectionBytes {
		t.Errorf("reflection grew to %d bytes", len(ref.Markdown))
	}
}

func TestEnrichIsAdditiveAndOptional(t *testing.T) {
	base := Reflect(sampleReport())

	tests := []struct {
		name string
		sum  memory.Summarizer
		want string
	}{
		{"nil summarizer", nil, ""},
		{"erroring", func(context.Context, string) (string, error) { return "", errors.New("boom") }, ""},
		{"empty", func(context.Context, string) (string, error) { return "  ", nil }, ""},
		{"good", func(context.Context, string) (string, error) { return "Prefer search/replace here.", nil }, "Prefer search/replace here."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := Reflect(sampleReport())
			Enrich(context.Background(), &ref, tc.sum)
			if !strings.HasPrefix(ref.Markdown, base.Markdown) {
				t.Fatal("Enrich modified the deterministic part of the report")
			}
			if tc.want == "" {
				if ref.Markdown != base.Markdown {
					t.Errorf("a useless summarizer changed the report:\n%s", ref.Markdown)
				}
				return
			}
			if !strings.Contains(ref.Markdown, tc.want) || !strings.Contains(ref.Markdown, "Advisory only") {
				t.Errorf("enrichment not appended or not labeled advisory:\n%s", ref.Markdown)
			}
		})
	}
	var nilRef *Reflection
	Enrich(context.Background(), nilRef, nil) // must not panic
}

func TestWriteReflection(t *testing.T) {
	dir := t.TempDir()
	ref := Reflect(sampleReport())
	if err := WriteReflection(dir, ref); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".slmcode", "memory", "REFLECTION.md")
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("REFLECTION.md missing: %v", err)
	}
	if !strings.Contains(string(data), "# Reflection") {
		t.Error("wrong content written")
	}
	if err := WriteReflection("", ref); err != nil {
		t.Errorf("WriteReflection with no project dir should be a no-op: %v", err)
	}
}

func TestReflectionApplyCommitsEverything(t *testing.T) {
	proj, user := t.TempDir(), t.TempDir()
	mem, err := memory.Open(proj, user)
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := OpenRules(proj, user)
	bandit, _ := OpenBanditWith(user, BanditOptions{Deterministic: true})
	regs, _ := OpenRegressions(proj)

	// Bind the credited rule id to a real rule so Observe has something to hit.
	seed := SeedRules()[0]
	r := sampleReport()
	r.Failures[0].RuleID = seed.ID
	r.Failures[0].ResolvedBy = "rule:" + seed.ID

	ref := Reflect(r)
	if err := ref.Apply(mem, rules, bandit, regs); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if mem.Episodes().Count() != 1 {
		t.Errorf("episode not recorded: %d", mem.Episodes().Count())
	}
	if got, _ := rules.Get(seed.ID); got.Successes != 1 {
		t.Errorf("rule not credited: %+v", got)
	}
	if rules.Count() <= len(SeedRules()) {
		t.Error("the candidate rule was not learned")
	}
	if len(bandit.Snapshot()) == 0 {
		t.Error("bandit was not updated")
	}
	if regs.Count() != 1 {
		t.Errorf("regression checks = %d", regs.Count())
	}
	if p, ok := mem.Procedural().Get(memory.ProcKey{
		Topic: memory.TopicEditFormat, Option: "search_replace",
		ModelFamily: "qwen2.5-coder", Language: "go",
	}); !ok || p.Samples() != 1 {
		t.Errorf("procedural memory not updated: %+v (ok=%v)", p, ok)
	}
}

func TestReflectionApplyIsNilSafe(t *testing.T) {
	ref := Reflect(sampleReport())
	if err := ref.Apply(nil, nil, nil, nil); err != nil {
		t.Fatalf("Apply with no stores should be a no-op: %v", err)
	}
}
