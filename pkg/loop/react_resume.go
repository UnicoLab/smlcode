package loop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/session"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// SubAgentRunner is the executor surface used by the board loop (real or fake).
type SubAgentRunner interface {
	ExecuteSubAgents(ctx context.Context, requests []ggagent.SubAgentRequest, shared *ggagent.SharedState) ([]ggagent.SubAgentResult, error)
}

// ensure *SubAgentExecutor satisfies SubAgentRunner at compile time.
var _ SubAgentRunner = (*ggagent.SubAgentExecutor)(nil)

func (r *Runner) slmDir() string {
	if r.SlmDir != "" {
		return r.SlmDir
	}
	if r.Root == "" {
		return ""
	}
	return filepath.Join(r.Root, ".slmcode")
}

func (r *Runner) loadReact(taskID string) *session.ReactCheckpoint {
	if r.TurnID == "" || taskID == "" {
		return nil
	}
	dir := r.slmDir()
	if dir == "" {
		return nil
	}
	cp, err := session.LoadReactCheckpoint(dir, r.TurnID, taskID)
	if err != nil || cp == nil || len(cp.Messages) == 0 {
		return nil
	}
	return cp
}

func (r *Runner) saveReactFromResult(taskID, agentID string, res ggagent.SubAgentResult) {
	if r.TurnID == "" || taskID == "" || len(res.Messages) == 0 {
		return
	}
	dir := r.slmDir()
	if dir == "" {
		return
	}
	cp := session.ReactCheckpoint{
		SchemaVersion:    session.ReactSchemaVersion,
		TurnID:           r.TurnID,
		TaskID:           taskID,
		AgentID:          agentID,
		Provider:         res.Provider,
		Model:            res.Model,
		Iteration:        res.Iteration,
		Status:           "interrupted",
		Messages:         toSessionMessages(res.Messages),
		PendingToolCalls: toSessionToolCalls(res.PendingToolCalls),
	}
	_ = session.SaveReactCheckpoint(dir, cp)
	if r.Log != nil {
		r.Log("%s react checkpoint saved (%d messages, iter=%d)", taskID, len(cp.Messages), cp.Iteration)
	}
}

func (r *Runner) clearReact(taskID string) {
	if r.TurnID == "" || taskID == "" {
		return
	}
	dir := r.slmDir()
	if dir == "" {
		return
	}
	_ = session.ClearReactCheckpoint(dir, r.TurnID, taskID)
}

func (r *Runner) applyResumeRequest(req *ggagent.SubAgentRequest, taskID string) bool {
	cp := r.loadReact(taskID)
	if cp == nil {
		return false
	}
	req.TaskID = taskID
	req.Resume = true
	req.Messages = fromSessionMessages(cp.Messages)
	req.Iteration = cp.Iteration
	req.PendingToolCalls = fromSessionToolCalls(cp.PendingToolCalls)
	if strings.TrimSpace(req.Input) == "" {
		req.Input = "Continue from the interrupted ReAct step using the restored conversation. Finish pending tools, then return status JSON."
	}
	if r.Log != nil {
		r.Log("%s resuming ReAct with %d messages (iter=%d) — no cold replan", taskID, len(req.Messages), req.Iteration)
	}
	return true
}

func toSessionMessages(msgs []llm.Message) []session.ReactMessage {
	out := make([]session.ReactMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, session.ReactMessage{
			Role: m.Role, Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallID,
			ToolCalls: toSessionToolCalls(m.ToolCalls),
		})
	}
	return out
}

func fromSessionMessages(msgs []session.ReactMessage) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, llm.Message{
			Role: m.Role, Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallID,
			ToolCalls: fromSessionToolCalls(m.ToolCalls),
		})
	}
	return out
}

func toSessionToolCalls(calls []llm.ToolCall) []session.ReactToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]session.ReactToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, session.ReactToolCall{
			ID: c.ID, Type: c.Type, Name: c.Function.Name, Arguments: c.Function.Arguments,
		})
	}
	return out
}

func fromSessionToolCalls(calls []session.ReactToolCall) []llm.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, 0, len(calls))
	for _, c := range calls {
		typ := c.Type
		if typ == "" {
			typ = "function"
		}
		out = append(out, llm.ToolCall{
			ID: c.ID, Type: typ,
			Function: llm.FunctionCall{Name: c.Name, Arguments: c.Arguments},
		})
	}
	return out
}

func isCancelResult(err error, res ggagent.SubAgentResult) bool {
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(strings.ToLower(err.Error()), "canceled") ||
		strings.Contains(strings.ToLower(err.Error()), "cancelled")) {
		return true
	}
	if res.Error != nil {
		e := res.Error.Error()
		lower := strings.ToLower(e)
		return strings.Contains(lower, "canceled") || strings.Contains(lower, "cancelled") ||
			errors.Is(res.Error, context.Canceled)
	}
	return false
}
