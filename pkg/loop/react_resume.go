package loop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/UnicoLab/slmcode/pkg/quality"
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
	msgs := toSessionMessages(res.Messages)
	msgs = r.maybeCompactReact(msgs, agentID, res.Iteration)
	cp := session.ReactCheckpoint{
		SchemaVersion:    session.ReactSchemaVersion,
		TurnID:           r.TurnID,
		TaskID:           taskID,
		AgentID:          agentID,
		Provider:         res.Provider,
		Model:            res.Model,
		Iteration:        res.Iteration,
		MaxIterations:    roleMaxIter(agentID),
		Status:           "interrupted",
		Messages:         msgs,
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
	msgs := cp.Messages
	agentID := cp.AgentID
	if agentID == "" {
		agentID = req.AgentID
	}
	msgs = r.maybeCompactReact(msgs, agentID, cp.Iteration)
	req.Messages = fromSessionMessages(msgs)
	req.Iteration = cp.Iteration
	req.PendingToolCalls = fromSessionToolCalls(cp.PendingToolCalls)
	if strings.TrimSpace(req.Input) == "" {
		req.Input = "Continue from the interrupted ReAct step using the restored conversation. Finish pending tools, then return status JSON."
	}
	maxIter := cp.MaxIterations
	if maxIter <= 0 {
		maxIter = roleMaxIter(agentID)
	}
	if r.FinalizeWarn && quality.ShouldFinalizeSteer(cp.Iteration, maxIter) {
		remaining := maxIter - cp.Iteration
		steer := quality.FinalizeSteerMessage(remaining)
		req.Input = strings.TrimSpace(req.Input) + "\n\n" + steer
		req.Messages = append(req.Messages, llm.Message{Role: "user", Content: steer})
		if r.Log != nil {
			r.Log("%s finalize-steer: ~%d turns left", taskID, remaining)
		}
	}
	if r.Log != nil {
		r.Log("%s resuming ReAct with %d messages (iter=%d) — no cold replan", taskID, len(req.Messages), req.Iteration)
	}
	return true
}

// maybeCompactReact shrinks oversized ReAct transcripts (little-coder context-watchdog).
func (r *Runner) maybeCompactReact(msgs []session.ReactMessage, agentID string, iteration int) []session.ReactMessage {
	if !r.ReactCompact || len(msgs) < 10 {
		return msgs
	}
	pct := r.ReactCompactAtPercent
	if pct <= 0 {
		pct = compact.DefaultCompactAtPercent
	}
	if r.reactWatch == nil {
		r.reactWatch = compact.NewWatchdog(pct)
	}
	chat := sessionToChat(msgs)
	tokens := compact.EstimateTokens(compact.MessagesBytes(chat))
	window := compact.WindowTokensFromKB(r.MaxContextKB)
	usage := compact.UsagePercent(tokens, window)
	r.reactWatch.MaybeRearm(usage)
	if !r.reactWatch.ShouldCompact(usage) {
		return msgs
	}
	keep := 8
	if iteration > 0 && iteration < 4 {
		keep = 10
	}
	compacted, ok := compact.CompactChatMessages(chat, keep)
	if !ok {
		return msgs
	}
	postTokens := compact.EstimateTokens(compact.MessagesBytes(compacted))
	postUsage := compact.UsagePercent(postTokens, window)
	r.reactWatch.RecordPostCompact(postUsage)
	if r.Log != nil {
		r.Log("%s react-compact: %d→%d msgs (usage %.0f%%→%.0f%%)",
			agentID, len(msgs), len(compacted), usage, postUsage)
	}
	return chatToSession(compacted)
}

func sessionToChat(msgs []session.ReactMessage) []compact.ChatMsg {
	out := make([]compact.ChatMsg, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		if len(m.ToolCalls) > 0 {
			var names []string
			for _, tc := range m.ToolCalls {
				names = append(names, tc.Name)
			}
			content = strings.TrimSpace(content + " [tools:" + strings.Join(names, ",") + "]")
		}
		out = append(out, compact.ChatMsg{Role: m.Role, Content: content})
	}
	return out
}

func chatToSession(msgs []compact.ChatMsg) []session.ReactMessage {
	out := make([]session.ReactMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, session.ReactMessage{Role: m.Role, Content: m.Content})
	}
	return out
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
