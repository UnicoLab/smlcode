package cli

import (
	"strings"
	"unicode"
)

// Human-in-the-loop gates rendered inline in the terminal.
//
// The orchestrator exposes OnPlanApprove / OnContinue / OnEscalate / OnAsk
// hooks; the CLI registers handlers that build a Gate, hand it to the live
// session, and block on the answer. With a TTY attached a gate blocks
// indefinitely (a human is there); without one it resolves via the configured
// non-interactive policy instead of silently auto-approving.

// GateOption is one answerable choice.
type GateOption struct {
	Key   rune   // single-keystroke accelerator
	Label string // "yes", "replan", …
	Value string // value handed back to the orchestrator
	Hint  string // extra description shown after the label
	// Freeform marks an option that needs follow-up text (e.g. "edit").
	Freeform bool
}

// Gate is a pending human decision.
type Gate struct {
	ID      string
	Kind    string // plan | continue | escalate | clarify
	Title   string // "Approve plan?"
	Body    []string
	Options []GateOption
	// Timeout policy applied only when no TTY is attached.
	NonTTYDefault string
}

// GateAnswer is what the user chose.
type GateAnswer struct {
	Value string
	Notes string
}

// GateTimeoutPolicy is the non-interactive behavior for a gate.
type GateTimeoutPolicy string

const (
	GateTimeoutStop    GateTimeoutPolicy = "stop"
	GateTimeoutApprove GateTimeoutPolicy = "approve"
	GateTimeoutReject  GateTimeoutPolicy = "reject"
)

// ParseGateTimeoutPolicy validates --on-gate-timeout.
func ParseGateTimeoutPolicy(s string) (GateTimeoutPolicy, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "stop":
		return GateTimeoutStop, true
	case "approve", "yes", "auto":
		return GateTimeoutApprove, true
	case "reject", "no":
		return GateTimeoutReject, true
	}
	return GateTimeoutStop, false
}

// PromptLine renders the one-line answer prompt, e.g.
// "Approve plan? [y]es / [e]dit / [n]o / [r]eplan ›".
func (g Gate) PromptLine() string {
	var parts []string
	for _, o := range g.Options {
		label := o.Label
		k := string(o.Key)
		if i := strings.IndexFunc(label, func(r rune) bool {
			return unicode.ToLower(r) == unicode.ToLower(o.Key)
		}); i >= 0 {
			label = label[:i] + "[" + label[i:i+len(k)] + "]" + label[i+len(k):]
		} else {
			label = "[" + k + "]" + label
		}
		parts = append(parts, Yellow(label))
	}
	return Bold(g.Title) + " " + strings.Join(parts, Dim(" / ")) + " " + Accent("›")
}

// Render draws the full gate card (title, body, prompt) for the transcript.
func (g Gate) Render(width int) string {
	var b strings.Builder
	b.WriteString(Yellow("┌─ ") + Bold(Yellow(g.Title)) + "\n")
	for _, line := range g.Body {
		for _, sub := range wrapPlain(line, width-4) {
			b.WriteString(Yellow("│ ") + sub + "\n")
		}
	}
	b.WriteString(Yellow("└─ ") + Dim("answer below · type free text with a choice to add notes") + "\n")
	return b.String()
}

// Resolve maps a typed answer (a single accelerator key, a full label, or free
// text) onto an option value plus notes. ok is false when nothing matched.
func (g Gate) Resolve(input string) (GateAnswer, bool) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return GateAnswer{}, false
	}
	head := raw
	rest := ""
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		head, rest = raw[:i], strings.TrimSpace(raw[i+1:])
	}
	lowerHead := strings.ToLower(head)
	for _, o := range g.Options {
		if lowerHead == strings.ToLower(o.Label) ||
			lowerHead == strings.ToLower(o.Value) ||
			(len([]rune(lowerHead)) == 1 && unicode.ToLower([]rune(lowerHead)[0]) == unicode.ToLower(o.Key)) {
			return GateAnswer{Value: o.Value, Notes: rest}, true
		}
	}
	// Free text with no recognized prefix becomes notes on the first freeform
	// option when one exists — "add tests for the parser" answers an edit gate.
	for _, o := range g.Options {
		if o.Freeform {
			return GateAnswer{Value: o.Value, Notes: raw}, true
		}
	}
	return GateAnswer{}, false
}

// ResolveKey maps a single keystroke onto an option.
func (g Gate) ResolveKey(k Key) (GateAnswer, bool) {
	if k.Type != KeyRune {
		return GateAnswer{}, false
	}
	for _, o := range g.Options {
		if unicode.ToLower(k.Rune) == unicode.ToLower(o.Key) {
			if o.Freeform {
				return GateAnswer{}, false // needs typed follow-up
			}
			return GateAnswer{Value: o.Value}, true
		}
	}
	return GateAnswer{}, false
}

// wrapPlain hard-wraps text to width display cells on word boundaries.
func wrapPlain(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	s = strings.TrimRight(s, " ")
	if s == "" {
		return []string{""}
	}
	if VisibleWidth(s) <= width {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	curW := 0
	for _, word := range strings.Fields(s) {
		w := StringWidth(word)
		if curW > 0 && curW+1+w > width {
			out = append(out, cur.String())
			cur.Reset()
			curW = 0
		}
		if curW > 0 {
			cur.WriteString(" ")
			curW++
		}
		for w > width { // a single over-long token
			cut := TruncateWidth(word, width)
			out = append(out, cut)
			word = word[len(cut):]
			w = StringWidth(word)
		}
		cur.WriteString(word)
		curW += w
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// PlanGate builds the plan-approval gate.
func PlanGate(id, query, summary string, goals, tasks []string, taskCount int) Gate {
	body := []string{}
	if query != "" {
		body = append(body, Dim("query  ")+Clip(query, 200))
	}
	if summary != "" {
		body = append(body, Dim("plan   ")+Clip(summary, 400))
	}
	for _, g := range goals {
		body = append(body, Dim("goal   ")+Clip(g, 200))
	}
	body = append(body, Dim("tasks  ")+Bold(itoa(taskCount)))
	for i, t := range tasks {
		if i >= 12 {
			body = append(body, Dim("       … +"+itoa(len(tasks)-12)+" more"))
			break
		}
		body = append(body, "       "+Cyan(Clip(t, 160)))
	}
	return Gate{
		ID:    id,
		Kind:  "plan",
		Title: "Approve plan?",
		Body:  body,
		Options: []GateOption{
			{Key: 'y', Label: "yes", Value: "approve"},
			{Key: 'e', Label: "edit", Value: "approve", Hint: "approve with notes", Freeform: true},
			{Key: 'n', Label: "no", Value: "reject"},
			{Key: 'r', Label: "replan", Value: "replan"},
		},
		NonTTYDefault: "reject",
	}
}

// ContinueGate builds the continue/stop gate.
func ContinueGate(id, reason, summary string, gaps, escalated []string) Gate {
	body := []string{}
	if reason != "" {
		body = append(body, Dim("reason ")+Clip(reason, 240))
	}
	if summary != "" {
		body = append(body, Dim("state  ")+Clip(summary, 240))
	}
	for i, g := range gaps {
		if i >= 8 {
			body = append(body, Dim("       … +"+itoa(len(gaps)-8)+" more gaps"))
			break
		}
		body = append(body, Dim("gap    ")+Yellow(Clip(g, 160)))
	}
	if len(escalated) > 0 {
		body = append(body, Dim("stuck  ")+Red(strings.Join(escalated, ", ")))
	}
	return Gate{
		ID:    id,
		Kind:  "continue",
		Title: "Retries exhausted — continue?",
		Body:  body,
		Options: []GateOption{
			{Key: 'c', Label: "continue", Value: "continue"},
			{Key: 's', Label: "stop", Value: "stop"},
			{Key: 'f', Label: "flag only", Value: "flag_only"},
		},
		NonTTYDefault: "stop",
	}
}

// EscalateGate builds the per-task escalate gate.
func EscalateGate(id, taskID, title, detail string, files []string) Gate {
	body := []string{Dim("task   ") + Accent(taskID) + "  " + Clip(title, 140)}
	if len(files) > 0 {
		body = append(body, Dim("files  ")+Cyan(strings.Join(files, ", ")))
	}
	if detail != "" {
		body = append(body, Dim("why    ")+Clip(detail, 400))
	}
	return Gate{
		ID:    id,
		Kind:  "escalate",
		Title: "Task escalated — what now?",
		Body:  body,
		Options: []GateOption{
			{Key: 'r', Label: "retry", Value: "retry"},
			{Key: 's', Label: "scope", Value: "re_scope", Hint: "leave for human edit"},
			{Key: 'd', Label: "done", Value: "mark_done", Hint: "force done"},
			{Key: 'a', Label: "abort", Value: "abort"},
		},
		NonTTYDefault: "re_scope",
	}
}

// ClarifyGate builds a gate for one clarify question.
func ClarifyGate(id, question string, options []string, recommended string) Gate {
	body := []string{}
	for _, o := range options {
		mark := "  "
		if o == recommended {
			mark = Green("★ ")
		}
		body = append(body, mark+Clip(o, 160))
	}
	opts := []GateOption{}
	keys := "1234567890"
	for i, o := range options {
		if i >= len(keys) {
			break
		}
		opts = append(opts, GateOption{Key: rune(keys[i]), Label: Clip(o, 24), Value: o})
	}
	opts = append(opts,
		GateOption{Key: 'a', Label: "auto", Value: "__recommended__", Hint: "use recommended"},
		GateOption{Key: 'o', Label: "other", Value: "__freeform__", Freeform: true},
	)
	return Gate{
		ID:            id,
		Kind:          "clarify",
		Title:         Clip(question, 160),
		Body:          body,
		Options:       opts,
		NonTTYDefault: "__recommended__",
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
