package evolve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/memory"
)

// Same fixture, twice: deterministic mode must produce byte-identical choices.
func TestDeterministicModeIsReproducible(t *testing.T) {
	run := func() []string {
		dir := t.TempDir()
		e, err := OpenWith(dir, dir, EngineOptions{
			Deterministic: true, ProjectPolicy: true,
			Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
		})
		if err != nil {
			t.Logf("open warnings: %v", err)
		}
		var out []string
		arms := []string{"unified_diff", "search_replace", "whole_file"}
		for i := 0; i < 20; i++ {
			c := e.Bandit().ChooseWithReason(Key{ModelFamily: "qwen", Language: "go", Decision: "edit_format"}, arms)
			out = append(out, c.Arm+"|"+c.Reason)
			e.Bandit().Update(Key{ModelFamily: "qwen", Language: "go", Decision: "edit_format"}, c.Arm,
				Outcome{Applied: i%3 == 0, GateRan: true, GatePassed: i%2 == 0})
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("run %d diverged:\n  %s\n  %s", i, a[i], b[i])
		}
	}
}

// A read-only / unwritable memory + evolve directory must never fail a run.
func TestUnwritableStoresDegradeGracefully(t *testing.T) {
	dir := t.TempDir()
	// .slmcode is a regular FILE: nothing can be created underneath it. This
	// reproduces an unwritable/corrupt store even when the test runs as root.
	if err := os.WriteFile(filepath.Join(dir, ".slmcode"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := OpenWith(dir, dir, EngineOptions{ProjectPolicy: true})
	t.Logf("open err=%v warnings=%v", err, e.Warnings())
	if e == nil {
		t.Fatal("nil engine")
	}
	// Hot path must not panic or block.
	for i := 0; i < 100; i++ {
		if m := e.Memory(); m != nil {
			m.Working().RecordTool(memory.ToolEvent{Tool: "ws_read", Path: "a.go", OK: true})
		}
		e.OnFailure(Signal{Tool: "ws_edit", Message: "old_str not found", Language: "go"}, `{"old_str":"x"}`)
		_ = e.Choose("edit_format", "unified_diff", "search_replace")
	}
	if err := e.Close(); err != nil {
		t.Logf("close err (tolerated): %v", err)
	}
}
