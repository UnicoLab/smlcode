package workspace

import (
	"context"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// MaxLoopCorrections caps consecutive repeated-call refusals (little-coder #81).
const MaxLoopCorrections = 2

// CallTracker breaks mid-ReAct tool loops without GoLangGraph hooks by refusing
// verbatim repeated tool calls (quality-monitor port via tool wrappers).
type CallTracker struct {
	mu             sync.Mutex
	history        []quality.ToolCall
	consecutive    int
	known          map[string]bool
	MaxHistory     int
	MaxCorrect     int
	OnIntervention func(reason, message string)
}

// NewCallTracker returns a tracker with known ws_* tools.
func NewCallTracker() *CallTracker {
	return &CallTracker{
		MaxHistory: 12,
		MaxCorrect: MaxLoopCorrections,
		known: map[string]bool{
			"ws_read": true, "ws_write": true, "ws_edit": true, "ws_patch": true,
			"ws_shell": true, "ws_glob": true, "ws_grep": true, "ws_list": true,
			"ws_mv": true, "ws_delete": true, "git_status": true, "git_diff": true,
			"mcp_call": true,
		},
	}
}

// Wrap returns a ToolExecutor that refuses verbatim loops / unknown tools.
func (t *CallTracker) Wrap(name string, fn tools.ToolExecutor) tools.ToolExecutor {
	if t == nil || fn == nil {
		return fn
	}
	return func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		tc := quality.ToolCall{Name: name, Input: cloneArgs(args)}
		t.mu.Lock()
		prev := append([]quality.ToolCall(nil), t.history...)
		assess := quality.AssessResponse("", []quality.ToolCall{tc}, prev, t.known)
		if !assess.OK {
			refuse := assess.Reason == "repeated_tool_call" ||
				assess.Reason == "empty_tool_name" ||
				strings.HasPrefix(assess.Reason, "unknown_tool:") ||
				strings.HasPrefix(assess.Reason, "malformed_args:")
			if refuse {
				t.consecutive++
				msg := quality.CorrectionMessage(assess.Reason)
				out := "QUALITY MONITOR: " + msg
				max := t.MaxCorrect
				if max <= 0 {
					max = MaxLoopCorrections
				}
				if t.consecutive > max {
					out = "QUALITY MONITOR HARD STOP: repeated the same tool call " +
						"too many times. Stop calling tools. Finish NOW with STRICT JSON: " +
						`{"status":"done|blocked","summary":"…","files_changed":[],"notes":""}`
				}
				cb := t.OnIntervention
				t.mu.Unlock()
				if cb != nil {
					cb(assess.Reason, out)
				}
				return out, nil
			}
		} else {
			t.consecutive = 0
		}
		t.mu.Unlock()

		out, err := fn(ctx, args)

		t.mu.Lock()
		defer t.mu.Unlock()
		t.history = append(t.history, tc)
		if t.MaxHistory > 0 && len(t.history) > t.MaxHistory {
			t.history = t.history[len(t.history)-t.MaxHistory:]
		}
		return out, err
	}
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
