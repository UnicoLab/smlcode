package mcp

import "testing"

func TestStatusEmpty(t *testing.T) {
	m := &Manager{}
	st := m.Status(nil)
	if st.MetaTool != "mcp_call" || st.Enabled {
		t.Fatalf("%+v", st)
	}
}

func TestStatusWithServers(t *testing.T) {
	m := &Manager{
		Servers: []ServerConfig{{Name: "docs", ReadOnly: true, Command: "echo"}},
		clients: map[string]*client{"docs": {}},
	}
	st := m.Status([]ToolInfo{{Server: "docs", Name: "search"}})
	if !st.Enabled || st.TotalTools != 1 || len(st.Servers) != 1 {
		t.Fatalf("%+v", st)
	}
	if !st.Servers[0].Connected || st.Servers[0].ToolCount != 1 {
		t.Fatalf("%+v", st.Servers[0])
	}
}
