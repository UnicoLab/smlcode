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
		r.logf("%s react checkpoint saved (%d messages, iter=%d)", taskID, len(cp.Messages), cp.Iteration)
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
			r.logf("%s finalize-steer: ~%d turns left", taskID, remaining)
		}
	}
	if r.Log != nil {
		r.logf("%s resuming ReAct with %d messages (iter=%d) — no cold replan", taskID, len(req.Messages), req.Iteration)
	}
	return true
}

// reactCompactMinMessages is the floor below which compaction never triggers.
const reactCompactMinMessages = 10

// maybeCompactReact shrinks an oversized ReAct transcript.
//
// Three things were wrong with it:
//
//  1. The window was WindowTokensFromKB(MaxContextKB), which reports a 32K
//     model as having a 4096-token window — so the watchdog believed a run was
//     at 80% capacity when it was at 10%. It now uses the model's real
//     ContextLimit via compact.WindowTokensFor.
//  2. Compaction dropped straight to summarization. Deterministic elision of
//     old tool RESULTS (keeping every tool CALL, so no pair is orphaned) costs
//     no inference and is measured to beat summarization, so it is tried first
//     and summarization only runs if usage is still over threshold.
//  3. The kept tail was msgs[len-8:], which routinely began on a role:"tool"
//     message; every OpenAI-compatible server rejects that with HTTP 400.
//     compact.CompactChatMessagesWithDigest now picks the slice point with
//     SafeKeepStart, and the digest is the structured MustPreserve schema
//     rather than a 160-character one-liner.
func (r *Runner) maybeCompactReact(msgs []session.ReactMessage, agentID string, iteration int) []session.ReactMessage {
	if !r.ReactCompact || len(msgs) < reactCompactMinMessages {
		return msgs
	}
	window := compact.WindowTokensFor(r.ContextLimitTokens, r.MaxContextKB)
	usage := reactUsagePercent(msgs, window)

	r.reactWatchMu.Lock()
	defer r.reactWatchMu.Unlock()
	pct := r.ReactCompactAtPercent
	if pct <= 0 {
		pct = compact.DefaultCompactAtPercent
	}
	if r.reactWatch == nil {
		r.reactWatch = compact.NewWatchdog(pct)
	}
	r.reactWatch.MaybeRearm(usage)
	if !r.reactWatch.ShouldCompact(usage) {
		return msgs
	}

	// Step 1 — deterministic elision of old observations. No inference.
	out, elided := elideReactToolResults(msgs, compact.DefaultElideKeepLast)
	if elided > 0 {
		postUsage := reactUsagePercent(out, window)
		r.logf("%s react-elide: %d old tool result(s) collapsed (usage %.0f%%→%.0f%%)",
			agentID, elided, usage, postUsage)
		if postUsage < float64(pct) {
			r.reactWatch.RecordPostCompact(postUsage)
			return out
		}
		msgs, usage = out, postUsage
	}

	// Step 2 — structured summarization of the prefix.
	keep := reactKeepLast(iteration)
	chat := sessionToChat(msgs)
	compacted, ok := compact.CompactChatMessagesWithDigest(chat, keep, compact.DefaultDigestBytes)
	if !ok {
		return msgs
	}
	postTokens := compact.EstimateTokens(compact.MessagesBytes(compacted))
	postUsage := compact.UsagePercent(postTokens, window)
	r.reactWatch.RecordPostCompact(postUsage)
	r.logf("%s react-compact: %d→%d msgs (usage %.0f%%→%.0f%%, window=%d tok)",
		agentID, len(msgs), len(compacted), usage, postUsage, window)
	return chatToSession(compacted)
}

// CompactLiveMessages is the LIVE per-iteration compaction entry point.
//
// pkg/loop cannot install itself inside the ReAct loop: ggagent's
// SubAgentRequest carries no per-iteration callback and its Middleware
// interface is BeforeRun/AfterRun only, so nothing in this package ever sees
// iteration N of a 16-iteration worker. maybeCompactReact therefore had exactly
// one call site — restoreReact, the resume path — and a live worker appending a
// whole file per tool result compacted never, even though react_compact:true is
// the default and pkg/readiness tells the operator they are protected.
//
// This method is the policy; the executor is the call site. Hand it to whatever
// runs the ReAct loop and call it once per iteration with the live transcript.
func (r *Runner) CompactLiveMessages(agentID string, iteration int, msgs []llm.Message) []llm.Message {
	if r == nil || !r.ReactCompact || len(msgs) < reactCompactMinMessages {
		return msgs
	}
	return fromSessionMessages(r.maybeCompactReact(toSessionMessages(msgs), agentID, iteration))
}

// reactKeepLast is how many trailing messages compaction aims to keep.
func reactKeepLast(iteration int) int {
	if iteration > 0 && iteration < 4 {
		return 10
	}
	return 8
}

// reactUsagePercent estimates context usage for a transcript.
func reactUsagePercent(msgs []session.ReactMessage, window int) float64 {
	return compact.UsagePercent(compact.EstimateTokens(compact.MessagesBytes(sessionToChat(msgs))), window)
}

// elideReactToolResults replaces the content of all but the last keepLast tool
// RESULTS with a placeholder, leaving every tool CALL intact.
func elideReactToolResults(msgs []session.ReactMessage, keepLast int) ([]session.ReactMessage, int) {
	return compact.ElideOldToolResultsFunc(msgs, keepLast, compact.DefaultElidedPlaceholder,
		func(m session.ReactMessage) bool { return strings.EqualFold(m.Role, compact.RoleTool) },
		func(m session.ReactMessage, p string) session.ReactMessage { m.Content = p; return m })
}

// sessionToChat converts a checkpoint transcript to compaction messages,
// carrying the tool-call linkage. Flattening ToolCalls into a "[tools:ws_edit]"
// text suffix (the old behavior) destroyed ToolCallID/Name/ToolCalls, so a
// compacted transcript could never be restored into a valid request.
func sessionToChat(msgs []session.ReactMessage) []compact.ChatMsg {
	out := make([]compact.ChatMsg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, compact.ChatMsg{
			Role: m.Role, Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallID,
			ToolCalls: toCompactToolCalls(m.ToolCalls),
		})
	}
	return out
}

// chatToSession is the inverse of sessionToChat.
func chatToSession(msgs []compact.ChatMsg) []session.ReactMessage {
	out := make([]session.ReactMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, session.ReactMessage{
			Role: m.Role, Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallID,
			ToolCalls: fromCompactToolCalls(m.ToolCalls),
		})
	}
	return out
}

func toCompactToolCalls(calls []session.ReactToolCall) []compact.ToolCallRef {
	if len(calls) == 0 {
		return nil
	}
	out := make([]compact.ToolCallRef, 0, len(calls))
	for _, c := range calls {
		out = append(out, compact.ToolCallRef{
			ID: c.ID, Type: c.Type, Name: c.Name, Arguments: c.Arguments,
		})
	}
	return out
}

func fromCompactToolCalls(calls []compact.ToolCallRef) []session.ReactToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]session.ReactToolCall, 0, len(calls))
	for _, c := range calls {
		typ := c.Type
		if typ == "" {
			typ = "function"
		}
		out = append(out, session.ReactToolCall{
			ID: c.ID, Type: typ, Name: c.Name, Arguments: c.Arguments,
		})
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
	if err != nil && (errors.Is(err, context.Canceled) ||
		strings.Contains(strings.ToLower(err.Error()), "canceled") ||
		strings.Contains(strings.ToLower(err.Error()), "cancelled")) { //nolint:misspell // matches the provider error text verbatim; some servers spell it "cancelled"
		return true
	}
	if res.Error != nil {
		e := res.Error.Error()
		lower := strings.ToLower(e)
		return strings.Contains(lower, "canceled") || strings.Contains(lower, "cancelled") || //nolint:misspell // matches the provider error text verbatim; some servers spell it "cancelled"
			errors.Is(res.Error, context.Canceled)
	}
	return false
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "deadline") || strings.Contains(lower, "timeout")
}

func isTimeoutResult(err error, res ggagent.SubAgentResult) bool {
	if isTimeoutErr(res.Error) {
		return true
	}
	return res.Error == nil && strings.TrimSpace(outputString(res)) == "" && isTimeoutErr(err)
}

func timeoutErr(err error, res ggagent.SubAgentResult) error {
	if isTimeoutErr(res.Error) {
		return res.Error
	}
	if isTimeoutErr(err) {
		return err
	}
	return context.DeadlineExceeded
}
