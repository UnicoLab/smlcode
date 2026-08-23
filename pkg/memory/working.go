package memory

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/compact"
)

// Working-memory caps. Every one of these is a hard ceiling: working memory can
// never grow with the length of a run.
const (
	MaxToolEvents    = 24
	MaxWorkingFiles  = compact.MaxDigestFiles
	MaxWorkingCmds   = compact.MaxDigestCommands
	MaxOpenFailures  = 8
	MaxDoneFailures  = 8
	MaxDecisions     = compact.MaxDigestDecisions
	MaxFocusFiles    = 16
	MaxSignatures    = 128
	DefaultWorkingTk = 700
)

// Tool-name classification, mirroring pkg/compact so a working-memory digest
// and a compaction digest describe the workspace the same way.
var (
	readToolNames  = map[string]bool{"ws_read": true, "ws_list": true, "ws_glob": true, "ws_grep": true, "read": true}
	editToolNames  = map[string]bool{"ws_edit": true, "ws_write": true, "ws_patch": true, "ws_delete": true, "ws_mv": true, "edit": true, "write": true}
	shellToolNames = map[string]bool{"ws_shell": true, "bash": true, "shell": true, "run_command": true}
)

// ToolEvent is one tool invocation and how it ended. Recording one is O(1) and
// allocation-light: this runs on the hot path after every tool call.
type ToolEvent struct {
	Tool     string        `json:"tool"`
	Path     string        `json:"path,omitempty"`
	Args     string        `json:"args,omitempty"`
	Command  string        `json:"command,omitempty"`
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	Key      string        `json:"key,omitempty"` // failure fingerprint, when known
	At       time.Time     `json:"at"`
	Duration time.Duration `json:"duration,omitempty"`
}

// Failure is an observed problem the run has not yet recovered from (or, once
// Resolve is called, a record of how it was recovered).
type Failure struct {
	Key        string    `json:"key"`
	Tool       string    `json:"tool,omitempty"`
	Path       string    `json:"path,omitempty"`
	Message    string    `json:"message"`
	Attempts   int       `json:"attempts"`
	FirstAt    time.Time `json:"first_at"`
	LastAt     time.Time `json:"last_at"`
	Resolved   bool      `json:"resolved,omitempty"`
	Resolution string    `json:"resolution,omitempty"`
	ResolvedBy string    `json:"resolved_by,omitempty"` // rule id, "llm", "retry"…
}

// WorkingSnapshot is an immutable copy of working memory, safe to hand to a
// reflection pass or serialize for inspection.
type WorkingSnapshot struct {
	RunID       string                  `json:"run_id,omitempty"`
	Task        string                  `json:"task,omitempty"`
	Role        string                  `json:"role,omitempty"`
	Focus       []string                `json:"focus,omitempty"`
	Summary     string                  `json:"summary,omitempty"`
	Events      []ToolEvent             `json:"events,omitempty"`
	FilesRead   []string                `json:"files_read,omitempty"`
	FilesEdited []string                `json:"files_edited,omitempty"`
	Commands    []compact.CommandRecord `json:"commands,omitempty"`
	Open        []Failure               `json:"open_failures,omitempty"`
	Resolved    []Failure               `json:"resolved_failures,omitempty"`
	Decisions   []string                `json:"decisions,omitempty"`
	ToolCalls   int                     `json:"tool_calls"`
	ToolErrors  int                     `json:"tool_errors"`
	Redundant   int                     `json:"redundant_calls"`
	StartedAt   time.Time               `json:"started_at,omitempty"`
}

// Working is short-term, run-scoped memory. It is safe for concurrent use:
// parallel workers share one Working through the Store.
type Working struct {
	mu sync.Mutex

	runID     string
	task      string
	role      string
	summary   string
	startedAt time.Time

	focus       []string
	events      []ToolEvent
	filesRead   []string
	filesEdited []string
	commands    []compact.CommandRecord
	open        []Failure
	resolved    []Failure
	decisions   []string

	signatures map[string]int
	toolCalls  int
	toolErrors int
	redundant  int

	budget int
	count  TokenCounter
	now    func() time.Time
}

func newWorking(budget int, count TokenCounter, now func() time.Time) *Working {
	if budget <= 0 {
		budget = DefaultWorkingTk
	}
	if now == nil {
		now = time.Now
	}
	return &Working{
		budget:     budget,
		count:      count,
		now:        now,
		signatures: make(map[string]int, 32),
		startedAt:  now(),
	}
}

// NewWorking builds a standalone working memory (no persistence). Useful in
// tests and for callers that only want the short-term layer.
func NewWorking(budgetTokens int) *Working { return newWorking(budgetTokens, nil, nil) }

// Start begins a new task within the run, clearing per-task state (focus,
// decisions, tool events) but keeping run-level counters so metrics stay
// meaningful across a multi-task run.
func (w *Working) Start(runID, task, role string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if runID != "" {
		w.runID = runID
	}
	w.task = strings.TrimSpace(task)
	w.role = strings.TrimSpace(role)
	w.focus = nil
	w.decisions = nil
	w.events = nil
	if w.startedAt.IsZero() {
		w.startedAt = w.now()
	}
}

// SetTask updates the description of what the run is currently doing.
func (w *Working) SetTask(task string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.task = strings.TrimSpace(task)
	w.mu.Unlock()
}

// SetRole records which specialist is active.
func (w *Working) SetRole(role string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.role = strings.TrimSpace(role)
	w.mu.Unlock()
}

// Focus adds paths to the focus set (most recent first, bounded).
func (w *Working) Focus(paths ...string) {
	if w == nil || len(paths) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.focus = dedupe(append(append([]string{}, paths...), w.focus...), MaxFocusFiles)
}

// Summarize replaces the compact rolling summary of the run so far.
func (w *Working) Summarize(text string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.summary = clip(text, 600)
	w.mu.Unlock()
}

// Decide records a decision the agent took without calling a tool.
func (w *Working) Decide(text string) {
	if w == nil {
		return
	}
	line := firstLine(text, 160)
	if line == "" {
		return
	}
	w.mu.Lock()
	w.decisions = tailStrings(append(w.decisions, line), MaxDecisions)
	w.mu.Unlock()
}

// RecordTool folds one tool call into working memory. It is the hot-path entry
// point: no I/O, no token counting, no regex — a handful of slice appends.
func (w *Working) RecordTool(e ToolEvent) {
	if w == nil || strings.TrimSpace(e.Tool) == "" {
		return
	}
	if e.At.IsZero() {
		e.At = w.now()
	}
	e.Tool = strings.ToLower(strings.TrimSpace(e.Tool))
	e.Args = clip(e.Args, 200)
	e.Error = firstLine(e.Error, 160)

	w.mu.Lock()
	defer w.mu.Unlock()

	w.toolCalls++
	w.events = appendBounded(w.events, e, MaxToolEvents)

	sig := e.Tool + "\x00" + e.Path + "\x00" + e.Args + "\x00" + e.Command
	if len(w.signatures) < MaxSignatures {
		w.signatures[sig]++
		if w.signatures[sig] > 1 {
			w.redundant++
		}
	} else if w.signatures[sig] > 0 {
		w.signatures[sig]++
		w.redundant++
	}

	switch {
	case readToolNames[e.Tool] && e.Path != "":
		w.filesRead = pushUnique(w.filesRead, e.Path, MaxWorkingFiles)
	case editToolNames[e.Tool] && e.Path != "" && e.OK:
		w.filesEdited = pushUnique(w.filesEdited, e.Path, MaxWorkingFiles)
	case shellToolNames[e.Tool]:
		cmd := e.Command
		if cmd == "" {
			cmd = e.Args
		}
		if cmd = firstLine(cmd, 120); cmd != "" {
			status := "ok"
			if !e.OK {
				status = "failed"
			}
			w.commands = appendBounded(w.commands, compact.CommandRecord{Command: cmd, Status: status}, MaxWorkingCmds)
		}
	}

	if e.OK {
		return
	}
	w.toolErrors++
	w.failLocked(Failure{
		Key:     e.Key,
		Tool:    e.Tool,
		Path:    e.Path,
		Message: e.Error,
		LastAt:  e.At,
	})
}

// Fail opens (or re-counts) a failure independently of a tool call — used for
// gate failures, review rejections and loop stalls.
func (w *Working) Fail(f Failure) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failLocked(f)
}

func (w *Working) failLocked(f Failure) {
	f.Message = firstLine(f.Message, 200)
	if f.Key == "" {
		f.Key = failureKey(f.Tool, f.Message)
	}
	if f.LastAt.IsZero() {
		f.LastAt = w.now()
	}
	for i := range w.open {
		if w.open[i].Key == f.Key {
			w.open[i].Attempts++
			w.open[i].LastAt = f.LastAt
			if f.Message != "" {
				w.open[i].Message = f.Message
			}
			return
		}
	}
	f.Attempts = 1
	f.FirstAt = f.LastAt
	w.open = appendBounded(w.open, f, MaxOpenFailures)
}

// Resolve marks an open failure as recovered. resolvedBy names what fixed it
// (a repair-rule id, "llm", "retry"…) so the reflection pass can tell memory
// hits from fresh LLM round-trips. Unknown keys are ignored.
func (w *Working) Resolve(key, resolution, resolvedBy string) bool {
	if w == nil || key == "" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.open {
		if w.open[i].Key != key {
			continue
		}
		f := w.open[i]
		f.Resolved = true
		f.Resolution = clip(resolution, 200)
		f.ResolvedBy = resolvedBy
		f.LastAt = w.now()
		w.open = append(w.open[:i], w.open[i+1:]...)
		w.resolved = appendBounded(w.resolved, f, MaxDoneFailures)
		return true
	}
	return false
}

// OpenFailures returns a copy of the failures still outstanding.
func (w *Working) OpenFailures() []Failure {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Failure(nil), w.open...)
}

// Snapshot copies working memory for reflection or inspection.
func (w *Working) Snapshot() WorkingSnapshot {
	if w == nil {
		return WorkingSnapshot{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return WorkingSnapshot{
		RunID:       w.runID,
		Task:        w.task,
		Role:        w.role,
		Focus:       append([]string(nil), w.focus...),
		Summary:     w.summary,
		Events:      append([]ToolEvent(nil), w.events...),
		FilesRead:   append([]string(nil), w.filesRead...),
		FilesEdited: append([]string(nil), w.filesEdited...),
		Commands:    append([]compact.CommandRecord(nil), w.commands...),
		Open:        append([]Failure(nil), w.open...),
		Resolved:    append([]Failure(nil), w.resolved...),
		Decisions:   append([]string(nil), w.decisions...),
		ToolCalls:   w.toolCalls,
		ToolErrors:  w.toolErrors,
		Redundant:   w.redundant,
		StartedAt:   w.startedAt,
	}
}

// Counters returns the run-level tool statistics (calls, errors, redundant
// repeats) that feed the metrics record.
func (w *Working) Counters() (calls, errs, redundant int) {
	if w == nil {
		return 0, 0, 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.toolCalls, w.toolErrors, w.redundant
}

// Digest projects working memory onto pkg/compact's MustPreserve schema — the
// same shape a conversation compaction is required to carry across the cut, so
// a compacted run and a fresh run see the same headings in the same order.
func (w *Working) Digest() compact.MustPreserve {
	snap := w.Snapshot()
	d := compact.MustPreserve{
		FilesRead:   snap.FilesRead,
		FilesEdited: snap.FilesEdited,
		Commands:    snap.Commands,
		Decisions:   snap.Decisions,
	}
	for _, f := range snap.Open {
		d.Failures = append(d.Failures, compact.FailedCall{
			Tool: f.Tool, Path: f.Path, Error: f.Message,
		})
		if len(d.Failures) >= compact.MaxDigestFailures {
			break
		}
	}
	if len(snap.Resolved) > 0 {
		lines := make([]string, 0, len(snap.Resolved))
		for _, f := range snap.Resolved {
			lines = append(lines, fmt.Sprintf("%s → fixed by %s", f.Message, orElse(f.Resolution, f.ResolvedBy)))
		}
		d.Extra = map[string][]string{"Already fixed this run (do not redo)": lines}
	}
	return d
}

// Render emits the injectable working-memory block, capped at budgetTokens
// (0 uses the configured default). Returns "" when there is nothing worth
// saying — an empty block is strictly better than a block of headings.
func (w *Working) Render(budgetTokens int) string {
	if w == nil {
		return ""
	}
	if budgetTokens <= 0 {
		budgetTokens = w.budget
	}
	snap := w.Snapshot()
	digest := w.Digest()

	var b strings.Builder
	b.WriteString("## Working memory (this run)\n\n")
	head := b.Len()
	if snap.Task != "" {
		fmt.Fprintf(&b, "Current task: %s\n", firstLine(snap.Task, 200))
	}
	if snap.Role != "" {
		fmt.Fprintf(&b, "Role: %s\n", snap.Role)
	}
	if len(snap.Focus) > 0 {
		fmt.Fprintf(&b, "Focus files: %s\n", strings.Join(snap.Focus, ", "))
	}
	if snap.Summary != "" {
		fmt.Fprintf(&b, "\nSo far: %s\n", snap.Summary)
	}
	if b.Len() > head {
		b.WriteString("\n")
	}
	if b.Len() == head && digest.Empty() {
		return ""
	}
	if !digest.Empty() {
		body := digest.Render(budgetTokens * bytesPerToken)
		// Drop the compaction-specific heading; this is live state, not a cut.
		body = strings.TrimPrefix(body, "## Compacted session state\n\n")
		b.WriteString(body)
	}
	return fitToTokens(strings.TrimRight(b.String(), "\n")+"\n", budgetTokens, w.count)
}

// Reset clears working memory entirely (new run).
func (w *Working) Reset() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.runID, w.task, w.role, w.summary = "", "", "", ""
	w.focus, w.events, w.filesRead, w.filesEdited = nil, nil, nil, nil
	w.commands, w.open, w.resolved, w.decisions = nil, nil, nil, nil
	w.signatures = make(map[string]int, 32)
	w.toolCalls, w.toolErrors, w.redundant = 0, 0, 0
	w.startedAt = w.now()
}

func failureKey(tool, msg string) string {
	return hashID("wf_", strings.ToLower(tool), strings.ToLower(clip(msg, 120)))
}

func appendBounded[T any](in []T, v T, max int) []T {
	in = append(in, v)
	if max > 0 && len(in) > max {
		in = in[len(in)-max:]
	}
	return in
}

func pushUnique(in []string, v string, max int) []string {
	for _, s := range in {
		if s == v {
			return in
		}
	}
	return appendBounded(in, v, max)
}

func orElse(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	if strings.TrimSpace(b) != "" {
		return b
	}
	return "unknown"
}
