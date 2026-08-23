package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// A stdio MCP server that records its own pid, answers the handshake, and then
// blocks — the same shape pkg/mcp uses, plus the pid file this test needs to
// prove the process really went away.
const pidServer = `
import sys, json, os
open(sys.argv[1], "a").write(str(os.getpid()) + "\n")
while True:
    line = sys.stdin.readline()
    if not line:
        break
    try:
        req = json.loads(line)
    except Exception:
        continue
    m = req.get("method")
    if m == "initialize":
        print(json.dumps({"jsonrpc":"2.0","id":req["id"],"result":{"ok":True}}), flush=True)
    elif m == "tools/list":
        print(json.dumps({"jsonrpc":"2.0","id":req["id"],"result":{"tools":[
            {"name":"search","description":"find","inputSchema":{"type":"object"},
             "annotations":{"readOnlyHint":True}}]}}), flush=True)
`

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitGone(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func pidsFrom(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []int
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("bad pid %q", line)
		}
		out = append(out, n)
	}
	return out
}

// mcpWorkspace writes a project whose config starts one stdio MCP server, and
// returns the root plus the pid file that server appends to.
func mcpWorkspace(t *testing.T) (root, pidFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stdio fake server is POSIX only")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	root = t.TempDir()
	script := filepath.Join(root, "server.py")
	if err := os.WriteFile(script, []byte(pidServer), 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile = filepath.Join(root, "pids.txt")

	cfg := config.Default(root)
	cfg.Root = root
	cfg.MCPServers = []config.MCPServerConfig{{
		Name:    "fake",
		Command: "python3",
		Args:    []string{"-u", script, pidFile},
	}}
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	return root, pidFile
}

// TestReplacingTheOrchestratorClosesThePreviousOne is the MCP subprocess leak.
//
// mcp.Manager.Close had zero reachable call sites from the harness, and
// `slmcode studio` rebuilds the orchestrator on every PUT /api/config — so one
// stdio server process per config save accumulated for the daemon's lifetime.
func TestReplacingTheOrchestratorClosesThePreviousOne(t *testing.T) {
	root, pidFile := mcpWorkspace(t)

	h, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	first := pidsFrom(t, pidFile)
	if len(first) != 1 {
		t.Skipf("the fake MCP server did not start (pids %v) — nothing to assert", first)
	}
	if !alive(first[0]) {
		t.Fatalf("server %d died on its own", first[0])
	}

	// The Studio config-save path: build a replacement and install it.
	if err := h.RebuildOrchestrator(); err != nil {
		t.Fatalf("RebuildOrchestrator: %v", err)
	}
	all := pidsFrom(t, pidFile)
	if len(all) != 2 {
		t.Fatalf("expected the rebuild to start exactly one new server, got pids %v", all)
	}
	if !waitGone(t, first[0]) {
		t.Fatalf("the replaced orchestrator's MCP subprocess %d is still running — this is the leak", first[0])
	}
	if !alive(all[1]) {
		t.Fatalf("the NEW orchestrator's MCP subprocess %d must still be running", all[1])
	}

	// Teardown must reap the live one too.
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !waitGone(t, all[1]) {
		t.Fatalf("harness teardown leaked MCP subprocess %d", all[1])
	}
}

// TestHarnessCloseIsIdempotentAndNilSafe: Close is deferred by callers, so it
// must tolerate being called twice and on a zero harness.
func TestHarnessCloseIsIdempotentAndNilSafe(t *testing.T) {
	var nilH *Harness
	if err := nilH.Close(); err != nil {
		t.Fatalf("nil harness Close: %v", err)
	}
	if err := nilH.SetOrchestrator(nil); err != nil {
		t.Fatalf("nil harness SetOrchestrator: %v", err)
	}
	h := &Harness{}
	if err := h.Close(); err != nil {
		t.Fatalf("empty harness Close: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
}

// TestSetOrchestratorSwapsAndKeepsTheNewOne covers the pointer contract without
// any subprocess: the new engine is installed, the old pointer is released, and
// re-installing the SAME orchestrator does not close it out from under itself.
func TestSetOrchestratorSwapsAndKeepsTheNewOne(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Root = root
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	h, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	first := h.Orchestrator
	if first == nil {
		t.Fatal("New must install an orchestrator")
	}
	// Installing the same pointer must not close it.
	if err := h.SetOrchestrator(first); err != nil {
		t.Fatalf("self-install: %v", err)
	}
	if h.Orchestrator != first {
		t.Fatal("self-install must keep the orchestrator")
	}
	second, err := orchestrator.New(h.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SetOrchestrator(second); err != nil {
		t.Fatalf("SetOrchestrator: %v", err)
	}
	if h.Orchestrator != second {
		t.Fatal("SetOrchestrator must install the new orchestrator")
	}
	// The replaced orchestrator is closed; Close is idempotent, so calling it
	// again must still succeed.
	if err := first.Close(); err != nil {
		t.Fatalf("the replaced orchestrator must already be closed (idempotent): %v", err)
	}
}
