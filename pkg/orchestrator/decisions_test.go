package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

type decisionCapture struct{ requests []ggagent.SubAgentRequest }

func (e *decisionCapture) ExecuteSubAgents(_ context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	e.requests = append([]ggagent.SubAgentRequest(nil), reqs...)
	return []ggagent.SubAgentResult{{Output: "unchanged result"}}, nil
}

func TestDecisionGuidanceDispatchAndFallback(t *testing.T) {
	var calls atomic.Int32
	var failing atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if failing.Load() {
			w.WriteHeader(503)
			return
		}
		_, _ = fmt.Fprint(w, `{"answers":{"relevant":{"type":"noul","noul":0.95}}}`)
	}))
	defer srv.Close()
	cfg := config.Default(t.TempDir())
	cfg.LayaEndpoint = srv.URL
	capture := &decisionCapture{}
	o := &Orchestrator{cfg: cfg, executor: capture}
	original := []ggagent.SubAgentRequest{{AgentID: "reviewer", TaskID: "task-1", Input: "review actual disk evidence", ShareState: true}}
	before := append([]ggagent.SubAgentRequest(nil), original...)
	for _, enabled := range []bool{false, true} {
		cfg.LayaGuidance = enabled
		_, err := o.gatedExecutor().ExecuteSubAgents(context.Background(), original, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := capture.requests[0]
		if got.AgentID != original[0].AgentID || got.TaskID != original[0].TaskID || got.ShareState != original[0].ShareState {
			t.Fatal("advice changed dispatch policy")
		}
		if strings.Contains(got.Input, "Optional decision-model hint") != enabled {
			t.Fatal("opt-in not respected")
		}
	}
	if calls.Load() != 1 || !reflect.DeepEqual(original, before) {
		t.Fatal("disabled call or caller mutation")
	}
	failing.Store(true)
	_, err := o.gatedExecutor().ExecuteSubAgents(context.Background(), original, nil)
	if err != nil || capture.requests[0].Input != original[0].Input {
		t.Fatalf("fallback failed: %v", err)
	}
}
