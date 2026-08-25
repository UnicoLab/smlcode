package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// MaxLoopCorrections caps consecutive repeated-call refusals (little-coder #81).
const MaxLoopCorrections = 2

// MaxRepeatNudges is how many times the harness will ANSWER one repeated call
// with corrective TEXT before it stops answering and takes the tool away.
//
// One. The first repeat gets a nudge because a model deserves to be told. A
// model that repeats the SAME call after being told has demonstrated that the
// text does not work, and repeating the text is not a different intervention —
// measured live on a 9B: two identical ws calls, two identical nudges, the
// model paraphrasing the refusal back ("The tool call was rejected because I
// repeated the same…") and immediately re-issuing the call. The old ladder made
// this worse than it looks: its escalation was keyed on `consecutive`, which
// needed a THIRD refusal to fire and was reset to zero by any unrelated call
// that happened to succeed, so an alternating A,B,A,B loop never escalated at
// all.
const MaxRepeatNudges = 1

// ReasonToolWithdrawn prefixes the intervention reason reported when a tool is
// withdrawn from a task. The tool name follows the colon.
const ReasonToolWithdrawn = "tool_withdrawn"

// ReasonRepeatedToolCall is the verdict reason for a verbatim repeat.
const ReasonRepeatedToolCall = "repeated_tool_call"

// DefaultLoopHistory is how many recent calls one task's history keeps.
const DefaultLoopHistory = 12

// taskIDKey carries the owning task/agent id through context so a shared
// CallTracker can keep per-task histories.
type taskIDKey struct{}

// WithTaskID tags ctx with the task (or agent) that owns the tool calls made
// under it. The orchestrator MUST set this before running a task, otherwise
// every parallel worker shares the "" bucket and they trip each other's loop
// detection — two workers legitimately reading go.mod used to hard-stop each
// other at max_parallel: 4.
func WithTaskID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, taskIDKey{}, id)
}

// TaskIDFrom returns the task id carried by ctx ("" when unset).
func TaskIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(taskIDKey{}).(string); ok {
		return v
	}
	return ""
}

// stateChangingTools mutate the workspace, so an identical later call can
// legitimately return something new.
var stateChangingTools = map[string]bool{
	"ws_edit": true, "ws_write": true, "ws_patch": true, "ws_mv": true,
	"ws_delete": true, "ws_shell": true, "bash": true, "edit": true, "write": true,
}

type trackedCall struct {
	name        string
	args        string
	stateChange bool
}

type taskHistory struct {
	calls       []trackedCall
	consecutive int
	// refusals counts how often each EXACT call was refused. Unlike
	// consecutive it is not cleared by an unrelated successful call, so an
	// alternating A,B,A,B loop escalates on A's second refusal instead of
	// resetting the ladder every other turn.
	refusals map[string]int
	// withdrawn holds the tools this task may no longer use at all. A
	// withdrawn tool does not run for ANY arguments — that is the point: the
	// model cannot keep the loop alive by tweaking the path or the pattern.
	// Real progress (a state-changing call that actually executed) hands them
	// all back, so a corrector that starts by editing is never starved.
	withdrawn map[string]bool
}

func (h *taskHistory) refuse(key string) int {
	if h.refusals == nil {
		h.refusals = map[string]int{}
	}
	h.refusals[key]++
	return h.refusals[key]
}

// progressed clears the loop state a real state change makes obsolete.
func (h *taskHistory) progressed() {
	h.refusals = nil
	h.withdrawn = nil
}

func (h *taskHistory) withdraw(tool string) {
	if h.withdrawn == nil {
		h.withdrawn = map[string]bool{}
	}
	h.withdrawn[tool] = true
}

// CallTracker breaks mid-ReAct tool loops by refusing verbatim repeated tool
// calls. History is kept PER TASK and reset at task start.
type CallTracker struct {
	mu             sync.Mutex
	tasks          map[string]*taskHistory
	known          map[string]bool
	MaxHistory     int
	MaxCorrect     int
	OnIntervention func(reason, message string)
}

// NewCallTracker returns a tracker with known ws_* tools.
func NewCallTracker() *CallTracker {
	return &CallTracker{
		MaxHistory: DefaultLoopHistory,
		MaxCorrect: MaxLoopCorrections,
		tasks:      map[string]*taskHistory{},
		known: map[string]bool{
			"ws_read": true, "ws_write": true, "ws_edit": true, "ws_patch": true,
			"ws_shell": true, "ws_glob": true, "ws_grep": true, "ws_list": true,
			"ws_mv": true, "ws_delete": true, "ws_todo": true,
			"git_status": true, "git_diff": true, "mcp_call": true,
		},
	}
}

// ResetTask clears one task's history. Call at task/wave/turn start so a fresh
// attempt is never judged against the previous attempt's calls.
func (t *CallTracker) ResetTask(id string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tasks, strings.TrimSpace(id))
}

// ResetAll clears every task history (new run).
func (t *CallTracker) ResetAll() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tasks = map[string]*taskHistory{}
}

// historyFor returns (creating if needed) the bucket for a task id.
// Caller holds t.mu.
func (t *CallTracker) historyFor(id string) *taskHistory {
	if t.tasks == nil {
		t.tasks = map[string]*taskHistory{}
	}
	h := t.tasks[id]
	if h == nil {
		h = &taskHistory{}
		t.tasks[id] = h
	}
	return h
}

// LoopVerdict is the tracker's decision about one incoming call.
type LoopVerdict struct {
	Refuse bool
	Reason string
}

// assess decides whether call should be refused, given this task's history.
// Caller holds t.mu.
func (t *CallTracker) assess(h *taskHistory, c trackedCall) LoopVerdict {
	if strings.TrimSpace(c.name) == "" {
		return LoopVerdict{Refuse: true, Reason: "empty_tool_name"}
	}
	if len(t.known) > 0 && !t.known[c.name] {
		return LoopVerdict{Refuse: true, Reason: "unknown_tool:" + c.name}
	}
	if strings.Contains(c.args, `"_raw"`) {
		return LoopVerdict{Refuse: true, Reason: "malformed_args:" + c.name}
	}
	// Find the LAST identical earlier call in this task.
	last := -1
	for i := len(h.calls) - 1; i >= 0; i-- {
		if h.calls[i].name == c.name && h.calls[i].args == c.args {
			last = i
			break
		}
	}
	if last < 0 {
		return LoopVerdict{}
	}
	// The environment only counts as "changed" when the change happened AFTER
	// the earlier identical call. The old global check accepted any
	// state-changing call anywhere in the shared 12-entry history, which
	// disabled the guard exactly when a model was looping.
	for _, prev := range h.calls[last+1:] {
		if prev.stateChange {
			return LoopVerdict{}
		}
	}
	return LoopVerdict{Refuse: true, Reason: ReasonRepeatedToolCall}
}

// escalate decides what a refusal DOES, beyond what it says.
//
// The guard already refuses the duplicate at the tool layer, so the call never
// runs — but until this existed that was the whole of the intervention: every
// subsequent repeat got another paragraph of advice, and a model that ignores
// advice is exactly the model this fires for. Rung two therefore stops being a
// message and starts being a change to what the task CAN do: the tool is
// withdrawn, for every argument, until the task makes a real state change.
//
// State-changing tools are never withdrawn. An editing task that loses ws_edit
// cannot be completed by any strategy, and a repeat there is already bounded —
// varying old_str is a genuinely different attempt, and the exact call that
// failed stays refused either way. Those get the terminal finish directive
// instead, on the second repeat rather than the third.
//
// Caller holds t.mu.
func (t *CallTracker) escalate(h *taskHistory, c trackedCall, verdict LoopVerdict) (string, string) {
	reason := verdict.Reason
	if reason != ReasonRepeatedToolCall {
		return reason, "QUALITY MONITOR: " + LoopCorrectionMessage(reason)
	}
	repeats := h.refuse(c.name + "|" + c.args)
	max := t.MaxCorrect
	if max <= 0 {
		max = MaxLoopCorrections
	}
	if repeats <= MaxRepeatNudges && h.consecutive <= max {
		return reason, "QUALITY MONITOR: " + LoopCorrectionMessage(reason)
	}
	if c.stateChange {
		return reason, hardStopMessage()
	}
	h.withdraw(c.name)
	return ReasonToolWithdrawn + ":" + c.name, withdrawnMessage(c.name)
}

// Wrap returns a ToolExecutor that refuses verbatim loops / unknown tools.
func (t *CallTracker) Wrap(name string, fn tools.ToolExecutor) tools.ToolExecutor {
	if t == nil || fn == nil {
		return fn
	}
	return func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		taskID := TaskIDFrom(ctx)
		c := trackedCall{
			name:        name,
			args:        mustJSON(cloneArgs(args)),
			stateChange: stateChangingTools[strings.ToLower(name)],
		}
		t.mu.Lock()
		h := t.historyFor(taskID)
		// A withdrawn tool answers nothing, whatever the arguments. This is
		// the rung that is not a message: the stuck strategy is no longer
		// available to the model at all.
		if h.withdrawn[name] {
			h.consecutive++
			out := withdrawnMessage(name)
			cb := t.OnIntervention
			t.mu.Unlock()
			if cb != nil {
				cb(ReasonToolWithdrawn+":"+name, out)
			}
			return out, nil
		}
		verdict := t.assess(h, c)
		if verdict.Refuse {
			h.consecutive++
			reason, out := t.escalate(h, c, verdict)
			cb := t.OnIntervention
			t.mu.Unlock()
			if cb != nil {
				cb(reason, out)
			}
			return out, nil
		}
		h.consecutive = 0
		t.mu.Unlock()

		out, err := fn(ctx, args)

		t.mu.Lock()
		defer t.mu.Unlock()
		h = t.historyFor(taskID)
		h.calls = append(h.calls, c)
		if c.stateChange {
			// Real progress: the same read can now legitimately return
			// something new, so the loop state that was built up against it is
			// obsolete. This is what keeps a withdrawal from outliving the
			// loop it broke — a corrector that opens with an edit starts clean.
			h.progressed()
		}
		if t.MaxHistory > 0 && len(h.calls) > t.MaxHistory {
			h.calls = h.calls[len(h.calls)-t.MaxHistory:]
		}
		return out, err
	}
}

// hardStopMessage is the terminal directive for a tool that cannot be withdrawn.
func hardStopMessage() string {
	return "QUALITY MONITOR HARD STOP: repeated the same tool call " +
		"too many times. Stop calling tools. Finish NOW with STRICT JSON: " +
		`{"status":"done|blocked","summary":"…","files_changed":[],"notes":""}`
}

// withdrawnMessage tells the model the tool is gone, not merely discouraged.
// It says so explicitly, because "try different arguments" is the move this
// rung exists to remove.
func withdrawnMessage(tool string) string {
	return "QUALITY MONITOR HARD STOP: " + tool + " is now DISABLED for this task — you " +
		"called it repeatedly with the same arguments and were told to do something else. " +
		"It will not run again for ANY arguments until you make a real change. " +
		"Use a different tool (ws_edit, ws_patch, ws_write, ws_shell) or finish NOW with " +
		`STRICT JSON: {"status":"done|blocked","summary":"…","files_changed":[],"notes":""}`
}

// LoopCorrectionMessage is steered back to the model on a loop refusal.
// Every branch names the corrective action.
func LoopCorrectionMessage(reason string) string {
	switch {
	case reason == "empty_tool_name":
		return "Your tool call had an empty name. Use a real tool: ws_read, ws_write, " +
			"ws_edit, ws_patch, ws_shell, ws_glob, ws_grep, ws_list, ws_todo."
	case reason == ReasonRepeatedToolCall:
		return "You just made the exact same tool call again with nothing changed in between — " +
			"the result will be identical. Do something DIFFERENT: change the arguments " +
			"(different path/offset/pattern), make an edit, or finish with status JSON. " +
			"This is your ONE warning: repeat it and the tool is disabled for this task."
	case strings.HasPrefix(reason, ReasonToolWithdrawn+":"):
		return withdrawnMessage(strings.TrimPrefix(reason, ReasonToolWithdrawn+":"))
	case strings.HasPrefix(reason, "unknown_tool:"):
		name := strings.TrimPrefix(reason, "unknown_tool:")
		return "Tool '" + name + "' does not exist. Available: ws_read, ws_write, ws_edit, " +
			"ws_patch, ws_shell, ws_glob, ws_grep, ws_list, ws_mv, ws_delete, ws_todo, " +
			"git_status, git_diff."
	case strings.HasPrefix(reason, "malformed_args:"):
		name := strings.TrimPrefix(reason, "malformed_args:")
		return "The arguments for tool '" + name + "' were malformed (not valid JSON). " +
			"Re-issue the call with a proper JSON object, e.g. " +
			`{"path":"pkg/x/y.go","offset":1,"limit":120}.`
	}
	return "Issue detected: " + reason + ". Try a different approach."
}

func cloneArgs(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
