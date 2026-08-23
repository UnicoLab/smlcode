package loop

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// LoopEvent is the structured live event emitted by the inner loop.
//
// The legacy AgentEvent callback cannot carry Level or Data, which is why a
// failing agent used to render green in the CLI (Event.Level was never set on
// agent_end) and why token-by-token streaming had nowhere to go. Consumers that
// want either set OnEventFull; OnEvent keeps working unchanged for the rest.
type LoopEvent struct {
	Kind    string
	Agent   string
	TaskID  string
	Message string
	Scope   string
	Output  string
	// Level is one of the stream.Level* constants. It is ALWAYS set on
	// agent_end so the CLI can color the icon and count ✔/✖ correctly.
	Level string
	// Data carries typed payloads (stream.Token for KindToken).
	Data any
}

// StructuredEvent is the richer event sink.
type StructuredEvent func(ev LoopEvent)

// fireEvent dispatches to OnEventFull when set, always mirroring to the legacy
// OnEvent sink so existing wiring keeps receiving everything.
func (r *Runner) fireEvent(ev LoopEvent) {
	if r == nil {
		return
	}
	if ev.Level == "" {
		ev.Level = levelForKind(ev.Kind)
	}
	if r.OnEventFull != nil {
		r.OnEventFull(ev)
	}
	if r.OnEvent != nil {
		r.OnEvent(ev.Kind, ev.Agent, ev.TaskID, ev.Message, ev.Scope, ev.Output)
	}
}

// levelForKind is the default severity for an event kind.
func levelForKind(kind string) string {
	switch kind {
	case stream.KindIntervention:
		return stream.LevelWarn
	case stream.KindDebug:
		return stream.LevelInfo
	default:
		return stream.LevelInfo
	}
}

// fire emits an event with a level inferred from the message text. Kept for the
// many call sites that have no strong opinion; prefer fireLevel on agent_end.
func (r *Runner) fire(kind, agent, taskID, msg, scope, output string) {
	level := ""
	if kind == stream.KindAgentEnd {
		level = inferEndLevel(msg)
	}
	r.fireEvent(LoopEvent{
		Kind: kind, Agent: agent, TaskID: taskID,
		Message: msg, Scope: scope, Output: output, Level: level,
	})
}

// fireLevel emits an event with an explicit level.
func (r *Runner) fireLevel(kind, agent, taskID, msg, scope, output, level string) {
	r.fireEvent(LoopEvent{
		Kind: kind, Agent: agent, TaskID: taskID,
		Message: msg, Scope: scope, Output: output, Level: level,
	})
}

// endLevelNeedles maps agent_end phrasing to a severity so a failing agent is
// never rendered as a success.
var endLevelNeedles = []struct {
	needle string
	level  string
}{
	{"timed out", stream.LevelError},
	{"error", stream.LevelError},
	{"failed", stream.LevelProblem},
	{"rejected", stream.LevelProblem},
	{"blocked", stream.LevelProblem},
	{"interrupted", stream.LevelWarn},
	{"skipped", stream.LevelWarn},
}

// inferEndLevel classifies an agent_end message. Anything not recognized as a
// problem is a success — agent_end only fires when a unit of work finished.
func inferEndLevel(msg string) string {
	lower := strings.ToLower(msg)
	for _, n := range endLevelNeedles {
		if strings.Contains(lower, n.needle) {
			return n.level
		}
	}
	if strings.Contains(lower, "approved=false") {
		return stream.LevelProblem
	}
	return stream.LevelSuccess
}

// EmitToken publishes one incremental model-output delta.
//
// The loop itself never sees raw stream chunks: ggagent.SubAgentRequest has no
// per-token callback, so the executor (or the backend wrapper the orchestrator
// installs) must call this — use TokenSink to get a bound emitter.
func (r *Runner) EmitToken(agent, taskID, delta string, tokens int) {
	if r == nil || delta == "" {
		return
	}
	r.fireEvent(LoopEvent{
		Kind: stream.KindToken, Agent: agent, TaskID: taskID,
		Message: delta, Level: stream.LevelInfo,
		Data: stream.Token{Delta: delta, Tokens: tokens},
	})
}

// TokenSink returns an emitter bound to one agent/task, suitable for handing to
// an LLM streaming callback.
func (r *Runner) TokenSink(agent, taskID string) func(delta string, tokens int) {
	return func(delta string, tokens int) { r.EmitToken(agent, taskID, delta, tokens) }
}

// roleReviewerStrict is the built-in strict second reviewer used by the
// speculative review race when capacity allows. It aliases the agents package
// so the id cannot drift from the registered role again.
const roleReviewerStrict = agents.RoleReviewerStrict

// logf is the nil-safe progress log. The Log field is optional (zero-value
// Runners are common in tests and in embedding callers), so nothing in this
// package may call it directly.
func (r *Runner) logf(format string, args ...interface{}) {
	if r == nil || r.Log == nil {
		return
	}
	r.Log(format, args...)
}
