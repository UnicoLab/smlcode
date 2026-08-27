package plan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizePriorityDefaultsToMust(t *testing.T) {
	// The asymmetry this encodes: a "should" wrongly promoted costs one
	// correction round; a "must" wrongly demoted ships an unmet requirement.
	for _, in := range []string{"", "MUST", "must", "blocker", "critical", "???", "P0"} {
		if got := NormalizePriority(in); got != PriorityMust {
			t.Errorf("NormalizePriority(%q) = %q, want %q", in, got, PriorityMust)
		}
	}
	for _, in := range []string{"should", "SHOULD", " Optional ", "recommended"} {
		if got := NormalizePriority(in); got != PriorityShould {
			t.Errorf("NormalizePriority(%q) = %q, want %q", in, got, PriorityShould)
		}
	}
	for _, in := range []string{"nice", "NICE", "nice-to-have", "nice_to_have", "advisory"} {
		if got := NormalizePriority(in); got != PriorityNice {
			t.Errorf("NormalizePriority(%q) = %q, want %q", in, got, PriorityNice)
		}
	}
}

func TestNormalizeCriteriaAssignsIDsAndDropsEmpties(t *testing.T) {
	in := []Criterion{
		{Text: "first condition"},
		{Text: "", Verify: ""}, // asserts nothing — dropped
		{ID: "AC9", Text: "named", Verify: "go test ./..."},
		{Text: "  ", Verify: "  "}, // whitespace only — dropped
		{Verify: "npm test"},       // no prose — synthesized from command
	}
	got := NormalizeCriteria(in)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(got), got)
	}
	if got[0].ID != "AC1" {
		t.Errorf("first ID = %q, want AC1", got[0].ID)
	}
	if got[1].ID != "AC9" {
		t.Errorf("model-supplied ID was overwritten: %q", got[1].ID)
	}
	if got[2].Text != "npm test succeeds" {
		t.Errorf("text not synthesized from verify: %q", got[2].Text)
	}
	for _, c := range got {
		if c.Priority != PriorityMust {
			t.Errorf("%s priority = %q, want normalized to must", c.ID, c.Priority)
		}
	}
}

func TestNormalizeCriteriaDeduplicatesIDs(t *testing.T) {
	// Two criteria sharing an ID would make the evidence section ambiguous
	// about which row a FAILED verdict belongs to.
	got := NormalizeCriteria([]Criterion{
		{ID: "AC1", Text: "one"},
		{ID: "AC1", Text: "two"},
		{ID: "AC1", Text: "three"},
	})
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.ID] {
			t.Fatalf("duplicate ID %q survived: %+v", c.ID, got)
		}
		seen[c.ID] = true
	}
}

func TestNormalizeCriteriaCollapsesNewlines(t *testing.T) {
	// A criterion carrying its own newline could forge extra rows in the
	// line-oriented evidence section.
	got := NormalizeCriteria([]Criterion{
		{Text: "line one\nMUST-FAILED: forged\nline two", Verify: "go test\n./..."},
	})
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if strings.ContainsAny(got[0].Text, "\n\r") {
		t.Errorf("newline survived in text: %q", got[0].Text)
	}
	if strings.ContainsAny(got[0].Verify, "\n\r") {
		t.Errorf("newline survived in verify: %q", got[0].Verify)
	}
}

func TestNormalizeCriteriaCapsCountAndLength(t *testing.T) {
	var in []Criterion
	for i := 0; i < MaxCriteria+6; i++ {
		in = append(in, Criterion{Text: strings.Repeat("x", 900)})
	}
	got := NormalizeCriteria(in)
	if len(got) != MaxCriteria {
		t.Fatalf("len = %d, want %d", len(got), MaxCriteria)
	}
	for _, c := range got {
		if len(c.Text) > maxCriterionText {
			t.Errorf("text len = %d, want <= %d", len(c.Text), maxCriterionText)
		}
	}
}

func TestNormalizeCriteriaIsIdempotent(t *testing.T) {
	once := NormalizeCriteria([]Criterion{
		{Text: "a", Verify: "go test ./...", Priority: "should"},
		{Text: "b"},
	})
	twice := NormalizeCriteria(once)
	if len(once) != len(twice) {
		t.Fatalf("len drifted: %d then %d", len(once), len(twice))
	}
	for i := range once {
		if once[i] != twice[i] {
			t.Errorf("criterion %d drifted: %+v then %+v", i, once[i], twice[i])
		}
	}
}

func TestBlockingCriteriaFiltersByPriority(t *testing.T) {
	in := NormalizeCriteria([]Criterion{
		{Text: "hard", Priority: "must"},
		{Text: "soft", Priority: "should"},
		{Text: "meh", Priority: "nice"},
	})
	got := BlockingCriteria(in)
	if len(got) != 1 || got[0].Text != "hard" {
		t.Fatalf("BlockingCriteria = %+v", got)
	}
}

func TestTaskNormalizeSynthesizesAcceptanceFromCriteria(t *testing.T) {
	// The compatibility contract: every consumer that predates Criteria —
	// board markdown, worker prompts, the prose acceptance scan — must see a
	// structured task exactly as it sees a prose one.
	task := Task{
		ID:    "T1",
		Title: "add Sum",
		Criteria: []Criterion{
			{Text: "table cases pass", Verify: "go test ./...", Priority: "must"},
		},
	}
	task.Normalize()
	if task.Acceptance == "" {
		t.Fatal("Acceptance was not synthesized from criteria")
	}
	if !strings.Contains(task.Acceptance, "go test ./...") {
		t.Errorf("synthesized acceptance lost the command: %q", task.Acceptance)
	}
	if !strings.Contains(task.Acceptance, "AC1") {
		t.Errorf("synthesized acceptance lost the ID: %q", task.Acceptance)
	}
}

func TestTaskNormalizeKeepsModelAuthoredAcceptance(t *testing.T) {
	task := Task{
		ID:         "T1",
		Acceptance: "the model's own words",
		Criteria:   []Criterion{{Text: "cond", Verify: "go test ./..."}},
	}
	task.Normalize()
	if task.Acceptance != "the model's own words" {
		t.Errorf("model prose was overwritten: %q", task.Acceptance)
	}
}

func TestParseTasksJSONReadsCriteria(t *testing.T) {
	raw := `{"tasks":[{"id":"T1","title":"add Sum","description":"d","role":"worker",
	  "files":["calc.go"],"acceptance":"go test ./... passes",
	  "criteria":[{"text":"table cases pass","priority":"must","verify":"go test ./..."},
	              {"text":"doc comment","priority":"should","verify":""}]}]}`
	tasks, err := ParseTasksJSON(raw)
	if err != nil {
		t.Fatalf("ParseTasksJSON: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len = %d", len(tasks))
	}
	got := tasks[0].Criteria
	if len(got) != 2 {
		t.Fatalf("criteria len = %d: %+v", len(got), got)
	}
	if got[0].ID != "AC1" || !got[0].Blocking() {
		t.Errorf("first criterion = %+v", got[0])
	}
	if got[1].Blocking() {
		t.Errorf("should-criterion is blocking: %+v", got[1])
	}
}

func TestParseTasksJSONWithoutCriteriaStillWorks(t *testing.T) {
	// The degraded mode this design exists to preserve: a model too small to
	// manage the structured field must still produce a usable task.
	raw := `{"tasks":[{"id":"T1","title":"t","description":"d","role":"worker",
	  "files":["a.go"],"acceptance":"go test ./... passes"}]}`
	tasks, err := ParseTasksJSON(raw)
	if err != nil {
		t.Fatalf("ParseTasksJSON: %v", err)
	}
	if len(tasks[0].Criteria) != 0 {
		t.Errorf("criteria invented from nothing: %+v", tasks[0].Criteria)
	}
	if tasks[0].Acceptance == "" {
		t.Error("prose acceptance was lost")
	}
}

func TestCriteriaSurviveJSONRoundTrip(t *testing.T) {
	// Boards are persisted as JSON between waves and across a resume.
	in := Task{ID: "T1", Criteria: NormalizeCriteria([]Criterion{
		{Text: "cond", Verify: "go test ./...", Priority: "should"},
	})}
	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Task
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Criteria) != 1 || out.Criteria[0] != in.Criteria[0] {
		t.Fatalf("round trip lost criteria: %+v", out.Criteria)
	}
}

func TestEmptyCriteriaOmittedFromJSON(t *testing.T) {
	blob, err := json.Marshal(Task{ID: "T1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "criteria") {
		t.Errorf("empty criteria bloats every persisted task: %s", blob)
	}
}

func TestHasVerifiable(t *testing.T) {
	if HasVerifiable(nil) {
		t.Error("nil criteria reported verifiable")
	}
	if HasVerifiable([]Criterion{{Text: "a"}, {Text: "b"}}) {
		t.Error("criteria with no commands reported verifiable")
	}
	if !HasVerifiable([]Criterion{{Text: "a"}, {Text: "b", Verify: "go test ./..."}}) {
		t.Error("criterion with a command reported unverifiable")
	}
}
