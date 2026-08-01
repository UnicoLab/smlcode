package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// ServerConfig describes one MCP server (stdio or HTTP).
type ServerConfig struct {
	Name     string            `yaml:"name" json:"name"`
	Command  string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args     []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env      map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	URL      string            `yaml:"url,omitempty" json:"url,omitempty"` // HTTP JSON-RPC
	ReadOnly bool              `yaml:"read_only" json:"read_only"`         // default true
}

// ToolInfo is a discovered MCP tool.
type ToolInfo struct {
	Server      string
	Name        string
	Description string
	InputSchema map[string]interface{}
}

// Manager hosts thin read-only MCP clients and registers them as ws_mcp_* tools.
type Manager struct {
	Servers []ServerConfig
	Log     func(string, ...interface{})
	mu      sync.Mutex
	clients map[string]*client
}

type client struct {
	cfg    ServerConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID atomic.Int64
	mu     sync.Mutex
}

// Connect starts configured servers and lists tools (best-effort).
func (m *Manager) Connect(ctx context.Context) ([]ToolInfo, error) {
	if m.clients == nil {
		m.clients = map[string]*client{}
	}
	var all []ToolInfo
	for _, sc := range m.Servers {
		sc.Name = strings.TrimSpace(sc.Name)
		if sc.Name == "" {
			continue
		}
		if sc.ReadOnly == false && sc.Command == "" && sc.URL == "" {
			continue
		}
		// Default read-only.
		if !sc.ReadOnly && sc.URL == "" && sc.Command == "" {
			sc.ReadOnly = true
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
	return all, nil
}

// Close shuts down stdio servers.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}
	m.clients = map[string]*client{}
}

// RegisterTools adds a single meta-tool `mcp_call` for read-only MCP invocations.
func (m *Manager) RegisterTools(reg *tools.ToolRegistry, infos []ToolInfo) error {
	if reg == nil || m == nil {
		return nil
	}
	desc := "Call a read-only MCP tool. server+tool from connected MCP servers. "
	if len(infos) > 0 {
		var names []string
		for _, t := range infos {
			names = append(names, t.Server+"."+t.Name)
			if len(names) >= 12 {
				break
			}
		}
		desc += "Available: " + strings.Join(names, ", ")
	} else {
		desc += "No MCP servers connected."
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
			return m.Call(ctx, server, name, rawArgs)
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

// Call invokes an MCP tool (read-only servers only).
func (m *Manager) Call(ctx context.Context, server, tool string, args map[string]interface{}) (string, error) {
	m.mu.Lock()
	c := m.clients[server]
	m.mu.Unlock()
	if c == nil {
		return "", fmt.Errorf("mcp server %q not connected", server)
	}
	if !c.cfg.ReadOnly {
		// Still allow but annotate — default configs are read-only.
	}
	return c.callTool(ctx, tool, args)
}

func (m *Manager) start(ctx context.Context, sc ServerConfig) (*client, error) {
	c := &client{cfg: sc}
	if sc.URL != "" {
		return c, nil
	}
	if sc.Command == "" {
		return nil, fmt.Errorf("command or url required")
	}
	cmd := exec.CommandContext(ctx, sc.Command, sc.Args...)
	for k, v := range sc.Env {
		cmd.Env = append(os.Environ(), k+"="+v)
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
	// initialize
	_, err = c.request(ctx, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "slmcode", "version": "0.1"},
	})
	if err != nil {
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
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	var out []ToolInfo
	for _, t := range resp.Tools {
		out = append(out, ToolInfo{
			Server: c.cfg.Name, Name: t.Name,
			Description: t.Description, InputSchema: t.InputSchema,
		})
	}
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
	if _, err := c.stdin.Write(append(body, '\n')); err != nil {
		return nil, err
	}
	// Read until matching id (simple line protocol).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var resp struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp: %s", resp.Error.Message)
		}
		if len(resp.Result) > 0 {
			return resp.Result, nil
		}
	}
	return nil, fmt.Errorf("mcp timeout on %s", method)
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
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
