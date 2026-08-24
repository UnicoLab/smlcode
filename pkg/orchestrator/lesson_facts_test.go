package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/learning"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// newLessonFixture is an Orchestrator with a real evolve engine and no LLM: the
// lesson → fact path is deterministic and must need zero model calls.
func newLessonFixture(t *testing.T) (*Orchestrator, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.Root = dir
	cfg.Evolve = true
	cfg.Normalize()

	eng, err := evolve.OpenWith(cfg.Root, filepath.Join(dir, "userhome"),
		evolve.EngineOptions{Deterministic: true})
	if err != nil {
		t.Fatalf("open evolve engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	o := &Orchestrator{
		cfg:     cfg,
		store:   contextstore.New(cfg.SlmDir()),
		evolve:  eng,
		onEvent: func(Event) {},
	}
	if mem := eng.Memory(); mem != nil {
		mem.SetRunContext(memory.RunContext{RunID: "run-abc", Query: "wire the graph backfill"})
	}
	return o, dir
}

// TestWaveLessonsReachSemanticMemory walks the production wiring: board tasks →
// learning.Extract → recordLessonFacts → facts.json on disk, with provenance
// intact. Before this path existed the two learning stacks never spoke: lessons
// went to flat Markdown and the typed store never heard about them.
func TestWaveLessonsReachSemanticMemory(t *testing.T) {
	o, dir := newLessonFixture(t)

	blocked := plan.Task{ID: "T4", Column: plan.ColBlocked, Error: "import cycle between pkg/a and pkg/b"}
	blocked.Normalize()
	lessons := learning.Extract(blocked)
	if len(lessons) == 0 {
		t.Fatal("expected a failure lesson")
	}
	o.recordLessonFacts(lessons)

	facts := o.evolve.Memory().Semantic()
	found := false
	for _, f := range facts.All() {
		if !strings.Contains(f.Text, "import cycle") {
			continue
		}
		found = true
		if f.Kind != memory.FactGotcha {
			t.Errorf("failure lesson landed in %q, want gotcha", f.Kind)
		}
		// Sources is read by name elsewhere (pkg/graph's backfill), so it must
		// carry the originating task and run id.
		if len(f.Sources) < 2 || f.Sources[0] != "T4" || f.Sources[1] != "run-abc" {
			t.Errorf("sources = %v, want [T4 run-abc ...]", f.Sources)
		}
		if f.Confidence <= 0 || f.Confidence >= 1 {
			t.Errorf("confidence = %v, want a Beta posterior strictly inside (0,1)", f.Confidence)
		}
	}
	if !found {
		t.Fatalf("lesson never reached semantic memory: %+v", facts.All())
	}

	// recordLessonFacts flushes, so an interrupted run still keeps what it
	// learned. Verify through the file, not the in-memory store.
	data, err := os.ReadFile(filepath.Join(dir, ".slmcode", "memory", "facts.json"))
	if err != nil {
		t.Fatalf("facts.json not written: %v", err)
	}
	var ff struct {
		Facts []memory.Fact `json:"facts"`
	}
	if err := json.Unmarshal(data, &ff); err != nil {
		t.Fatalf("facts.json unreadable: %v", err)
	}
	if len(ff.Facts) == 0 {
		t.Fatalf("facts.json holds no facts:\n%s", data)
	}
}

// TestRecordLessonFactsIsNilSafe: --no-evolve is a supported mode, and losing
// the enrichment must never cost a run.
func TestRecordLessonFactsIsNilSafe(t *testing.T) {
	lessons := []learning.Lesson{{TaskID: "T1", Kind: "failure", Text: "something went wrong"}}
	(&Orchestrator{onEvent: func(Event) {}}).recordLessonFacts(lessons) // no engine
	var nilOrch *Orchestrator
	nilOrch.recordLessonFacts(lessons)

	o, _ := newLessonFixture(t)
	o.recordLessonFacts(nil) // no lessons
}
