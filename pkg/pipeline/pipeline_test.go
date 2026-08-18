package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultValidateAndRoundTrip(t *testing.T) {
	cfg := Default()
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.PhaseAgent("plan", "planner") != "planner" {
		t.Fatal(cfg.PhaseAgent("plan", "planner"))
	}
	dir := t.TempDir()
	if err := Save(dir, &cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Order) != len(cfg.Order) {
		t.Fatalf("order len %d", len(loaded.Order))
	}
	if loaded.Execute.Reviewer != "reviewer" {
		t.Fatal(loaded.Execute.Reviewer)
	}
}

func TestLoadFallsBackToPipelineBackup(t *testing.T) {
	cfg := Default()
	cfg.Execute.MaxWaves = 2
	dir := t.TempDir()
	if err := Save(dir, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Execute.MaxWaves = 7
	if err := Save(dir, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte("version: [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Execute.MaxWaves != 2 {
		t.Fatalf("max_waves=%d", got.Execute.MaxWaves)
	}
}

func TestSlotsAtAndWhen(t *testing.T) {
	cfg := Default()
	cfg.Slots = []Slot{
		{ID: "pre-plan", Agent: "worker", Before: "plan", When: WhenAlways},
		{ID: "lg-only", Agent: "architect", After: "explore", When: "query_matches:langgraph"},
		{ID: "off", Agent: "memory", After: "test", When: WhenNever},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if n := len(cfg.SlotsAt("plan", "before")); n != 1 {
		t.Fatalf("before plan=%d", n)
	}
	if !SlotMatchesWhen("query_matches:langgraph", "setup langgraph agent") {
		t.Fatal("expected match")
	}
	if SlotMatchesWhen("query_matches:langgraph", "hello world") {
		t.Fatal("expected no match")
	}
	// never slot still listed structurally but EnabledOrDefault false
	off := cfg.SlotsAt("test", "after")
	if len(off) != 0 {
		t.Fatalf("never slot should be filtered: %#v", off)
	}
}

func TestEnsureFile(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureFile(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	// second call no-op
	if err := EnsureFile(dir); err != nil {
		t.Fatal(err)
	}
}

func TestRenderSlotInput(t *testing.T) {
	out := RenderSlotInput("Q={{query}} P={{phase}}", "hi", "", "", "plan")
	if out != "Q=hi P=plan" {
		t.Fatal(out)
	}
}

func TestView(t *testing.T) {
	v := View(nil)
	if len(v.Anchors) == 0 || v.Defaults["plan"] == "" {
		t.Fatalf("%+v", v)
	}
}

// TestValidateGroupRules covers the structural group checks: unique non-empty
// ids, steps referencing known phases, and no phase in multiple groups.
func TestValidateGroupRules(t *testing.T) {
	base := func() Config {
		cfg := Default()
		cfg.Groups = []GroupMeta{
			{ID: "g1", Label: "G1", Steps: []string{"init", "skills"}},
			{ID: "g2", Label: "G2", Steps: []string{"plan", "split"}},
		}
		cfg.Normalize()
		return cfg
	}

	t.Run("valid groups pass", func(t *testing.T) {
		cfg := base()
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("duplicate group id", func(t *testing.T) {
		cfg := base()
		cfg.Groups[1].ID = "g1"
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), `group "g1": duplicate group id`) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("empty group id", func(t *testing.T) {
		cfg := base()
		cfg.Groups[0].ID = "   "
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "group: empty id") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("unknown phase in steps", func(t *testing.T) {
		cfg := base()
		cfg.Groups[0].Steps = []string{"init", "nope"}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), `group "g1": unknown phase "nope" in steps`) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("phase in multiple groups", func(t *testing.T) {
		cfg := base()
		cfg.Groups[1].Steps = []string{"init", "split"} // init already in g1
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), `phase "init" assigned to multiple groups`) {
			t.Fatalf("err = %v", err)
		}
	})
}
