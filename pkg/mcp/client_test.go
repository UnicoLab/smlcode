package mcp

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func boolp(b bool) *bool { return &b }

// read_only used to be declared, shown in the UI, and enforced by an EMPTY
// if-branch. These cases pin the real policy.
func TestIsToolAllowed(t *testing.T) {
	cases := []struct {
		name  string
		cfg   ServerConfig
		info  ToolInfo
		known bool
		want  bool
		msg   string
	}{
		{
			name: "read-only server refuses an unannotated tool",
			cfg:  ServerConfig{Name: "s", ReadOnly: true},
			info: ToolInfo{Name: "delete_everything"}, known: true,
			want: false, msg: "not annotated read-only",
		},
		{
			name: "read-only server allows a readOnlyHint tool",
			cfg:  ServerConfig{Name: "s", ReadOnly: true},
			info: ToolInfo{Name: "search", ReadOnlyHint: boolp(true)}, known: true,
			want: true,
		},
		{
			name:  "read-only server refuses a destructive tool even if readOnlyHint is set",
			cfg:   ServerConfig{Name: "s", ReadOnly: true},
			info:  ToolInfo{Name: "wipe", ReadOnlyHint: boolp(true), DestructiveHint: boolp(true)},
			known: true, want: false, msg: "destructive",
		},
		{
			name: "read-only server refuses an undiscovered tool",
			cfg:  ServerConfig{Name: "s", ReadOnly: true},
			info: ToolInfo{Name: "ghost"}, known: false,
			want: false, msg: "not advertised",
		},
		{
			name: "writable server allows anything",
			cfg:  ServerConfig{Name: "s", ReadOnly: false},
			info: ToolInfo{Name: "delete_everything"}, known: true,
			want: true,
		},
		{
			name: "allow_tools is authoritative",
			cfg:  ServerConfig{Name: "s", ReadOnly: true, AllowTools: []string{"wipe"}},
			info: ToolInfo{Name: "wipe"}, known: true,
			want: true,
		},
		{
			name: "allow_tools excludes everything else",
			cfg:  ServerConfig{Name: "s", ReadOnly: false, AllowTools: []string{"search"}},
			info: ToolInfo{Name: "wipe", ReadOnlyHint: boolp(true)}, known: true,
			want: false, msg: "allow_tools",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, why := IsToolAllowed(tc.cfg, tc.info, tc.known)
			if ok != tc.want {
				t.Fatalf("allowed=%v want %v (%s)", ok, tc.want, why)
			}
			if !ok {
				if why == "" {
					t.Fatal("a refusal must explain itself to the model")
				}
				if tc.msg != "" && !strings.Contains(why, tc.msg) {
					t.Fatalf("refusal %q should mention %q", why, tc.msg)
				}
			}
		})
	}
}

func TestRenderToolContract(t *testing.T) {
	infos := []ToolInfo{
		{
			Server: "docs", Name: "search", Description: "Search the docs index",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string"},
					"limit": map[string]interface{}{"type": "integer"},
				},
				"required": []interface{}{"query"},
			},
		},
		{Server: "docs", Name: "ping"},
	}
	out, hidden := RenderToolContract(infos, 12)
	if hidden != 0 {
		t.Fatalf("hidden=%d", hidden)
	}
	// Required params come first and carry no "?"; optional ones do.
	if !strings.Contains(out, "docs.search(query: string, limit?: integer)") {
		t.Fatalf("signature missing:\n%s", out)
	}
	if !strings.Contains(out, "Search the docs index") {
		t.Fatalf("description missing:\n%s", out)
	}
	if !strings.Contains(out, "docs.ping()") {
		t.Fatalf("schema-less tool missing:\n%s", out)
	}
}

func TestRenderToolContractAnnouncesTruncation(t *testing.T) {
	var infos []ToolInfo
	for i := 0; i < 20; i++ {
		infos = append(infos, ToolInfo{Server: "s", Name: "t"})
	}
	out, hidden := RenderToolContract(infos, 12)
	if hidden != 8 {
		t.Fatalf("hidden=%d want 8", hidden)
	}
	if strings.Count(out, "\n")+1 != 12 {
		t.Fatalf("expected 12 rendered lines:\n%s", out)
	}
}

func TestSchemaType(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{"string", map[string]interface{}{"type": "string"}, "string"},
		{"int array", map[string]interface{}{"type": "array",
			"items": map[string]interface{}{"type": "integer"}}, "integer[]"},
		{"bare array", map[string]interface{}{"type": "array"}, "any[]"},
		{"union", map[string]interface{}{"type": []interface{}{"string", "null"}}, "string|null"},
		{"enum", map[string]interface{}{"enum": []interface{}{"a", "b"}}, "enum"},
		{"unknown", map[string]interface{}{}, "any"},
		{"not an object", "nope", "any"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := schemaType(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestCapResult(t *testing.T) {
	if got := capResult("small"); got != "small" {
		t.Fatal("small payloads pass through")
	}
	big := strings.Repeat("x", MaxResultBytes*3)
	got := capResult(big)
	if len(got) >= len(big) {
		t.Fatal("MCP results must be capped")
	}
	if !strings.Contains(got, "bytes truncated") {
		t.Fatal("truncation must be announced")
	}
}

func TestCallRefusesUnknownServer(t *testing.T) {
	m := &Manager{}
	if _, err := m.Call(context.Background(), "nope", "tool", nil); err == nil {
		t.Fatal("expected an error")
	} else if !strings.Contains(err.Error(), "no MCP servers") {
		t.Fatalf("got %v", err)
	}
	m.clients = map[string]*client{"real": {cfg: ServerConfig{Name: "real"}}}
	if _, err := m.Call(context.Background(), "nope", "tool", nil); err == nil ||
		!strings.Contains(err.Error(), "connected: real") {
		t.Fatalf("error should list connected servers: %v", err)
	}
	if _, err := m.Call(context.Background(), "", "", nil); err == nil {
		t.Fatal("missing server/tool must error")
	}
}

func TestCallEnforcesReadOnly(t *testing.T) {
	m := &Manager{clients: map[string]*client{
		"s": {
			cfg:   ServerConfig{Name: "s", ReadOnly: true},
			tools: map[string]ToolInfo{"wipe": {Name: "wipe"}},
		},
	}}
	_, err := m.Call(context.Background(), "s", "wipe", nil)
	if err == nil {
		t.Fatal("read_only must actually block the call")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("got %v", err)
	}
}

// ── item 21: process lifecycle, env, timeouts ──────────────────────────────

// fakeServer writes a tiny stdio MCP server that answers initialize and
// tools/list, then blocks forever — enough to exercise start/Close.
func fakeServer(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-based fake server is POSIX only")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "server.py")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

const echoServer = `
import sys, json, os
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
            {"name":"search","description":"find","inputSchema":{"type":"object",
             "properties":{"q":{"type":"string"}},"required":["q"]},
             "annotations":{"readOnlyHint":True}},
            {"name":"wipe","description":"destroy","annotations":{"destructiveHint":True}}
        ]}}), flush=True)
    elif m == "tools/call":
        print(json.dumps({"jsonrpc":"2.0","id":req["id"],
            "result":{"env_a":os.environ.get("A",""),"env_b":os.environ.get("B","")}}), flush=True)
`

func TestManagerLifecycleEnvAndPolicy(t *testing.T) {
	script := fakeServer(t, echoServer)
	m := &Manager{Servers: []ServerConfig{{
		Name: "fake", Command: "python3", Args: []string{"-u", script},
		Env:      map[string]string{"A": "1", "B": "2"},
		ReadOnly: true,
	}}}
	infos, err := m.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("discovered %d tools", len(infos))
	}
	// Annotations must survive discovery so the read-only policy can use them.
	var search, wipe ToolInfo
	for _, i := range infos {
		switch i.Name {
		case "search":
			search = i
		case "wipe":
			wipe = i
		}
	}
	if search.ReadOnlyHint == nil || !*search.ReadOnlyHint {
		t.Fatal("readOnlyHint must be parsed")
	}
	if wipe.DestructiveHint == nil || !*wipe.DestructiveHint {
		t.Fatal("destructiveHint must be parsed")
	}
	// The destructive tool is refused on a read-only server...
	if _, err := m.Call(context.Background(), "fake", "wipe", nil); err == nil {
		t.Fatal("destructive tool must be refused")
	}
	// ...and the read-only one goes through, proving BOTH env vars survived
	// (cmd.Env used to be reassigned in the loop, keeping only the last).
	out, err := m.Call(context.Background(), "fake", "search", map[string]interface{}{"q": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"env_a": "1"`) || !strings.Contains(out, `"env_b": "2"`) {
		t.Fatalf("every configured env var must be passed: %s", out)
	}

	// Close must terminate AND reap the child; it must be idempotent.
	pid := 0
	m.mu.Lock()
	if c := m.clients["fake"]; c != nil && c.cmd != nil && c.cmd.Process != nil {
		pid = c.cmd.Process.Pid
	}
	m.mu.Unlock()
	if err := m.Close(); err != nil && !strings.Contains(err.Error(), "signal") {
		t.Fatalf("close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close must be idempotent: %v", err)
	}
	if pid > 0 {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if err := syscallKill0(pid); err != nil {
				return // process is gone
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("stdio MCP server leaked after Close")
	}
}

func TestManagerImplementsCloser(t *testing.T) {
	var _ io.Closer = &Manager{}
	// Close on a never-connected manager is a no-op, not a panic.
	m := &Manager{}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

const hangingServer = `
import sys, json
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
        print(json.dumps({"jsonrpc":"2.0","id":req["id"],"result":{}}), flush=True)
    elif m == "tools/list":
        print(json.dumps({"jsonrpc":"2.0","id":req["id"],"result":{"tools":[]}}), flush=True)
    # tools/call is silently swallowed — the server never answers
`

// The 30s deadline used to be decorative: ReadBytes blocked forever while
// holding c.mu, so a silent server wedged the whole agent.
func TestRequestRespectsContextCancellation(t *testing.T) {
	script := fakeServer(t, hangingServer)
	m := &Manager{Servers: []ServerConfig{{
		Name: "hang", Command: "python3", Args: []string{"-u", script}, ReadOnly: true,
	}}}
	if _, err := m.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Logf("Manager.Close: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	m.mu.Lock()
	c := m.clients["hang"]
	m.mu.Unlock()
	if c == nil {
		t.Skip("server did not start")
	}
	if _, err := c.request(ctx, "tools/call", map[string]interface{}{"name": "x"}); err == nil {
		t.Fatal("a silent server must not hang forever")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("request blocked for %s despite context cancellation", elapsed)
	}
}
