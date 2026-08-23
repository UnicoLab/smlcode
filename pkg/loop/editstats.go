package loop

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// Edit-format accounting.
//
// The edit-format bandit arm (evolve.DecEditFormat) picks between
// search_replace, unified_diff and whole_file, and the prompt tells the worker
// which one to use. For that arm to LEARN anything, the outcome fed back has to
// be about the edit format — specifically whether the edit applied ON FIRST
// ATTEMPT. Rewarding "the task eventually succeeded" teaches the bandit that
// every arm works, because the harness recovers from a bad edit format by
// retrying with a different one; the run succeeds and the format that failed
// twice gets the credit.
//
// This file counts the three numbers that answer the question, straight from
// the agent's own transcript — which the loop already receives on every
// SubAgentResult — so nothing new has to be wired through the tool layer.

// editToolNames are the workspace tools whose call IS an edit attempt.
// ws_write is included: it is the `whole_file` arm's tool, and since the write
// guard gained its read-this-session escape hatch, rewriting a file that was
// read is a legitimate edit rather than a refused overwrite.
var editToolNames = map[string]bool{
	"ws_write": true, "ws_edit": true, "ws_patch": true,
	// Aliases some tool registries expose.
	"write": true, "edit": true, "patch": true,
}

// editAppliedMarkers are the workspace's own success lines. Matching on SUCCESS
// rather than on failure is deliberate: the guards return their refusals as
// ordinary strings with a nil error (see workspace.WriteRefuseReason), so a
// "not an error" test would score every refusal as an applied edit.
var editAppliedMarkers = []string{
	"edited ",      // ws_edit:  edited <path> (N replacement(s))
	"patched ",     // ws_patch: patched <path> (N hunk(s) applied)
	"wrote ",       // ws_write: wrote <path> (N bytes)
	"overwrote ",   // ws_write: overwrote <path> (N bytes)
	"staged write", // review queue — the edit was accepted, a human gates it
	"dry-run: would edit",
	"dry-run: would write",
	"dry-run: would patch",
}

// editApplied classifies one tool RESULT.
func editApplied(result string) bool {
	s := strings.ToLower(strings.TrimSpace(result))
	if s == "" || strings.HasPrefix(s, "error:") {
		return false
	}
	for _, m := range editAppliedMarkers {
		if strings.HasPrefix(s, m) || strings.Contains(s, "\n"+m) {
			return true
		}
	}
	return false
}

// editStats is the per-run edit ledger. Waves and parallel reviews both write
// to it, so every method takes the lock.
type editStats struct {
	mu        sync.Mutex
	attempted int
	applied   int
	first     int
	// repairs counts argument rewrites the harness handed back to the tool
	// layer (ReportToolFailure). The next edit that lands consumes one and is
	// booked as applied-but-not-first: it worked, but not as the model emitted
	// it, which is exactly the difference the two metrics exist to record.
	repairs int
	// counted dedupes tool calls across re-scans. A resumed ReAct task is handed
	// its own earlier transcript, so without this every resume would re-count
	// the edits that happened before the interrupt.
	counted map[string]bool
}

func (s *editStats) noteRepair() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.repairs++
	s.mu.Unlock()
}

// note books one edit tool call. id must be stable for the same call.
func (s *editStats) note(id string, applied bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.counted == nil {
		s.counted = map[string]bool{}
	}
	if s.counted[id] {
		return
	}
	s.counted[id] = true
	s.attempted++
	if !applied {
		return
	}
	s.applied++
	if s.repairs > 0 {
		s.repairs-- // landed, but on arguments the harness rewrote
		return
	}
	s.first++
}

func (s *editStats) snapshot() (attempted, applied, first int) {
	if s == nil {
		return 0, 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempted, s.applied, s.first
}

// ---------------------------------------------------------------------------
// Transcript scanning
// ---------------------------------------------------------------------------

// noteEdits folds one agent transcript into the ledger.
//
// GoLangGraph returns the whole conversation on every SubAgentResult, so an
// assistant message's tool calls and the matching tool-result messages are both
// here. That makes this the only edit signal in the harness that needs no new
// plumbing through pkg/workspace.
func (r *Runner) noteEdits(msgs []llm.Message) {
	if r == nil || len(msgs) == 0 {
		return
	}
	// Index tool results by the call they answer.
	results := make(map[string]string, len(msgs))
	for _, m := range msgs {
		if strings.EqualFold(m.Role, "tool") && m.ToolCallID != "" {
			results[m.ToolCallID] = m.Content
		}
	}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			name := strings.ToLower(strings.TrimSpace(tc.Function.Name))
			if !editToolNames[name] {
				continue
			}
			res, ok := results[tc.ID]
			if !ok {
				// The call is in the transcript but its result is not — the turn
				// was interrupted, or the provider emitted no tool-call id to
				// match on. Either way it is not an attempt we can score, and
				// booking it as a failure would invent format non-compliance.
				continue
			}
			r.edits.note(editCallID(tc), editApplied(res))
		}
	}
}

// editCallID is the dedupe key for one tool call: its id when the provider set
// one, otherwise the tool plus its arguments. Two byte-identical edit calls in
// one run therefore count once — which is correct anyway, because the tool
// layer's loop guard refuses the repeat rather than executing it.
func editCallID(tc llm.ToolCall) string {
	if id := strings.TrimSpace(tc.ID); id != "" {
		return id
	}
	return tc.Function.Name + "\x00" + canonicalArgs(tc.Function.Arguments)
}

// canonicalArgs normalises JSON arguments so key order does not create a second
// identity for the same call.
func canonicalArgs(args string) string {
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return strings.TrimSpace(args)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(args)
	}
	return string(b)
}

// EditStats reports the run's edit-format accounting:
//
//	attempted    — every ws_edit / ws_patch / ws_write call the agents made
//	applied      — those that landed, including ones the harness had to repair
//	firstAttempt — those that landed exactly as the model emitted them
//
// firstAttempt is the number the edit-format bandit is rewarded on, and the one
// that belongs in metrics.Metrics.EditsFirstAttempt. The orchestrator uses all
// three to fill evolve.RunReport, which previously reported zero edits for
// every run and so recorded a procedural memory of "0/0 hunks applied".
func (r *Runner) EditStats() (attempted, applied, firstAttempt int) {
	if r == nil {
		return 0, 0, 0
	}
	return r.edits.snapshot()
}

// ---------------------------------------------------------------------------
// Feeding the bandit
// ---------------------------------------------------------------------------

// editFormatOutcome turns the ledger into the reward the edit-format arm earns.
//
// Applied is TRUE only when every edit attempt landed as emitted: that is the
// arm doing its job. Each attempt that needed a repair or a retry is booked as
// a retry (Outcome.Reward penalizes up to three), and an arm that never landed
// anything is a hard failure. A run with no edits at all returns ok=false — an
// unexercised arm must not be rewarded OR punished, or the bandit converges on
// whichever format happened to be chosen for the planning-only runs.
func editFormatOutcome(attempted, applied, first int) (evolve.Outcome, bool) {
	if attempted <= 0 {
		return evolve.Outcome{}, false
	}
	return evolve.Outcome{
		Applied: first == attempted,
		Retries: attempted - first,
		Failed:  applied == 0,
	}, true
}

// finalizeEditDecisions stamps the measured outcome onto every edit-format
// decision before the orchestrator drains it.
//
// Without this the records went out with a zero Outcome, whose Reward() is the
// same ~0.31 for every arm — so the bandit was updated the same amount whatever
// it chose, and DecEditFormat could never move off its prior. That is the whole
// defect: the arm existed, the prompt changed, and the feedback was constant.
func (r *Runner) finalizeEditDecisions(in []evolve.DecisionRecord) []evolve.DecisionRecord {
	if r == nil || len(in) == 0 {
		return in
	}
	attempted, applied, first := r.EditStats()
	out, ok := editFormatOutcome(attempted, applied, first)
	kept := in[:0]
	for _, rec := range in {
		if rec.Key.Decision != evolve.DecEditFormat {
			kept = append(kept, rec)
			continue
		}
		if !ok {
			// The run made no edits, so this arm was never exercised. Dropping
			// the record is the only honest option: a zero Outcome would still
			// have folded a reward into the posterior.
			continue
		}
		rec.Outcome = out
		kept = append(kept, rec)
	}
	return kept
}
