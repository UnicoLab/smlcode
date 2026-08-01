package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestCallTrackerRefusesRepeatedEdit(t *testing.T) {
	tr := NewCallTracker()
	calls := 0
	fn := tr.Wrap("ws_edit", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		calls++
		return "edited", nil
	})
	args := map[string]interface{}{"path": "a.go", "old_str": "x", "new_str": "y"}
	if _, err := fn(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	out, err := fn(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := out.(string)
	if !strings.Contains(msg, "QUALITY MONITOR") || !strings.Contains(msg, "same tool call") {
		t.Fatalf("expected loop refuse, got %q", msg)
	}
	if calls != 1 {
		t.Fatalf("second call should not execute, calls=%d", calls)
	}
}

func TestCallTrackerAllowsAfterDifferentStateChange(t *testing.T) {
	tr := NewCallTracker()
	editN, shellN := 0, 0
	edit := tr.Wrap("ws_edit", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		editN++
		return "edited", nil
	})
	shell := tr.Wrap("ws_shell", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		shellN++
		return "ok", nil
	})
	editArgs := map[string]interface{}{"path": "a.go", "old_str": "x", "new_str": "y"}
	_, _ = edit(context.Background(), editArgs)
	_, _ = shell(context.Background(), map[string]interface{}{"command": "true"})
	if _, err := edit(context.Background(), editArgs); err != nil {
		t.Fatal(err)
	}
	if editN != 2 || shellN != 1 {
		t.Fatalf("edit=%d shell=%d", editN, shellN)
	}
}

func TestCallTrackerAllowsDifferentArgs(t *testing.T) {
	tr := NewCallTracker()
	n := 0
	fn := tr.Wrap("ws_edit", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		n++
		return "ok", nil
	})
	_, _ = fn(context.Background(), map[string]interface{}{"path": "a.go", "old_str": "x", "new_str": "y"})
	_, _ = fn(context.Background(), map[string]interface{}{"path": "a.go", "old_str": "x", "new_str": "z"})
	if n != 2 {
		t.Fatalf("different args should execute, n=%d", n)
	}
}

func TestCallTrackerHardStopsAfterMaxCorrections(t *testing.T) {
	tr := NewCallTracker()
	tr.MaxCorrect = 2
	fn := tr.Wrap("ws_read", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		return "body", nil
	})
	args := map[string]interface{}{"path": "a.go"}
	_, _ = fn(context.Background(), args) // first real read
	var last string
	for i := 0; i < 4; i++ {
		out, err := fn(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		last, _ = out.(string)
	}
	if !strings.Contains(last, "HARD STOP") {
		t.Fatalf("expected hard stop after max corrections, got %q", last)
	}
}
