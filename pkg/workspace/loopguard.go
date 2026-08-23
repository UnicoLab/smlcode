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
	return LoopVerdict{Refuse: true, Reason: "repeated_tool_call"}
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
		verdict := t.assess(h, c)
		if verdict.Refuse {
			h.consecutive++
			out := "QUALITY MONITOR: " + LoopCorrectionMessage(verdict.Reason)
			max := t.MaxCorrect
			if max <= 0 {
				max = MaxLoopCorrections
			}
			if h.consecutive > max {
				out = "QUALITY MONITOR HARD STOP: repeated the same tool call " +
					"too many times. Stop calling tools. Finish NOW with STRICT JSON: " +
					`{"status":"done|blocked","summary":"…","files_changed":[],"notes":""}`
			}
			cb := t.OnIntervention
			t.mu.Unlock()
			if cb != nil {
				cb(verdict.Reason, out)
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
		if t.MaxHistory > 0 && len(h.calls) > t.MaxHistory {
			h.calls = h.calls[len(h.calls)-t.MaxHistory:]
		}
		return out, err
	}
}

// LoopCorrectionMessage is steered back to the model on a loop refusal.
// Every branch names the corrective action.
func LoopCorrectionMessage(reason string) string {
	switch {
	case reason == "empty_tool_name":
		return "Your tool call had an empty name. Use a real tool: ws_read, ws_write, " +
			"ws_edit, ws_patch, ws_shell, ws_glob, ws_grep, ws_list, ws_todo."
	case reason == "repeated_tool_call":
		return "You just made the exact same tool call again with nothing changed in between — " +
			"the result will be identical. Do something DIFFERENT: change the arguments " +
			"(different path/offset/pattern), make an edit, or finish with status JSON."
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
