package session

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// EventSummary is a compact, user-facing diagnosis for a persisted run log.
// It is derived from events.jsonl and intentionally avoids runtime-only state.
type EventSummary struct {
	TotalEvents int              `json:"total_events"`
	StartedAt   string           `json:"started_at,omitempty"`
	LastAt      string           `json:"last_at,omitempty"`
	DurationMS  int64            `json:"duration_ms,omitempty"`
	FinalPhase  string           `json:"final_phase,omitempty"`
	FinalKind   string           `json:"final_kind,omitempty"`
	LastMessage string           `json:"last_message,omitempty"`
	Phases      []EventNameCount `json:"phases,omitempty"`
	Agents      []EventNameCount `json:"agents,omitempty"`
	Models      []EventNameCount `json:"models,omitempty"`
	Tasks       int              `json:"tasks"`
	Retries     int              `json:"retries"`
	Replans     int              `json:"replans"`
	Failures    int              `json:"failures"`
	Warnings    int              `json:"warnings"`
	Errors      int              `json:"errors"`
	ToolCalls   int              `json:"tool_calls"`
	ShellCalls  int              `json:"shell_calls"`
	Tokens      int              `json:"tokens,omitempty"`
	CostUSD     float64          `json:"cost_usd,omitempty"`
	Insights    []RunInsight     `json:"insights,omitempty"`
	Actions     []RunAction      `json:"actions,omitempty"`
}

type EventNameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type RunInsight struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Phase    string `json:"phase,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Time     string `json:"time,omitempty"`
}

type RunAction struct {
	Title   string `json:"title"`
	Detail  string `json:"detail,omitempty"`
	Command string `json:"command,omitempty"`
}

// AnalyzeEvents summarizes run behavior in terms users need when debugging
// local SLM runs: retries, replans, model/agent usage, and failure clues.
func AnalyzeEvents(events []EventRecord) EventSummary {
	s := EventSummary{TotalEvents: len(events)}
	if len(events) == 0 {
		s.Insights = []RunInsight{{
			Severity: "warning",
			Title:    "No events recorded",
			Detail:   "Enable session_event_log or run again to capture an inspectable timeline.",
		}}
		return s
	}

	phases := map[string]int{}
	agents := map[string]int{}
	models := map[string]int{}
	tasks := map[string]struct{}{}
	var firstTime, lastTime time.Time
	var terminalOK bool
	var providerIssue, modelIssue, qaIssue, contextIssue, permissionIssue bool

	for i, ev := range events {
		if ev.Phase != "" {
			phases[ev.Phase]++
			s.FinalPhase = ev.Phase
		}
		if ev.Kind != "" {
			s.FinalKind = ev.Kind
		}
		if ev.Agent != "" {
			agents[ev.Agent]++
		}
		if ev.Model != "" {
			models[ev.Model]++
		}
		if ev.TaskID != "" {
			tasks[ev.TaskID] = struct{}{}
		}
		if ev.Message != "" {
			s.LastMessage = ev.Message
		}
		if ev.Tokens > 0 {
			s.Tokens += ev.Tokens
		}
		if ev.CostUSD > 0 {
			s.CostUSD += ev.CostUSD
		}
		if ts, ok := parseEventTime(ev.Time); ok {
			if firstTime.IsZero() {
				firstTime = ts
				s.StartedAt = ev.Time
			}
			lastTime = ts
			s.LastAt = ev.Time
		}
		if looksLikeRetry(ev) {
			s.Retries++
		}
		if looksLikeReplan(ev) {
			s.Replans++
		}
		if looksLikeWarning(ev) {
			s.Warnings++
		}
		if looksLikeError(ev) {
			s.Errors++
		}
		if looksLikeFailure(ev) {
			providerIssue = providerIssue || looksLikeProviderIssue(ev)
			modelIssue = modelIssue || looksLikeModelIssue(ev)
			qaIssue = qaIssue || looksLikeQAIssue(ev)
			contextIssue = contextIssue || looksLikeContextIssue(ev)
			permissionIssue = permissionIssue || looksLikePermissionIssue(ev)
			s.Failures++
			s.addInsight(RunInsight{
				Severity: "error",
				Title:    "Failure event",
				Detail:   trimInsight(ev.Message),
				Phase:    ev.Phase,
				TaskID:   ev.TaskID,
				Agent:    ev.Agent,
				Time:     ev.Time,
			})
		}
		if ev.Kind == "tool" || strings.Contains(strings.ToLower(ev.Kind), "tool") {
			s.ToolCalls++
		}
		if ev.Kind == "shell" || strings.Contains(strings.ToLower(ev.Kind+" "+ev.Message), "shell") {
			s.ShellCalls++
		}
		if isTerminalSuccess(ev) {
			terminalOK = true
		}
		if !looksLikeFailure(ev) {
			providerIssue = providerIssue || looksLikeProviderIssue(ev)
			modelIssue = modelIssue || looksLikeModelIssue(ev)
			qaIssue = qaIssue || looksLikeQAIssue(ev)
			contextIssue = contextIssue || looksLikeContextIssue(ev)
			permissionIssue = permissionIssue || looksLikePermissionIssue(ev)
		}
		if i == len(events)-1 && !terminalOK && looksLikeTerminalProblem(ev) {
			s.addInsight(RunInsight{
				Severity: "error",
				Title:    "Run ended on a problem",
				Detail:   trimInsight(ev.Message),
				Phase:    ev.Phase,
				TaskID:   ev.TaskID,
				Agent:    ev.Agent,
				Time:     ev.Time,
			})
		}
	}

	if !firstTime.IsZero() && !lastTime.IsZero() && !lastTime.Before(firstTime) {
		s.DurationMS = lastTime.Sub(firstTime).Milliseconds()
	}
	s.Tasks = len(tasks)
	s.Phases = topCounts(phases, 16)
	s.Agents = topCounts(agents, 12)
	s.Models = topCounts(models, 8)

	if s.Replans > 0 {
		s.addInsight(RunInsight{
			Severity: "info",
			Title:    "Plan was revised",
			Detail:   plural(s.Replans, "replan signal") + " detected in the run timeline.",
		})
	}
	if s.Retries >= 3 {
		s.addInsight(RunInsight{
			Severity: "warning",
			Title:    "High retry pressure",
			Detail:   plural(s.Retries, "retry signal") + " detected; this often means the task scope or local model context was too broad.",
		})
	}
	if s.Errors > 0 && s.Failures == 0 {
		s.addInsight(RunInsight{
			Severity: "error",
			Title:    "Errors without task failure",
			Detail:   plural(s.Errors, "error signal") + " detected; inspect the event log for provider, tool, or QA failures.",
		})
	}
	if !terminalOK && !looksStopped(events[len(events)-1]) {
		s.addInsight(RunInsight{
			Severity: "warning",
			Title:    "No successful terminal event",
			Detail:   "The event log does not include a clear run_done or done success marker.",
			Phase:    events[len(events)-1].Phase,
			Time:     events[len(events)-1].Time,
		})
	}
	if len(s.Models) > 1 {
		s.addInsight(RunInsight{
			Severity: "info",
			Title:    "Multiple models used",
			Detail:   "The run switched or routed across more than one model.",
		})
	}
	if providerIssue {
		s.addAction(RunAction{
			Title:   "Check the model endpoint",
			Detail:  "The timeline looks like a provider or local runtime connectivity failure.",
			Command: "slmcode doctor",
		})
	}
	if modelIssue {
		s.addAction(RunAction{
			Title:   "Verify the configured model",
			Detail:  "The selected model may not be served by the current endpoint.",
			Command: "slmcode stack list",
		})
	}
	if contextIssue || s.Retries >= 3 {
		s.addAction(RunAction{
			Title:  "Shrink the next attempt",
			Detail: "Use the replan button or split the request into fewer files/tasks so a small local model has enough useful context.",
		})
	}
	if qaIssue {
		s.addAction(RunAction{
			Title:   "Run the project QA gate",
			Detail:  "A test/build/lint gate appears to be the blocker; rerun it locally before another model wave.",
			Command: "slmcode status",
		})
	}
	if permissionIssue {
		s.addAction(RunAction{
			Title:   "Review command permissions",
			Detail:  "A shell or filesystem guardrail may have stopped execution.",
			Command: "slmcode config show",
		})
	}
	if !terminalOK && len(s.Actions) == 0 {
		s.addAction(RunAction{
			Title:  "Inspect the final phase",
			Detail: "The run did not record a clean terminal event; open the last error/output row before resuming.",
		})
	}
	if len(s.Insights) > 8 {
		s.Insights = s.Insights[:8]
	}
	if len(s.Actions) > 5 {
		s.Actions = s.Actions[:5]
	}
	return s
}

func (s *EventSummary) addInsight(in RunInsight) {
	if in.Title == "" {
		return
	}
	for _, existing := range s.Insights {
		if existing.Title == in.Title && existing.Phase == in.Phase && existing.TaskID == in.TaskID && existing.Detail == in.Detail {
			return
		}
	}
	s.Insights = append(s.Insights, in)
}

func (s *EventSummary) addAction(action RunAction) {
	if action.Title == "" {
		return
	}
	for _, existing := range s.Actions {
		if existing.Title == action.Title && existing.Command == action.Command {
			return
		}
	}
	s.Actions = append(s.Actions, action)
}

func parseEventTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

func topCounts(counts map[string]int, limit int) []EventNameCount {
	out := make([]EventNameCount, 0, len(counts))
	for name, count := range counts {
		if name == "" || count <= 0 {
			continue
		}
		out = append(out, EventNameCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Name < out[j].Name
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func looksLikeRetry(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "retry") || strings.Contains(text, "retries") || strings.Contains(text, "corrective")
}

func looksLikeReplan(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "replan") || strings.Contains(text, "plan was revised") || strings.Contains(text, "request replan")
}

func looksLikeWarning(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "warn") || strings.Contains(text, "risk") || strings.Contains(text, "degraded")
}

func looksLikeError(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "error") || strings.Contains(text, "panic") || strings.Contains(text, "exception")
}

func looksLikeFailure(ev EventRecord) bool {
	text := eventText(ev)
	if strings.Contains(strings.ToLower(ev.Kind), "fail") || strings.EqualFold(ev.Phase, "error") {
		return true
	}
	return strings.Contains(text, "failed") || strings.Contains(text, "still red") || strings.Contains(text, "timed out") || strings.Contains(text, "timeout") || strings.Contains(text, "blocked")
}

func looksLikeProviderIssue(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "connection refused") ||
		strings.Contains(text, "connect:") ||
		strings.Contains(text, "no such host") ||
		strings.Contains(text, "econnrefused") ||
		strings.Contains(text, "server closed") ||
		strings.Contains(text, "provider") && (strings.Contains(text, "unreachable") || strings.Contains(text, "failed"))
}

func looksLikeModelIssue(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "model not found") ||
		strings.Contains(text, "unknown model") ||
		strings.Contains(text, "no model") ||
		strings.Contains(text, "404") && strings.Contains(text, "model")
}

func looksLikeQAIssue(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "qa_gate") ||
		strings.Contains(text, "test failed") ||
		strings.Contains(text, "lint failed") ||
		strings.Contains(text, "build failed") ||
		strings.Contains(text, "go test") ||
		strings.Contains(text, "npm test") ||
		strings.Contains(text, "pytest")
}

func looksLikeContextIssue(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "context length") ||
		strings.Contains(text, "context window") ||
		strings.Contains(text, "maximum context") ||
		strings.Contains(text, "token limit") ||
		strings.Contains(text, "too many tokens") ||
		strings.Contains(text, "truncated")
}

func looksLikePermissionIssue(ev EventRecord) bool {
	text := eventText(ev)
	return strings.Contains(text, "permission denied") ||
		strings.Contains(text, "shell denied") ||
		strings.Contains(text, "not allowed") ||
		strings.Contains(text, "blocked by")
}

func looksLikeTerminalProblem(ev EventRecord) bool {
	return looksLikeFailure(ev) || looksLikeError(ev) || strings.Contains(eventText(ev), "aborted")
}

func isTerminalSuccess(ev EventRecord) bool {
	text := eventText(ev)
	if ev.Kind == "run_done" || ev.Kind == "run_end" {
		return true
	}
	return (ev.Phase == "done" || ev.Phase == "finish") && (strings.Contains(text, "done") || strings.Contains(text, "success") || strings.Contains(text, "green"))
}

func looksStopped(ev EventRecord) bool {
	text := eventText(ev)
	return ev.Kind == "run_stop" || strings.Contains(text, "stopped by user")
}

func eventText(ev EventRecord) string {
	return strings.ToLower(strings.Join([]string{ev.Phase, ev.Kind, ev.Agent, ev.Message, ev.Output}, " "))
}

func trimInsight(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "See the event log output for details."
	}
	if len(s) <= 220 {
		return s
	}
	return s[:220] + "..."
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strings.TrimSpace(strings.Join([]string{intString(n), noun + "s"}, " "))
}

func intString(n int) string {
	return strconv.Itoa(n)
}
