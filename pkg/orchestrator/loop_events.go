package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

// LoopEvent is structured live feedback for Studio/TUI when the pipeline
// rewinds (tester fail → rewrite → corrective wave → continue ask).
type LoopEvent struct {
	Action   string   `json:"action"` // tester_reject | rewrite | corrective_wave | replan | continue_pending | continue_wave | placeholder_gaps
	Reason   string   `json:"reason"`
	Wave     int      `json:"wave,omitempty"`
	Failures []string `json:"failures,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Awaiting bool     `json:"awaiting,omitempty"` // true when HITL continue/abort is pending
}

func (o *Orchestrator) emitLoop(phase string, ev LoopEvent) {
	if o == nil {
		return
	}
	if ev.Action == "" {
		ev.Action = "loop"
	}
	if ev.Wave <= 0 {
		o.waveCounter++
		ev.Wave = o.waveCounter
	}
	data, err := json.Marshal(ev)
	if err != nil {
		data = []byte(`{"action":"loop"}`)
	}
	msg := ev.Reason
	if msg == "" {
		msg = "pipeline loop: " + ev.Action
	}
	if ev.From != "" && ev.To != "" {
		msg = fmt.Sprintf("%s → %s · %s", ev.From, ev.To, msg)
	}
	scope := ev.Action
	if ev.Awaiting {
		scope = ev.Action + ":awaiting"
	}
	o.emitFull(phase, stream.KindLoop, "loop", "", msg, scope, string(data))
}

func trimFailures(list []string, n int) []string {
	if n <= 0 || len(list) <= n {
		return list
	}
	out := append([]string{}, list[:n]...)
	out = append(out, fmt.Sprintf("…+%d more", len(list)-n))
	return out
}

func firstFailureLine(list []string, summary string) string {
	if len(list) > 0 && strings.TrimSpace(list[0]) != "" {
		return firstSentence(list[0])
	}
	if s := strings.TrimSpace(summary); s != "" {
		return firstSentence(s)
	}
	return "verification failed"
}
