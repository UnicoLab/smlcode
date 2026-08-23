package mcp

import "strings"

// ServerStatus is a connected (or configured) MCP server snapshot.
type ServerStatus struct {
	Name      string   `json:"name"`
	Connected bool     `json:"connected"`
	Transport string   `json:"transport"` // stdio | http
	ReadOnly  bool     `json:"read_only"`
	Tools     []string `json:"tools,omitempty"`
	ToolCount int      `json:"tool_count"`
	Error     string   `json:"error,omitempty"`
}

// StatusReport is GET /api/mcp payload.
type StatusReport struct {
	Enabled    bool           `json:"enabled"`
	MetaTool   string         `json:"meta_tool"` // always mcp_call
	Pattern    string         `json:"pattern"`
	Servers    []ServerStatus `json:"servers"`
	TotalTools int            `json:"total_tools"`
	Configured int            `json:"configured"`
}

// Status returns connected MCP servers and discovered tools (skill-gated meta-tool pattern).
func (m *Manager) Status(infos []ToolInfo) StatusReport {
	out := StatusReport{
		MetaTool: "mcp_call",
		Pattern:  "single meta-tool mcp_call — do not explode one tool per MCP capability",
		Servers:  []ServerStatus{},
	}
	if m == nil {
		return out
	}
	out.Enabled = len(m.Servers) > 0
	out.Configured = len(m.Servers)

	byServer := map[string][]string{}
	for _, t := range infos {
		byServer[t.Server] = append(byServer[t.Server], t.Name)
		out.TotalTools++
	}

	m.mu.Lock()
	clients := m.clients
	m.mu.Unlock()

	seen := map[string]bool{}
	for _, sc := range m.Servers {
		name := strings.TrimSpace(sc.Name)
		if name == "" {
			continue
		}
		seen[name] = true
		st := ServerStatus{
			Name:     name,
			ReadOnly: sc.IsReadOnly(),
			Tools:    byServer[name],
		}
		st.ToolCount = len(st.Tools)
		if sc.URL != "" {
			st.Transport = "http"
		} else {
			st.Transport = "stdio"
		}
		if clients != nil {
			_, st.Connected = clients[name]
		}
		out.Servers = append(out.Servers, st)
	}
	// Connected but not in config list (shouldn't happen) — still surface.
	for name, tools := range byServer {
		if seen[name] {
			continue
		}
		out.Servers = append(out.Servers, ServerStatus{
			Name: name, Connected: true, Tools: tools, ToolCount: len(tools),
			Transport: "unknown", ReadOnly: true,
		})
	}
	return out
}
