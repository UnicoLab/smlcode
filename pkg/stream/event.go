package stream

import "time"

// Kind constants for live CLI/GUI streaming.
const (
	KindPhase      = "phase"
	KindAgentStart = "agent_start"
	KindAgentEnd   = "agent_end"
	KindCoord      = "coord"
	KindLearn      = "learn"
	KindOutput     = "output"
	KindTool       = "tool"
	KindFileChange = "file_change" // partial apply / ws_edit / ws_write / ws_patch
	KindLatency    = "latency"     // phase/role wall-time telemetry for SLM tuning
	KindUsage      = "usage"       // token/cost accounting (estimated on early_exit when needed)
	KindDebug      = "debug"       // runner internals; filtered from default TUI/Studio views
	KindAsk        = "ask"         // clarify interview pending (questions in output JSON)
)

// Event is a live progress unit streamed to CLI + Studio SSE.
type Event struct {
	Phase   string    `json:"phase"`
	Kind    string    `json:"kind,omitempty"`
	Message string    `json:"message"`
	TaskID  string    `json:"task_id,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Scope   string    `json:"scope,omitempty"`
	Output  string    `json:"output,omitempty"`
	Time    time.Time `json:"time"`
}

// Truncate bounds output payloads for SSE/CLI.
func Truncate(s string, n int) string {
	s = trimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
