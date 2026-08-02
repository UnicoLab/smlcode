package pipeline

import (
	"os"
	"path/filepath"
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
