package mcp

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A stdio MCP server that emits an enormous single line must not be able to
// grow the harness's heap without bound.
func TestAdvGiantStdioLineIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "flood.sh")
	// Never emits a newline; just keeps writing.
	body := "#!/bin/bash\nwhile :; do head -c 1048576 /dev/zero | tr '\\0' 'A'; done\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := &client{cfg: ServerConfig{Name: "flood"}, tools: map[string]ToolInfo{}}
	cmd := exec.Command("bash", script)
	stdout, _ := cmd.StdoutPipe()
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	c.startReader()
	defer close(c.done)

	deadline := time.After(20 * time.Second)
	for {
		select {
		case line := <-c.lines:
			if len(line) > MaxLineBytes {
				t.Fatalf("UNBOUNDED LINE: got %d bytes, cap is %d", len(line), MaxLineBytes)
			}
			return
		case err := <-c.readErr:
			if err != nil && strings.Contains(err.Error(), "too long") {
				return // bounded, reported as an error — also fine
			}
			return
		case <-deadline:
			t.Fatal("reader neither bounded the line nor errored within 20s")
		}
	}
}

// Close must be safe twice, and safe while a request is outstanding.
func TestAdvCloseIdempotentAndDuringCall(t *testing.T) {
	m := &Manager{}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := m.Shutdown(); err != nil {
		t.Fatalf("Shutdown after Close: %v", err)
	}
	_, err := m.Call(context.Background(), "nope", "x", nil)
	if err == nil {
		t.Fatal("Call after Close should error")
	}
}

// read_only must refuse anything not provably read-only.
func TestAdvReadOnlyEnforcement(t *testing.T) {
	tru, fls := true, false
	ro := ServerConfig{Name: "s", ReadOnly: true}
	cases := []struct {
		info  ToolInfo
		known bool
		want  bool
	}{
		{ToolInfo{Name: "read"}, false, false},                   // unadvertised
		{ToolInfo{Name: "read"}, true, false},                    // no annotation
		{ToolInfo{Name: "read", ReadOnlyHint: &tru}, true, true}, // annotated ro
		{ToolInfo{Name: "w", ReadOnlyHint: &fls}, true, false},   // annotated rw
		{ToolInfo{Name: "w", ReadOnlyHint: &tru, DestructiveHint: &tru}, true, false},
	}
	for _, c := range cases {
		got, _ := IsToolAllowed(ro, c.info, c.known)
		if got != c.want {
			t.Errorf("IsToolAllowed(%+v, known=%v) = %v want %v", c.info, c.known, got, c.want)
		}
	}
	// allow_tools pins exactly.
	pin := ServerConfig{Name: "s", ReadOnly: false, AllowTools: []string{"ok"}}
	if ok, _ := IsToolAllowed(pin, ToolInfo{Name: "evil"}, true); ok {
		t.Error("allow_tools did not pin the tool set")
	}
}
