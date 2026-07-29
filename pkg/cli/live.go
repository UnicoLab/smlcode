package cli

import (
	"fmt"
	"strings"

	"github.com/piotrlaczkowski/slmcode/pkg/stream"
)

// FormatEvent renders a live stream event for the terminal (Antigravity/Zed style).
func FormatEvent(e stream.Event) string {
	kind := e.Kind
	if kind == "" {
		kind = stream.KindPhase
	}
	phase := e.Phase
	if e.Agent != "" {
		phase = e.Phase + " @" + e.Agent
	}
	head := Dim("["+phase+"]") + " " + e.Message
	if e.TaskID != "" {
		head += " " + Accent(e.TaskID)
	}
	if e.Scope != "" {
		head += "\n    " + Dim("scope ") + Cyan(e.Scope)
	}
	switch kind {
	case stream.KindAgentStart:
		head = Blue("▸ ") + head
	case stream.KindAgentEnd:
		head = Green("◂ ") + head
	case stream.KindCoord:
		head = Magenta("◆ ") + head
	case stream.KindLearn:
		head = Yellow("★ ") + head
	case stream.KindOutput:
		head = Cyan("∴ ") + head
	default:
		head = Dim("· ") + head
	}
	if strings.TrimSpace(e.Output) != "" && (kind == stream.KindAgentEnd || kind == stream.KindOutput) {
		out := e.Output
		if len(out) > 400 {
			out = out[:400] + "…"
		}
		for _, line := range strings.Split(out, "\n") {
			head += "\n    " + Dim("│ ") + White(line)
		}
	}
	return head
}

// PrintEvent writes a formatted live event to stdout.
func PrintEvent(e stream.Event) {
	fmt.Println(FormatEvent(e))
}
