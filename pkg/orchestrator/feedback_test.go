package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// feedbackExec records the input passed to ExecuteSubAgents.
type feedbackExec struct {
	gotInput string
}

func (f *feedbackExec) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest, _ *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	if len(reqs) > 0 {
		f.gotInput = reqs[0].Input
	}
	return []ggagent.SubAgentResult{{Output: `{"ok":true}`}}, nil
}

func TestSetLiveFeedbackRoundTrip(t *testing.T) {
	o := &Orchestrator{cfg: config.Default(t.TempDir())}
	if got := o.SetLiveFeedback("  prefer simpler code  "); got != "prefer simpler code" {
		t.Fatalf("SetLiveFeedback returned %q", got)
	}
	if got := o.LiveFeedback(); got != "prefer simpler code" {
		t.Fatalf("LiveFeedback = %q", got)
	}
	text, at := o.LiveFeedbackInfo()
	if text != "prefer simpler code" {
		t.Fatalf("LiveFeedbackInfo text = %q", text)
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		t.Fatalf("set_at not RFC3339: %q (%v)", at, err)
	}
	if got := o.ClearLiveFeedback(); got != "" {
		t.Fatalf("ClearLiveFeedback returned %q", got)
	}
	if got := o.LiveFeedback(); got != "" {
		t.Fatalf("LiveFeedback after clear = %q", got)
	}
}

func TestSetLiveFeedbackEmptyClears(t *testing.T) {
	o := &Orchestrator{cfg: config.Default(t.TempDir())}
	o.SetLiveFeedback("steer left")
	o.SetLiveFeedback("   ")
	if got := o.LiveFeedback(); got != "" {
		t.Fatalf("expected cleared, got %q", got)
	}
	if _, at := o.LiveFeedbackInfo(); at != "" {
		t.Fatalf("expected empty set_at, got %q", at)
	}
}

func TestLiveFeedbackNilOrchestrator(t *testing.T) {
	var o *Orchestrator
	if got := o.SetLiveFeedback("x"); got != "" {
		t.Fatalf("nil SetLiveFeedback = %q", got)
	}
	if got := o.LiveFeedback(); got != "" {
		t.Fatalf("nil LiveFeedback = %q", got)
	}
	if got := o.ClearLiveFeedback(); got != "" {
		t.Fatalf("nil ClearLiveFeedback = %q", got)
	}
	if text, at := o.LiveFeedbackInfo(); text != "" || at != "" {
		t.Fatalf("nil LiveFeedbackInfo = %q %q", text, at)
	}
}

func TestRunRoleTrackedPrependsLiveFeedback(t *testing.T) {
	fe := &feedbackExec{}
	o := &Orchestrator{
		cfg:      config.Default(t.TempDir()),
		executor: fe,
		shared:   ggagent.NewSharedState(),
	}
	o.SetLiveFeedback("prefer smaller diffs")
	if _, err := o.runRoleTracked(context.Background(), "worker", "T1", "base task prompt"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fe.gotInput, "\n\n## LIVE FEEDBACK FROM USER") {
		t.Fatalf("feedback not prepended: %q", fe.gotInput)
	}
	if !strings.Contains(fe.gotInput, "prefer smaller diffs") {
		t.Fatalf("feedback text missing: %q", fe.gotInput)
	}
	if !strings.HasSuffix(fe.gotInput, "base task prompt") {
		t.Fatalf("original input lost: %q", fe.gotInput)
	}

	// Cleared feedback must not change the input.
	o.ClearLiveFeedback()
	fe.gotInput = ""
	if _, err := o.runRoleTracked(context.Background(), "worker", "T1", "base task prompt"); err != nil {
		t.Fatal(err)
	}
	if fe.gotInput != "base task prompt" {
		t.Fatalf("input changed without feedback: %q", fe.gotInput)
	}
}
