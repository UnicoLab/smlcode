package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// Every refusal this package can produce has to be visible to the observer,
// otherwise the self-improvement engine never learns about the failure mode
// that caused it. These are the exported reason constructors plus the inline
// refusals the tools emit directly.
func TestEveryRefusalReasonIsDetected(t *testing.T) {
	cases := map[string]string{
		"empty old_str":     EmptyOldStrReason("a.go"),
		"line numbers":      LineNumberedOldStrReason("a.go"),
		"edit before read":  EditBeforeReadReason("a.go"),
		"edit not found":    EditNotFoundReason("a.go"),
		"ambiguous":         AmbiguityMessage("a.go", 3, "exact"),
		"write refused":     WriteRefuseReason("a.go"),
		"no-op edit":        "No-op edit refused — old_str and new_str are identical. Change something real, or finish with status JSON.",
		"missing path":      "ws_edit: path is required, e.g. {\"path\":\"pkg/foo/bar.go\",\"old_str\":\"…\",\"new_str\":\"…\"}.",
		"quality monitor":   "QUALITY MONITOR: " + LoopCorrectionMessage("repeat"),
		"read not existing": readErrorHint("a.go", os.ErrNotExist),
	}
	for name, msg := range cases {
		if got := ToolResultFailure(msg, nil); got == "" {
			t.Errorf("%s: refusal not detected — the evolve engine would never see it:\n%s", name, msg)
		}
	}
}

// The complement, and the reason the markers are narrow: an ordinary result
// must never be scored as a failure. A grep that HITS a line containing the
// word "refused" is a successful grep.
func TestOrdinaryResultsAreNotFailures(t *testing.T) {
	ok := []string{
		"edited a.go (1 replacement(s))",
		"wrote a.go (42 bytes)",
		"patched a.go (2 hunk(s) applied)",
		"   1|package x\n   2|\n   3|func A() {}\n",
		"a.go:12: // the write guard would have refused this\n",
		"",
	}
	for _, s := range ok {
		if got := ToolResultFailure(s, nil); got != "" {
			t.Errorf("successful result scored as a failure: %q", s)
		}
	}
}

// A hard error always counts, whatever the result text says.
func TestHardErrorIsAlwaysAFailure(t *testing.T) {
	if ToolResultFailure("", os.ErrPermission) == "" {
		t.Fatal("a tool error must be reported as a failure")
	}
}

// The observer must see every call, and a repaired retry must run the tool a
// second time and return the retry's result — this is the mechanism the
// no-model-round-trip repair depends on.
func TestObserverRepairsAndRetriesInPlace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"),
		[]byte("package x\n\nvar A = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewToolRegistry()
	ws, _, err := RegisterCodingToolsWithWorkspace(reg, root, ToolOpts{
		Permission: "auto", DisableReadBeforeEdit: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var seen []ToolCall
	ws.SetToolObserver(func(_ context.Context, c ToolCall) ToolAdvice {
		mu.Lock()
		seen = append(seen, c)
		mu.Unlock()
		if c.Tool != "ws_edit" || c.Failure() == "" || c.Retried {
			return ToolAdvice{}
		}
		repaired := map[string]interface{}{}
		for k, v := range c.Args {
			repaired[k] = v
		}
		repaired["old_str"] = "var A = 1"
		return ToolAdvice{RetryArgs: repaired}
	}, nil)

	out := callTool(t, reg, "ws_edit", map[string]interface{}{
		"path": "a.go", "old_str": "   3|var A = 1", "new_str": "var A = 2",
	})
	if !strings.Contains(out, "edited a.go") {
		t.Fatalf("the repaired retry did not land: %s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("observer saw %d calls, want 2 (original + retry)", len(seen))
	}
	if seen[0].Retried || !seen[1].Retried {
		t.Fatalf("Retried flags wrong: %v, %v", seen[0].Retried, seen[1].Retried)
	}
}

// Guidance with no repair is appended to the result the agent reads, so a
// stored rule that has advice but no deterministic fix still reaches the model.
func TestObserverGuidanceIsAppendedToTheResult(t *testing.T) {
	root := t.TempDir()
	reg := tools.NewToolRegistry()
	ws, _, err := RegisterCodingToolsWithWorkspace(reg, root, ToolOpts{Permission: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	ws.SetToolObserver(func(_ context.Context, c ToolCall) ToolAdvice {
		if c.Failure() == "" {
			return ToolAdvice{}
		}
		return ToolAdvice{Guidance: "ws_read the file first."}
	}, nil)

	out := callTool(t, reg, "ws_edit", map[string]interface{}{
		"path": "missing.go", "old_str": "a", "new_str": "b",
	})
	if !strings.Contains(out, "ws_read the file first.") {
		t.Fatalf("guidance never reached the agent:\n%s", out)
	}
}

// The retry sink reports the verdict, which is what lets the caller credit the
// rule that produced the repair.
func TestRetrySinkReportsTheVerdict(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("var A = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewToolRegistry()
	ws, _, err := RegisterCodingToolsWithWorkspace(reg, root, ToolOpts{
		Permission: "auto", DisableReadBeforeEdit: true, DisableSyntaxCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var gotOK bool
	var called bool
	ws.SetToolObserver(func(_ context.Context, c ToolCall) ToolAdvice {
		if c.Retried || c.Failure() == "" {
			return ToolAdvice{}
		}
		return ToolAdvice{RetryArgs: map[string]interface{}{
			"path": "a.go", "old_str": "var A = 1", "new_str": "var A = 2",
		}}
	}, func(_ map[string]interface{}, ok bool, _ string) {
		called, gotOK = true, ok
	})
	callTool(t, reg, "ws_edit", map[string]interface{}{
		"path": "a.go", "old_str": "  1|var A = 1", "new_str": "var A = 2",
	})
	if !called {
		t.Fatal("the retry sink was never called")
	}
	if !gotOK {
		t.Fatal("a repaired retry that landed was reported as a failure")
	}
}

// And the negative case, which is what keeps ResolvedFromMemory honest: a
// repair that did NOT work must be reported as a failure, so the rule behind
// it earns no credit.
func TestRetrySinkReportsAFailedRepairAsFailed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("var A = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewToolRegistry()
	ws, _, err := RegisterCodingToolsWithWorkspace(reg, root, ToolOpts{
		Permission: "auto", DisableReadBeforeEdit: true, DisableSyntaxCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var gotOK, called bool
	ws.SetToolObserver(func(_ context.Context, c ToolCall) ToolAdvice {
		if c.Retried || c.Failure() == "" {
			return ToolAdvice{}
		}
		// A "repair" that still cannot match anything in the file.
		return ToolAdvice{RetryArgs: map[string]interface{}{
			"path": "a.go", "old_str": "nothing like this is in the file", "new_str": "x",
		}}
	}, func(_ map[string]interface{}, ok bool, _ string) {
		called, gotOK = true, ok
	})
	callTool(t, reg, "ws_edit", map[string]interface{}{
		"path": "a.go", "old_str": "  1|var A = 1", "new_str": "var A = 2",
	})
	if !called {
		t.Fatal("the retry sink was never called")
	}
	if gotOK {
		t.Fatal("a repaired retry that refused again was reported as a success")
	}
}

func callTool(t *testing.T, reg *tools.ToolRegistry, name string, args map[string]interface{}) string {
	t.Helper()
	tool, ok := reg.GetTool(name)
	if !ok {
		t.Fatalf("tool %s is not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(context.Background(), string(raw))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	s, _ := out.(string)
	return s
}
