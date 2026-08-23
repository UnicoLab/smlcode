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

// ── item 18: per-task histories ────────────────────────────────────────────

func TestCallTrackerIsolatesTasks(t *testing.T) {
	tr := NewCallTracker()
	n := 0
	fn := tr.Wrap("ws_read", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		n++
		return "go.mod contents", nil
	})
	args := map[string]interface{}{"path": "go.mod"}
	// Two parallel workers legitimately reading the same file must not
	// hard-stop each other (max_parallel: 4 made this routine).
	for _, task := range []string{"task-1", "task-2", "task-3", "task-4"} {
		ctx := WithTaskID(context.Background(), task)
		if out, _ := fn(ctx, args); strings.Contains(out.(string), "QUALITY MONITOR") {
			t.Fatalf("task %s was refused a first read: %v", task, out)
		}
	}
	if n != 4 {
		t.Fatalf("all four tasks should have executed, n=%d", n)
	}
	// Within ONE task the repeat is still caught.
	ctx := WithTaskID(context.Background(), "task-1")
	out, _ := fn(ctx, args)
	if !strings.Contains(out.(string), "QUALITY MONITOR") {
		t.Fatalf("repeat within a task must still be refused: %v", out)
	}
}

func TestCallTrackerResetTask(t *testing.T) {
	tr := NewCallTracker()
	n := 0
	fn := tr.Wrap("ws_read", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		n++
		return "body", nil
	})
	ctx := WithTaskID(context.Background(), "t1")
	args := map[string]interface{}{"path": "a.go"}
	_, _ = fn(ctx, args)
	if out, _ := fn(ctx, args); !strings.Contains(out.(string), "QUALITY MONITOR") {
		t.Fatal("expected a repeat refusal before reset")
	}
	tr.ResetTask("t1")
	if out, _ := fn(ctx, args); strings.Contains(out.(string), "QUALITY MONITOR") {
		t.Fatalf("history must be cleared at task start: %v", out)
	}
	if n != 2 {
		t.Fatalf("n=%d", n)
	}
	tr.ResetAll()
	if out, _ := fn(ctx, args); strings.Contains(out.(string), "QUALITY MONITOR") {
		t.Fatalf("ResetAll must clear every task: %v", out)
	}
}

// The old escape hatch let ANY state-changing call anywhere in the shared
// 12-entry history unlock unlimited repeats — the guard was off exactly when
// the model was looping.
func TestCallTrackerEnvChangeIsScopedAfterTheEarlierCall(t *testing.T) {
	tr := NewCallTracker()
	reads := 0
	read := tr.Wrap("ws_read", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		reads++
		return "body", nil
	})
	shell := tr.Wrap("ws_shell", func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		return "ok", nil
	})
	ctx := WithTaskID(context.Background(), "t1")
	readArgs := map[string]interface{}{"path": "a.go"}

	// shell FIRST, then read, then the same read → the state change happened
	// BEFORE the earlier identical read, so it must not unlock the repeat.
	_, _ = shell(ctx, map[string]interface{}{"command": "go build ./..."})
	_, _ = read(ctx, readArgs)
	out, _ := read(ctx, readArgs)
	if !strings.Contains(out.(string), "QUALITY MONITOR") {
		t.Fatalf("a state change BEFORE the earlier call must not unlock the repeat: %v", out)
	}

	// A state change AFTER the earlier read does unlock it.
	_, _ = shell(ctx, map[string]interface{}{"command": "go test ./..."})
	out, _ = read(ctx, readArgs)
	if strings.Contains(out.(string), "QUALITY MONITOR") {
		t.Fatalf("a state change after the earlier call must unlock the repeat: %v", out)
	}
}

func TestCallTrackerUnknownAndMalformed(t *testing.T) {
	tr := NewCallTracker()
	cases := []struct {
		name string
		tool string
		args map[string]interface{}
		want string
	}{
		{"unknown tool", "ws_frobnicate", map[string]interface{}{}, "does not exist"},
		{"empty name", "", map[string]interface{}{}, "empty name"},
		{"malformed args", "ws_read", map[string]interface{}{"_raw": "path=a.go"}, "malformed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := tr.Wrap(tc.tool, func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
				t.Fatal("must not execute")
				return nil, nil
			})
			out, err := fn(WithTaskID(context.Background(), tc.name), tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.(string), tc.want) {
				t.Fatalf("got %q want %q", out, tc.want)
			}
		})
	}
}

func TestTaskIDRoundTrip(t *testing.T) {
	cases := []struct{ in, want string }{
		{"task-7", "task-7"},
		{"  padded  ", "padded"},
		{"", ""},
	}
	for _, tc := range cases {
		ctx := WithTaskID(context.Background(), tc.in)
		if got := TaskIDFrom(ctx); got != tc.want {
			t.Fatalf("WithTaskID(%q) → %q want %q", tc.in, got, tc.want)
		}
	}
	if TaskIDFrom(context.Background()) != "" {
		t.Fatal("unset context must yield the empty task id")
	}
	//nolint:staticcheck // deliberately checking the nil-context guard
	if TaskIDFrom(nil) != "" {
		t.Fatal("nil context must be safe")
	}
}

func TestLoopCorrectionMessagesAreActionable(t *testing.T) {
	for _, reason := range []string{
		"repeated_tool_call", "empty_tool_name", "unknown_tool:foo", "malformed_args:ws_read",
	} {
		msg := LoopCorrectionMessage(reason)
		if msg == "" {
			t.Fatalf("no message for %s", reason)
		}
		// Every branch must tell the model what to do next.
		if !strings.Contains(msg, "ws_") && !strings.Contains(msg, "DIFFERENT") {
			t.Fatalf("message for %s is not actionable: %s", reason, msg)
		}
	}
}
