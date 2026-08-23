package stream

import "time"

// Kind constants for live CLI/GUI streaming.
const (
	KindPhase        = "phase"
	KindAgentStart   = "agent_start"
	KindAgentEnd     = "agent_end"
	KindCoord        = "coord"
	KindLearn        = "learn"
	KindOutput       = "output"
	KindTool         = "tool"
	KindFileChange   = "file_change"  // partial apply / ws_edit / ws_write / ws_patch
	KindLatency      = "latency"      // phase/role wall-time telemetry for SLM tuning
	KindUsage        = "usage"        // token/cost accounting (estimated on early_exit when needed)
	KindDebug        = "debug"        // runner internals; filtered from default TUI/Studio views
	KindAsk          = "ask"          // clarify interview pending (questions in output JSON)
	KindIntervention = "intervention" // harness steered the model (quality/loop/whitelist/thinking)
	KindTurn         = "turn"         // turn-budget / progress meter update
	KindLoop         = "loop"         // tester reject / rewrite / corrective wave / continue-ask
	KindComposition  = "composition"  // dynamic pipeline/team/skill contract
	KindToken        = "token"        // incremental model output delta (token-by-token streaming)
)

// Level constants classify the severity of a live event so UIs can surface
// problems/warnings distinctly from routine progress.
const (
	LevelInfo    = "info"
	LevelWarn    = "warning"
	LevelError   = "error"
	LevelSuccess = "success"
	LevelProblem = "problem"
)

// Event is a live progress unit streamed to CLI + Studio SSE.
type Event struct {
	Phase   string    `json:"phase"`
	Kind    string    `json:"kind,omitempty"`
	Level   string    `json:"level,omitempty"`
	Message string    `json:"message"`
	TaskID  string    `json:"task_id,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Scope   string    `json:"scope,omitempty"`
	Output  string    `json:"output,omitempty"`
	Data    any       `json:"data,omitempty"`
	Time    time.Time `json:"time"`
}

// Token is the payload attached to Event.Data for KindToken so consumers can
// render incremental model output without parsing the message string.
type Token struct {
	Delta  string `json:"delta"`
	Tokens int    `json:"tokens,omitempty"` // running token count for this agent call
}

// Truncate bounds output payloads for SSE/CLI. The cut is made on a rune
// boundary and counted in runes, so multi-byte characters are never split into
// replacement characters.
func Truncate(s string, n int) string {
	s = trimSpace(s)
	if n <= 0 {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i] + "…"
		}
		count++
	}
	return s
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
