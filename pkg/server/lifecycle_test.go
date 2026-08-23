package server

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
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// Studio rebuilds the orchestrator on every PUT /api/config. An orchestrator
// OWNS PROCESSES — each stdio MCP server it starts is a child process that only
// dies in mcp.Manager.Close — so setOrch assigning s.h.Orchestrator directly
// stranded one server process per config save for the daemon's lifetime.
//
// This test starts a real stdio MCP child, swaps the orchestrator the way
// handlePutConfig does, and asserts the replaced child is reaped.

const pidMCPServer = `
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

func mcpAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func mcpWaitGone(pid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !mcpAlive(pid) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func mcpPids(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	if err != nil {
		return nil
	}
	var out []int
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			t.Fatalf("bad pid %q", line)
		}
		out = append(out, n)
	}
	return out
}

// TestSetOrchClosesTheReplacedEngine is the Studio half of the MCP leak.
func TestSetOrchClosesTheReplacedEngine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stdio fake server is POSIX only")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	script := filepath.Join(root, "server.py")
	if err := os.WriteFile(script, []byte(pidMCPServer), 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(root, "pids.txt")

	cfg := config.Default(root)
	cfg.Root = root
	cfg.MCPServers = []config.MCPServerConfig{{
		Name: "fake", Command: "python3", Args: []string{"-u", script, pidFile},
	}}
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	first := mcpPids(t, pidFile)
	if len(first) != 1 {
		t.Skipf("the fake MCP server did not start (pids %v) — nothing to assert", first)
	}

	s := New(h, nil)
	// Exactly what handlePutConfig does after saving the patch.
	replacement, err := orchestrator.New(h.Config)
	if err != nil {
		t.Fatal(err)
	}
	s.setOrch(replacement)

	all := mcpPids(t, pidFile)
	if len(all) != 2 {
		t.Fatalf("expected the rebuild to start exactly one new server, got pids %v", all)
	}
	if !mcpWaitGone(first[0]) {
		t.Fatalf("the replaced orchestrator's MCP subprocess %d is still running — this is the leak", first[0])
	}
	if s.orch() != replacement {
		t.Fatal("setOrch must install the new orchestrator")
	}
	if !mcpAlive(all[1]) {
		t.Fatalf("the NEW orchestrator's MCP subprocess %d must still be running", all[1])
	}
}

// TestSetOrchKeepsSSEWired: the swap must not silently unsubscribe Studio.
func TestSetOrchKeepsSSEWired(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.Root = root
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	s := New(h, nil)
	replacement, err := orchestrator.New(h.Config)
	if err != nil {
		t.Fatal(err)
	}
	s.setOrch(replacement)

	if !replacement.Subscribed() {
		t.Fatal("setOrch must re-wire Studio's SSE fan-out onto the new orchestrator")
	}
}
