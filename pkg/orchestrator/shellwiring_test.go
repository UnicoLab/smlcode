package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggtools "github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// runShellTool invokes the REGISTERED ws_shell tool, i.e. the same entry point
// a worker's tool call goes through in production.
func runShellTool(reg *ggtools.ToolRegistry, command string) (string, error) {
	tool, ok := reg.GetTool("ws_shell")
	if !ok {
		return "", errNoShellTool
	}
	raw, err := json.Marshal(map[string]interface{}{"command": command})
	if err != nil {
		return "", err
	}
	v, err := tool.Execute(context.Background(), string(raw))
	out, _ := v.(string)
	return out, err
}

var errNoShellTool = errors.New("ws_shell is not registered")

// TestShellHookWiringDoesNotStallTheToolCall drives the REAL path: a ws_shell
// command executed through the real Workspace, with the orchestrator's
// OnShellResult hook installed exactly as production installs it.
//
// WHY THIS EXISTS. The other tests in shellobjective_test.go call
// noteShellObjectiveRun directly, which is precisely where a wiring defect
// CANNOT show up. The hook runs on the worker's goroutine inside a tool call
// and reaches back into the orchestrator for o.mu; the first version of it
// deadlocked the entire run by computing a file fingerprint while already
// holding that lock, and no direct-call test could have caught that. This one
// would: a stall here is a stall in a worker's tool loop, which is exactly how
// a run burns its whole budget with nothing to show.
func TestShellHookWiringDoesNotStallTheToolCall(t *testing.T) {
	// A command that SUCCEEDS in a bare temp dir, and that is the configured
	// objective, so the hook reaches its recording path. With `go test ./...`
	// here the command fails, the hook early-returns, and this test passes
	// without ever executing the code it exists to guard.
	const okCmd = "echo objective-ok"
	o := objectiveOrch(t, &fakeGate{ok: false, out: "FAIL\n"}, &countingExec{},
		func(c *config.Config) { c.QAGateCommand = okCmd })

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := ggtools.NewToolRegistry()
	if _, _, err := workspace.RegisterCodingToolsWithWorkspace(reg, root, workspace.ToolOpts{
		OnShellResult: func(command string, ok bool, output string) string {
			return o.noteShellObjectiveRun(command, ok, output)
		},
	}); err != nil {
		t.Fatalf("workspace: %v", err)
	}

	// Run the objective command itself through the real tool, on a watchdog.
	// A deadlock shows up as this channel never being written to.
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, e := runShellTool(reg, okCmd)
		done <- result{out: s, err: e}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("ws_shell returned an error: %v", r.err)
		}
		// The command itself almost certainly fails in a bare temp dir, and
		// that is fine — this test is about the hook not stalling, not about
		// the command's verdict. What matters is that it CAME BACK.
		_ = r.out
	case <-time.After(45 * time.Second):
		t.Fatal("ws_shell did not return within 45s with the objective hook " +
			"installed — the observer stalled a worker's tool call, which is how " +
			"a run burns its entire budget having completed nothing")
	}
}

// TestShellHookSurvivesAPanickingObserver pins the recover in noteShellResult.
// The hook runs on the worker's goroutine mid-tool-call, so an observer that
// panics would otherwise take down the tool call, the task and the run — for a
// component whose only job is to watch.
func TestShellHookSurvivesAPanickingObserver(t *testing.T) {
	root := t.TempDir()
	reg := ggtools.NewToolRegistry()
	if _, _, err := workspace.RegisterCodingToolsWithWorkspace(reg, root, workspace.ToolOpts{
		OnShellResult: func(string, bool, string) string {
			panic("observer blew up")
		},
	}); err != nil {
		t.Fatalf("workspace: %v", err)
	}

	out, err := runShellTool(reg, "echo hi")
	if err != nil {
		t.Fatalf("a panicking observer failed the tool call: %v", err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("the command's own output was lost: %q", out)
	}
}
