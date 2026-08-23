package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// DefaultRequestTimeout bounds one JSON-RPC round trip.
const DefaultRequestTimeout = 30 * time.Second

// DefaultHTTPTimeout bounds an HTTP MCP request. http.DefaultClient has NO
// timeout, so a hung server used to wedge the agent forever.
const DefaultHTTPTimeout = 60 * time.Second

// MaxResultBytes caps an MCP tool result before it reaches the model.
const MaxResultBytes = 16 * 1024

// MaxAdvertisedTools caps the tool contract rendered into mcp_call's
// description; truncation is announced rather than silent.
const MaxAdvertisedTools = 12

// ServerConfig describes one MCP server (stdio or HTTP).
type ServerConfig struct {
	Name     string            `yaml:"name" json:"name"`
	Command  string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args     []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env      map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	URL      string            `yaml:"url,omitempty" json:"url,omitempty"` // HTTP JSON-RPC
	ReadOnly bool              `yaml:"read_only" json:"read_only"`         // default true
	// AllowTools optionally pins the exact tool names callable on this server.
	// When set it wins over the read-only annotation heuristic.
	AllowTools []string `yaml:"allow_tools,omitempty" json:"allow_tools,omitempty"`
}

// ToolInfo is a discovered MCP tool.
type ToolInfo struct {
	Server      string
	Name        string
	Description string
	InputSchema map[string]interface{}
	// ReadOnlyHint mirrors the MCP `annotations.readOnlyHint` field.
	ReadOnlyHint *bool
	// DestructiveHint mirrors `annotations.destructiveHint`.
	DestructiveHint *bool
}

// Manager hosts thin MCP clients and registers them as a single mcp_call
// meta-tool. Manager implements io.Closer.
type Manager struct {
	Servers []ServerConfig
	Log     func(string, ...interface{})
	mu      sync.Mutex
	clients map[string]*client
	infos   []ToolInfo // last Connect discovery
	closed  bool
}

var _ io.Closer = (*Manager)(nil)

type client struct {
	cfg    ServerConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID atomic.Int64
	mu     sync.Mutex
	// waitOnce makes cmd.Wait idempotent so the child is always reaped.
	waitOnce sync.Once
	httpc    *http.Client
	tools    map[string]ToolInfo
	toolsMu  sync.RWMutex
	// One PERSISTENT reader goroutine per client feeds these channels. A
	// per-request goroutine would keep blocking in ReadBytes after its request
	// returned and would then swallow the NEXT request's response line.
	lines   chan []byte
	readErr chan error
	done    chan struct{}
}

// startReader launches the single stdout pump for a stdio client.
func (c *client) startReader() {
	c.lines = make(chan []byte, 8)
	c.readErr = make(chan error, 1)
	c.done = make(chan struct{})
	go func() {
		for {
			line, err := c.stdout.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case c.lines <- line:
				case <-c.done:
					return
				}
			}
			if err != nil {
				select {
				case c.readErr <- err:
				case <-c.done:
				}
				return
			}
		}
	}()
}

// Connect starts configured servers and lists tools (best-effort).
func (m *Manager) Connect(ctx context.Context) ([]ToolInfo, error) {
	m.mu.Lock()
	if m.clients == nil {
		m.clients = map[string]*client{}
	}
	m.closed = false
	m.mu.Unlock()

	var all []ToolInfo
	for _, sc := range m.Servers {
		sc.Name = strings.TrimSpace(sc.Name)
		if sc.Name == "" {
			continue
		}
		if sc.Command == "" && sc.URL == "" {
			continue
		}
		c, err := m.start(ctx, sc)
		if err != nil {
			if m.Log != nil {
				m.Log("mcp %s: connect failed: %v", sc.Name, err)
			}
			continue
		}
		m.mu.Lock()
		m.clients[sc.Name] = c
		m.mu.Unlock()
		toolsList, err := c.listTools(ctx)
		if err != nil {
			if m.Log != nil {
				m.Log("mcp %s: list tools: %v", sc.Name, err)
			}
			continue
		}
		all = append(all, toolsList...)
	}
	m.mu.Lock()
	m.infos = append([]ToolInfo{}, all...)
	m.mu.Unlock()
	return all, nil
}

// LastInfos returns tools discovered on the last Connect.
func (m *Manager) LastInfos() []ToolInfo {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ToolInfo{}, m.infos...)
}

// Close shuts down stdio servers and REAPS them.
//
// Previously Close had zero call sites and never called cmd.Wait, so every
// stdio server leaked for the process lifetime and left a zombie behind.
// Close is idempotent and safe to call from a defer.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	clients := make([]*client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = map[string]*client{}
	m.mu.Unlock()

	var firstErr error
	for _, c := range clients {
		if err := c.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Shutdown is an alias kept for callers that prefer the name.
func (m *Manager) Shutdown() error { return m.Close() }

func (c *client) close() error {
	if c == nil {
		return nil
	}
	if c.done != nil {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	c.waitOnce.Do(func() {
		go func() { done <- c.cmd.Wait() }()
	})
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		select {
		case err := <-done:
			return err
		case <-time.After(2 * time.Second):
			return fmt.Errorf("mcp %s: server did not exit", c.cfg.Name)
		}
	}
}

// RenderToolContract builds a compact, per-tool signature block for the model.
// The discovered InputSchema used to be stored and then dropped, leaving the
// model to guess argument names.
func RenderToolContract(infos []ToolInfo, max int) (string, int) {
	if max <= 0 {
		max = MaxAdvertisedTools
	}
	shown := infos
	if len(shown) > max {
		shown = shown[:max]
	}
	var lines []string
	for _, t := range shown {
		lines = append(lines, fmt.Sprintf("  %s.%s(%s)%s",
			t.Server, t.Name, renderParams(t.InputSchema), renderDesc(t.Description)))
	}
	return strings.Join(lines, "\n"), len(infos) - len(shown)
}

func renderDesc(d string) string {
	d = strings.TrimSpace(strings.ReplaceAll(d, "\n", " "))
	if d == "" {
		return ""
	}
	if len(d) > 110 {
		d = d[:110] + "…"
	}
	return " — " + d
}

// renderParams turns a JSON Schema object into "a: string, b?: int".
func renderParams(schema map[string]interface{}) string {
	if schema == nil {
		return ""
	}
	props, _ := schema["properties"].(map[string]interface{})
	if len(props) == 0 {
		return ""
	}
	required := map[string]bool{}
	switch req := schema["required"].(type) {
	case []interface{}:
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	case []string:
		for _, s := range req {
			required[s] = true
		}
	}
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	// Required first, then alphabetical, so the signature is stable.
	sort.Slice(names, func(i, j int) bool {
		if required[names[i]] != required[names[j]] {
			return required[names[i]]
		}
		return names[i] < names[j]
	})
	var parts []string
	for i, n := range names {
		if i >= 8 {
			parts = append(parts, "…")
			break
		}
		opt := "?"
		if required[n] {
			opt = ""
		}
		parts = append(parts, fmt.Sprintf("%s%s: %s", n, opt, schemaType(props[n])))
	}
	return strings.Join(parts, ", ")
}

func schemaType(v interface{}) string {
	m, ok := v.(map[string]interface{})
	if !ok {
		return "any"
	}
	switch t := m["type"].(type) {
	case string:
		if t == "array" {
			if items, ok := m["items"].(map[string]interface{}); ok {
				return schemaType(items) + "[]"
			}
			return "any[]"
		}
		return t
	case []interface{}:
		var parts []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "|")
		}
	}
	if _, ok := m["enum"]; ok {
		return "enum"
	}
	return "any"
}

// RegisterTools adds a single meta-tool `mcp_call` for MCP invocations.
func (m *Manager) RegisterTools(reg *tools.ToolRegistry, infos []ToolInfo) error {
	if reg == nil || m == nil {
		return nil
	}
	desc := "Call a tool on a connected MCP server. "
	if len(infos) > 0 {
		contract, hidden := RenderToolContract(infos, MaxAdvertisedTools)
		desc += "Available tools (arg? = optional):\n" + contract
		if hidden > 0 {
			desc += fmt.Sprintf("\n  …and %d more tool(s) not listed here; "+
				"call one you know the name of, or ask the operator for the list.", hidden)
		}
		desc += "\nPass server, tool, and an arguments object matching the signature."
	} else {
		desc += "No MCP servers are connected — this tool will always fail; use ws_* tools instead."
	}
	tool := tools.NewGenericTool(
		"mcp_call",
		desc,
		func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			server, _ := args["server"].(string)
			name, _ := args["tool"].(string)
			rawArgs, _ := args["arguments"].(map[string]interface{})
			if rawArgs == nil {
				rawArgs = map[string]interface{}{}
			}
			out, err := m.Call(ctx, server, name, rawArgs)
			if err != nil {
				// Model-facing failures must be actionable, not raw errors.
				return fmt.Sprintf("mcp_call failed: %v\n"+
					"Check the server and tool names against the signatures in this tool's description, "+
					"or continue without MCP.", err), nil
			}
			return out, nil
		},
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"server":    map[string]interface{}{"type": "string"},
				"tool":      map[string]interface{}{"type": "string"},
				"arguments": map[string]interface{}{"type": "object"},
			},
			"required": []string{"server", "tool"},
		},
	)
	return reg.RegisterTool(tool)
}

// ErrToolNotAllowed is returned when read_only blocks a call.
var ErrToolNotAllowed = errors.New("mcp: tool not allowed on a read-only server")

// IsToolAllowed decides whether a tool may be invoked on a server.
//
// read_only used to be declared, shown in the UI, and enforced by an EMPTY
// if-branch: mcp_call invoked anything, including destructive tools. Now:
//   - an explicit allow_tools list is authoritative;
//   - otherwise, on a read-only server a tool must be annotated readOnlyHint
//     (and not destructiveHint) to be callable.
func IsToolAllowed(cfg ServerConfig, info ToolInfo, known bool) (bool, string) {
	if len(cfg.AllowTools) > 0 {
		for _, n := range cfg.AllowTools {
			if strings.EqualFold(strings.TrimSpace(n), info.Name) {
				return true, ""
			}
		}
		return false, fmt.Sprintf(
			"tool %q is not in this server's allow_tools list (%s). Use one of those, or ws_* tools.",
			info.Name, strings.Join(cfg.AllowTools, ", "))
	}
	if !cfg.ReadOnly {
		return true, ""
	}
	if !known {
		return false, fmt.Sprintf(
			"tool %q was not advertised by server %q, so the harness cannot tell whether it mutates state. "+
				"Server %q is configured read_only. Call a tool listed in mcp_call's description.",
			info.Name, cfg.Name, cfg.Name)
	}
	if info.DestructiveHint != nil && *info.DestructiveHint {
		return false, fmt.Sprintf(
			"tool %q is annotated destructive and server %q is configured read_only. "+
				"Make changes with ws_edit/ws_write instead.", info.Name, cfg.Name)
	}
	if info.ReadOnlyHint != nil && *info.ReadOnlyHint {
		return true, ""
	}
	return false, fmt.Sprintf(
		"tool %q is not annotated read-only and server %q is configured read_only, so it is refused. "+
			"Use a read-only tool from this server, or ask the operator to add %q to allow_tools.",
		info.Name, cfg.Name, info.Name)
}

// Call invokes an MCP tool, enforcing the read-only policy.
func (m *Manager) Call(ctx context.Context, server, tool string, args map[string]interface{}) (string, error) {
	server = strings.TrimSpace(server)
	tool = strings.TrimSpace(tool)
	if server == "" || tool == "" {
		return "", fmt.Errorf("both server and tool are required")
	}
	m.mu.Lock()
	c := m.clients[server]
	var names []string
	for n := range m.clients {
		names = append(names, n)
	}
	m.mu.Unlock()
	if c == nil {
		sort.Strings(names)
		if len(names) == 0 {
			return "", fmt.Errorf("no MCP servers are connected")
		}
		return "", fmt.Errorf("mcp server %q is not connected (connected: %s)", server, strings.Join(names, ", "))
	}
	info, known := c.toolInfo(tool)
	if !known {
		info = ToolInfo{Server: server, Name: tool}
	}
	if ok, why := IsToolAllowed(c.cfg, info, known); !ok {
		return "", fmt.Errorf("%w: %s", ErrToolNotAllowed, why)
	}
	out, err := c.callTool(ctx, tool, args)
	if err != nil {
		return "", err
	}
	return capResult(out), nil
}

// capResult bounds an MCP payload; MCP results had no size cap at all.
func capResult(s string) string {
	if len(s) <= MaxResultBytes {
		return s
	}
	head := MaxResultBytes * 2 / 3
	tail := MaxResultBytes - head
	return s[:head] +
		fmt.Sprintf("\n…[%d bytes truncated — ask the MCP tool for a narrower query]…\n", len(s)-head-tail) +
		s[len(s)-tail:]
}

func (c *client) toolInfo(name string) (ToolInfo, bool) {
	c.toolsMu.RLock()
	defer c.toolsMu.RUnlock()
	t, ok := c.tools[name]
	return t, ok
}

func (m *Manager) start(ctx context.Context, sc ServerConfig) (*client, error) {
	c := &client{cfg: sc, tools: map[string]ToolInfo{}}
	if sc.URL != "" {
		c.httpc = &http.Client{Timeout: DefaultHTTPTimeout}
		return c, nil
	}
	if sc.Command == "" {
		return nil, fmt.Errorf("command or url required")
	}
	cmd := exec.CommandContext(ctx, sc.Command, sc.Args...)
	if len(sc.Env) > 0 {
		// cmd.Env was REASSIGNED inside the loop, so only the last variable
		// survived. Build the environment once, then append.
		env := os.Environ()
		for k, v := range sc.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	c.startReader()
	// initialize
	_, err = c.request(ctx, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "slmcode", "version": "0.1"},
	})
	if err != nil {
		_ = c.close()
		return nil, err
	}
	_ = c.notify(ctx, "notifications/initialized", map[string]interface{}{})
	return c, nil
}

func (c *client) listTools(ctx context.Context) ([]ToolInfo, error) {
	raw, err := c.request(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
			Annotations *struct {
				ReadOnlyHint    *bool `json:"readOnlyHint"`
				DestructiveHint *bool `json:"destructiveHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	var out []ToolInfo
	index := map[string]ToolInfo{}
	for _, t := range resp.Tools {
		info := ToolInfo{
			Server: c.cfg.Name, Name: t.Name,
			Description: t.Description, InputSchema: t.InputSchema,
		}
		if t.Annotations != nil {
			info.ReadOnlyHint = t.Annotations.ReadOnlyHint
			info.DestructiveHint = t.Annotations.DestructiveHint
		}
		out = append(out, info)
		index[t.Name] = info
	}
	c.toolsMu.Lock()
	c.tools = index
	c.toolsMu.Unlock()
	return out, nil
}

func (c *client) callTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	raw, err := c.request(ctx, "tools/call", map[string]interface{}{
		"name": name, "arguments": args,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// rpcResponse is one line of the stdio JSON-RPC stream.
type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *client) request(ctx context.Context, method string, params map[string]interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	}
	body, _ := json.Marshal(req)
	if c.cfg.URL != "" {
		return c.httpRequest(ctx, body)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil || c.stdout == nil || c.lines == nil {
		return nil, fmt.Errorf("mcp %s: server is not running", c.cfg.Name)
	}
	if _, err := c.stdin.Write(append(body, '\n')); err != nil {
		return nil, err
	}
	// Read until a matching result. ReadBytes blocks indefinitely, so the read
	// happens on the client's persistent pump goroutine and the deadline is
	// enforced by select here — before, the 30s deadline was decorative and a
	// silent server hung the agent while HOLDING c.mu, blocking every other
	// request too.
	deadline := time.NewTimer(DefaultRequestTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("mcp %s: timeout after %s on %s", c.cfg.Name, DefaultRequestTimeout, method)
		case err := <-c.readErr:
			return nil, fmt.Errorf("mcp %s: %w", c.cfg.Name, err)
		case line := <-c.lines:
			var resp rpcResponse
			if err := json.Unmarshal(line, &resp); err != nil {
				continue // notification / log noise
			}
			if resp.Error != nil {
				return nil, fmt.Errorf("mcp: %s", resp.Error.Message)
			}
			if len(resp.Result) > 0 {
				return resp.Result, nil
			}
		}
	}
}

func (c *client) notify(_ context.Context, method string, params map[string]interface{}) error {
	if c.cfg.URL != "" || c.stdin == nil {
		return nil
	}
	req := map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params}
	body, _ := json.Marshal(req)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.stdin.Write(append(body, '\n'))
	return err
}

func (c *client) httpRequest(ctx context.Context, body []byte) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.httpc
	if hc == nil {
		hc = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Bound the body: a hostile or buggy server must not be able to OOM us.
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResultBytes*8))
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return data, nil
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("mcp: %s", envelope.Error.Message)
	}
	return envelope.Result, nil
}
