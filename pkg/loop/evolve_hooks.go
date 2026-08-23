package loop

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// DefaultMemoryTokens is the injected memory-block budget when MemoryTokens=0.
const DefaultMemoryTokens = 300

// evolveState accumulates what the orchestrator needs to hand to
// evolve.Engine.Finish at the end of a run. Every method is nil-safe and safe
// for concurrent use — waves and parallel reviews both write to it.
type evolveState struct {
	mu        sync.Mutex
	failures  []evolve.FailureEvent
	decisions []evolve.DecisionRecord
}

func (s *evolveState) addFailure(ev evolve.FailureEvent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.failures = append(s.failures, ev)
	s.mu.Unlock()
}

func (s *evolveState) addDecision(rec evolve.DecisionRecord) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.decisions = append(s.decisions, rec)
	s.mu.Unlock()
}

// resolve marks the most recent unresolved event for a fingerprint as fixed.
func (s *evolveState) resolve(fp evolve.Fingerprint, ruleID, resolution string) bool {
	if s == nil || fp.Zero() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.failures) - 1; i >= 0; i-- {
		if s.failures[i].Resolved() {
			continue
		}
		if evolve.Analyze(s.failures[i].Signal).ID != fp.ID {
			continue
		}
		s.failures[i].RuleID = ruleID
		s.failures[i].Resolution = resolution
		if ruleID != "" {
			s.failures[i].ResolvedBy = "rule:" + ruleID
		} else {
			s.failures[i].ResolvedBy = "llm"
		}
		return true
	}
	return false
}

func (s *evolveState) snapshotFailures() []evolve.FailureEvent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]evolve.FailureEvent(nil), s.failures...)
}

func (s *evolveState) drainFailures() []evolve.FailureEvent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.failures
	s.failures = nil
	return out
}

func (s *evolveState) snapshotDecisions() []evolve.DecisionRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]evolve.DecisionRecord(nil), s.decisions...)
}

func (s *evolveState) drainDecisions() []evolve.DecisionRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.decisions
	s.decisions = nil
	return out
}

// ── exported accessors the orchestrator drains ──────────────────────────────

// FailureEvents returns a copy of every failure recorded so far, for
// evolve.RunReport.Failures.
func (r *Runner) FailureEvents() []evolve.FailureEvent {
	if r == nil {
		return nil
	}
	return r.evo.snapshotFailures()
}

// DrainFailureEvents returns the accumulated failures and clears them, so the
// orchestrator can fold them into a RunReport once per run.
func (r *Runner) DrainFailureEvents() []evolve.FailureEvent {
	if r == nil {
		return nil
	}
	return r.evo.drainFailures()
}

// DecisionRecords returns a copy of every bandit decision recorded so far, for
// evolve.RunReport.Decisions. Edit-format decisions carry their MEASURED
// outcome (see finalizeEditDecisions), not a zero one.
func (r *Runner) DecisionRecords() []evolve.DecisionRecord {
	if r == nil {
		return nil
	}
	return r.finalizeEditDecisions(r.evo.snapshotDecisions())
}

// DrainDecisionRecords returns the accumulated bandit decisions and clears them.
func (r *Runner) DrainDecisionRecords() []evolve.DecisionRecord {
	if r == nil {
		return nil
	}
	return r.finalizeEditDecisions(r.evo.drainDecisions())
}

// ── failure path ────────────────────────────────────────────────────────────

// noteFailure is THE failure entry point: it fingerprints, consults the rule
// store, records the event for reflection, and returns the advice so the caller
// can act on it. Every field of the returned Advice is zero when Evolve is nil.
func (r *Runner) noteFailure(sig evolve.Signal, rawArgs string) evolve.Advice {
	if r == nil {
		return evolve.Advice{}
	}
	sig.Message = strings.TrimSpace(sig.Message)
	if sig.Message == "" {
		return evolve.Advice{}
	}
	sig.Message = textutil.TruncateDefault(sig.Message, 2000)
	var adv evolve.Advice
	if r.Evolve != nil {
		adv = r.Evolve.OnFailure(sig, rawArgs)
	} else {
		adv.Fingerprint = evolve.Analyze(sig)
	}
	r.evo.addFailure(evolve.FailureEvent{Signal: sig, Attempts: 1})
	if adv.Found {
		r.logf("evolve: %s", adv.Reason)
	}
	return adv
}

// noteResolved credits whatever fixed a failure — a stored rule or an LLM.
func (r *Runner) noteResolved(adv evolve.Advice, what string) {
	if r == nil || adv.Fingerprint.Zero() {
		return
	}
	r.evo.resolve(adv.Fingerprint, adv.RuleID, what)
	if r.Evolve != nil {
		r.Evolve.Resolved(adv.Fingerprint, adv.RuleID, what)
	}
}

// taskFailureSignal builds a Signal from a task-level failure.
func (r *Runner) taskFailureSignal(t plan.Task, phase string, err error) evolve.Signal {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	sig := evolve.Signal{
		Message:  msg,
		Language: detectSignalLanguage(r.Root),
		Phase:    phase,
		Role:     t.Role,
	}
	if len(t.Files) > 0 {
		sig.Path = t.Files[0]
	}
	return sig
}

// recordTool folds one tool invocation into working memory. Hot path: no I/O.
func (r *Runner) recordTool(ev memory.ToolEvent) {
	if r == nil || r.Evolve == nil {
		return
	}
	mem := r.Evolve.Memory()
	if mem == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	mem.Working().RecordTool(ev)
}

// memorySection renders the learned-memory block for a worker/corrector prompt.
// It returns "" when there is nothing worth saying — the common and correct
// case — and is never wrapped in a heading then.
func (r *Runner) memorySection(role string) string {
	if r == nil || r.Evolve == nil {
		return ""
	}
	mem := r.Evolve.Memory()
	if mem == nil {
		return ""
	}
	budget := r.MemoryTokens
	if budget <= 0 {
		budget = DefaultMemoryTokens
	}
	block := strings.TrimSpace(mem.RenderForPrompt(role, budget))
	if block == "" {
		return ""
	}
	return "\n\n" + block + "\n"
}

// Edit-format arms. The names are the bandit's arm ids AND the switch keys in
// editFormatSection, so a typo cannot silently render the default section while
// the bandit thinks it chose something else.
const (
	EditFormatSearchReplace = "search_replace"
	EditFormatUnifiedDiff   = "unified_diff"
	EditFormatWholeFile     = "whole_file"
)

// EditFormatArms is the arm set offered to the bandit, in the order the
// bandit sees them (the first is also the fallback).
func EditFormatArms() []string {
	return []string{EditFormatSearchReplace, EditFormatUnifiedDiff, EditFormatWholeFile}
}

// chooseEditFormat asks the bandit which edit format this model/language does
// best with, records the decision ONCE for the orchestrator to drain, and
// returns the arm. Falls back to search_replace when Evolve is nil.
//
// The choice is memoized for the life of the runner. It used to be re-drawn on
// every prompt render, which meant an ε-greedy bandit could hand different
// tasks in the same run different formats, and — worse — appended one
// DecisionRecord per render, so a five-task run folded five updates into the
// posterior for one decision. A decision is per run; so is its reward.
func (r *Runner) chooseEditFormat() string {
	if r == nil || r.Evolve == nil {
		return EditFormatSearchReplace
	}
	r.editFmtOnce.Do(func() {
		arms := EditFormatArms()
		choice := r.Evolve.Choose(evolve.DecEditFormat, arms...)
		if choice.Arm == "" {
			r.editFmt = EditFormatSearchReplace
			return
		}
		r.editFmt = choice.Arm
		r.evo.addDecision(evolve.DecisionRecord{Key: choice.Key, Arm: choice.Arm})
	})
	return r.editFmt
}

// EditFormat reports the arm this run is using ("" when Evolve is nil, i.e.
// when no arm was drawn at all). The orchestrator copies it into
// evolve.RunReport.EditFormat so the reward lands on the arm that was used.
func (r *Runner) EditFormat() string {
	if r == nil || r.Evolve == nil {
		return ""
	}
	return r.chooseEditFormat()
}

// editFormatSection tells the worker which edit format to prefer this run.
//
// Each arm names a DIFFERENT tool and a different payload, so the arm really
// does change what the worker is instructed to emit — and every one of the
// three tools is a path the workspace layer accepts:
//   - search_replace → ws_edit  (old_str/new_str)
//   - unified_diff   → ws_patch (a diff body)
//   - whole_file     → ws_read then ws_write the complete file, which the
//     write guard allows precisely because the file was read this session.
func (r *Runner) editFormatSection() string {
	if r == nil || r.Evolve == nil {
		return ""
	}
	switch r.chooseEditFormat() {
	case EditFormatUnifiedDiff:
		return "\n## Edit format\nPrefer ws_patch with a minimal unified diff for this model.\n"
	case EditFormatWholeFile:
		return "\n## Edit format\nThis model does best rewriting the whole file: ws_read then ws_write the complete new content.\n"
	default:
		return "\n## Edit format\nPrefer ws_edit with an exact SEARCH/REPLACE pair (smallest unique anchor).\n"
	}
}

// detectSignalLanguage is a cheap language tag for fingerprinting.
func detectSignalLanguage(root string) string {
	hint := detectProjectLangHint(root)
	switch {
	case strings.Contains(hint, "Go"):
		return "go"
	case strings.Contains(hint, "Python"):
		return "python"
	case strings.Contains(hint, "JS/TS"):
		return "javascript"
	case strings.Contains(hint, "Rust"):
		return "rust"
	case strings.Contains(hint, "Java"):
		return "java"
	case strings.Contains(hint, "C/C++"):
		return "cpp"
	}
	return ""
}

// applyAdviceAction turns a RepairAction into a concrete loop-side action.
// It returns a guidance line to inject when the action needs the model's help,
// and whether the caller should retry immediately with no LLM round-trip.
func (r *Runner) applyAdviceAction(ctx context.Context, adv evolve.Advice) (guidance string, retryNow bool) {
	if !adv.Apply || adv.Repair.Kind != evolve.RepairAction {
		return "", false
	}
	switch adv.Repair.Action {
	case evolve.ActionCompactContext:
		if r.OnOverflowCompact != nil {
			if err := r.OnOverflowCompact(ctx); err != nil {
				r.logf("evolve: compact_context repair failed: %v", err)
				return adv.Repair.Guidance, false
			}
		}
		return "", adv.Repair.Retry
	case evolve.ActionBackoffRetry:
		return "", adv.Repair.Retry
	case evolve.ActionRereadFile, evolve.ActionForceDifferent, evolve.ActionShortenResponse,
		evolve.ActionRaiseMaxTokens, evolve.ActionSplitTask, evolve.ActionEscalateModel:
		return adv.Repair.Guidance, false
	}
	return adv.Repair.Guidance, false
}

// errMaxRetries is the failure recorded when a task exhausts its review ladder.
var errMaxRetries = errors.New("max retries exceeded: review rejected")

// reportTaskFailure is the single loop-side failure sink: it fingerprints the
// failure for the evolve engine AND writes the structured failure record.
// Nothing in pkg/loop should call FailureHandler.ReportTaskFailure directly —
// doing so is how failures reached errors.md without ever reaching the memory
// that is supposed to stop them happening twice.
func (r *Runner) reportTaskFailure(board *plan.Board, t plan.Task, err error, attempt int,
	phase string, lesson bool) {
	if r == nil || err == nil {
		return
	}
	r.noteFailure(r.taskFailureSignal(t, phase, err), "")
	if r.FailureHandler == nil {
		return
	}
	if rerr := r.FailureHandler.ReportTaskFailure(board, t, err, attempt); rerr != nil {
		r.logf("%s failure report: %v", t.ID, rerr)
	}
	if lesson {
		if lerr := r.FailureHandler.AddWaveLesson(board, t, err); lerr != nil {
			r.logf("%s wave lesson: %v", t.ID, lerr)
		}
	}
}

// RecordToolEvent folds one tool invocation into working memory. It is O(1) and
// does no I/O, so the tool layer can call it after every single tool call.
func (r *Runner) RecordToolEvent(ev memory.ToolEvent) { r.recordTool(ev) }

// ReportToolFailure is the "fail once" entry point for the tool layer.
//
// Call it the moment a tool call fails, with the raw arguments. When newArgs is
// non-empty the caller should retry the SAME tool with those arguments and no
// LLM round-trip at all; when guidance is non-empty it should be injected into
// the next prompt instead.
func (r *Runner) ReportToolFailure(ctx context.Context, ev memory.ToolEvent, sig evolve.Signal) (
	newArgs string, guidance string, retry bool) {
	if r == nil {
		return "", "", false
	}
	ev.OK = false
	if sig.Tool == "" {
		sig.Tool = ev.Tool
	}
	if sig.Message == "" {
		sig.Message = ev.Error
	}
	if sig.Path == "" {
		sig.Path = ev.Path
	}
	if sig.Command == "" {
		sig.Command = ev.Command
	}
	if sig.Language == "" {
		sig.Language = detectSignalLanguage(r.Root)
	}
	adv := r.noteFailure(sig, ev.Args)
	ev.Key = adv.Fingerprint.ID
	r.recordTool(ev)
	if adv.Apply && adv.NewArgs != "" {
		r.logf("evolve: repaired %s arguments deterministically (%s)", ev.Tool, adv.RuleID)
		if editToolNames[strings.ToLower(strings.TrimSpace(ev.Tool))] {
			// The next edit that lands did so on arguments the HARNESS wrote.
			// It still counts as applied; it must never count as first-attempt
			// format compliance.
			r.edits.noteRepair()
		}
		return adv.NewArgs, "", true
	}
	if g, retryNow := r.applyAdviceAction(ctx, adv); g != "" || retryNow {
		return "", g, retryNow
	}
	if adv.Found {
		return "", adv.Repair.Guidance, false
	}
	return "", "", false
}

// ResolveToolFailure credits whatever finally fixed a tool failure.
func (r *Runner) ResolveToolFailure(fp evolve.Fingerprint, ruleID, what string) {
	r.noteResolved(evolve.Advice{Fingerprint: fp, RuleID: ruleID}, what)
}
