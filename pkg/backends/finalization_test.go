package backends

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

func TestFinalizationKeepsRoleContractOnWire(t *testing.T) {
	for _, role := range []string{schema.RoleTester, schema.RoleExplore, schema.RoleReview, schema.RoleWorker} {
		t.Run(role, func(t *testing.T) {
			srv := newFakeServer(t, "json_schema", "json_object")
			m, _ := newManagerFor(t, "openai", srv.endpoint())
			key := BindRole(m, "openai", Directives{Role: role, SchemaRole: role, SerialTools: role != schema.RoleReview, JSONOnly: role == schema.RoleReview})
			spec, _ := schema.For(role)
			raw, _ := json.Marshal(spec.Schema)
			req := llm.CompletionRequest{Model: "fake-model", MaxTokens: 256, Messages: []llm.Message{
				{Role: "system", Content: "Return JSON matching: " + string(raw)},
				{Role: "user", Content: "Verify the task using observed evidence."},
				{Role: "user", Content: graphFinalizeMessage},
			}}
			if _, err := m.Complete(context.Background(), key, req); err != nil {
				t.Fatal(err)
			}
			body, ok := lastBodyWith(srv.seen(), "messages")
			if !ok {
				t.Fatal("no completion")
			}
			msgs := body["messages"].([]any)
			tail := msgs[len(msgs)-1].(map[string]any)["content"].(string)
			if strings.Contains(tail, "done|blocked") {
				t.Fatalf("library's worker template survived: %s", tail)
			}
			for _, k := range schema.RequiredKeys(spec) {
				if !strings.Contains(tail, `"`+k+`"`) {
					t.Fatalf("missing %s contract key %q: %s", role, k, tail)
				}
			}
			if role != schema.RoleWorker && strings.Contains(tail, `"files_changed"`) {
				t.Fatalf("worker contract leaked: %s", tail)
			}
			if req.Messages[2].Content != graphFinalizeMessage {
				t.Fatal("mutated caller's conversation")
			}
		})
	}
}

func TestFinalizationLeavesOrdinaryTaskUntouched(t *testing.T) {
	p := structuredProvider{directives: Directives{SchemaRole: schema.RoleTester}}
	for _, content := range []string{"Finalize now using my custom format", "Task instructions: " + graphFinalizeMessage} {
		req := llm.CompletionRequest{Messages: []llm.Message{{Role: "user", Content: content}}}
		if got := p.shape(req); got.Messages[0].Content != content {
			t.Fatal("rewrote user instructions")
		}
	}
}

// Exercise the dependency itself so a future library change cannot silently
// bypass the exact-tail compatibility fix.
func TestReActTesterFinalizationPreservesContract(t *testing.T) {
	srv := newFakeServer(t, "json_object")
	srv.content = `{"passed":true,"commands":["go test ./..."],"summary":"checks passed","failures":[]}`
	m, _ := newManagerFor(t, "openai", srv.endpoint())
	key := BindRole(m, "openai", Directives{Role: "tester", SchemaRole: schema.RoleTester, SerialTools: true})
	cfg := agent.DefaultAgentConfig()
	cfg.Type = agent.AgentTypeReAct
	cfg.Provider = key
	cfg.Model = "fake-model"
	cfg.MaxIterations = 2
	cfg.SystemPrompt = `Verify the task. Emit {"passed":true,"commands":[],"summary":"evidence","failures":[]}.`
	a := agent.NewAgent(cfg, m, tools.NewToolRegistry())
	if _, err := a.Execute(context.Background(), "Verify the existing checks"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, body := range srv.seen() {
		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) == 0 {
			continue
		}
		tail, _ := msgs[len(msgs)-1].(map[string]any)["content"].(string)
		if !strings.HasPrefix(tail, "Finalize now.") {
			continue
		}
		found = true
		if !strings.Contains(tail, `"passed"`) || strings.Contains(tail, `"files_changed"`) {
			t.Fatalf("wrong real agent finalization: %s", tail)
		}
	}
	if !found {
		t.Fatal("agent finalization was not exercised")
	}
}
