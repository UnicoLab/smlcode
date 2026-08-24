package orchestrator

import (
	"context"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/graph"
	"github.com/UnicoLab/slmcode/pkg/learning"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// startEvolveRun seeds the memory run context and surfaces engine warnings.
// Called from Run once the run id and query are known — every later
// RenderForPrompt and RecallEpisodes keys on this context.
func (o *Orchestrator) startEvolveRun(runID, query string) {
	if o == nil || o.evolve == nil {
		return
	}
	for _, w := range o.evolve.Warnings() {
		if strings.TrimSpace(w) == "" {
			continue
		}
		o.emitFull("init", stream.KindDebug, "evolve", "", "evolve: "+w, "", "")
	}
	mem := o.evolve.Memory()
	if mem == nil {
		return
	}
	files := []string(nil)
	if o.cfg != nil {
		files = plan.DiscoverRelevantFiles(o.cfg.Root, query, "")
		if rm := o.repoMapNow(); rm != nil {
			// Seed focus discovery from the ranked symbol index, which knows
			// about identifiers the filename-based discovery cannot see.
			files = mergeUnique(files, rm.RankFilesFor(query, 8))
		}
	}
	rc := memory.RunContext{RunID: runID, Query: query, Files: files}
	if o.cfg != nil {
		rc.Language = detectProjectLang(o.cfg.Root)
		rc.Model = o.cfg.Model
		rc.Provider = o.cfg.Provider
	}
	mem.SetRunContext(rc)
}

// recordLessonFacts routes prose lessons into typed semantic memory.
//
// MEMORY.md keeps its bullets — this is additive, and a human still reads that
// file. What the fact store adds is everything the flat Markdown could never
// have: a Beta-posterior confidence, so a lesson seen twice outranks one seen
// once; contradiction and supersession, so a claim the project has outgrown
// decays instead of being repeated forever; queryable provenance in
// Fact.Sources; and a prune policy, so the store stays bounded.
//
// Nil-safe from top to bottom: with `--no-evolve`, or with memory disabled, the
// lessons simply stay prose.
func (o *Orchestrator) recordLessonFacts(lessons []learning.Lesson) {
	if o == nil || o.evolve == nil || len(lessons) == 0 {
		return
	}
	mem := o.evolve.Memory()
	if mem == nil {
		return
	}
	facts := mem.Semantic()
	if facts == nil {
		return
	}
	n := learning.RecordFacts(facts, lessons, mem.RunContext().RunID)
	if n == 0 {
		return
	}
	// Flush now rather than at Close: a wave's lessons are worth keeping even
	// if the run is interrupted before it ends, and facts.json is small.
	if err := facts.Flush(); err != nil {
		o.emitFull("learn", stream.KindDebug, "memory", "", "facts flush: "+err.Error(), "", "")
		return
	}
	o.emitFull("learn", stream.KindLearn, "memory", "",
		itoa(n)+" lesson(s) folded into semantic memory", "", "")
}

// closeEvolve releases the engine (flushes memory to disk). Safe to call twice.
func (o *Orchestrator) closeEvolve() {
	if o == nil || o.evolve == nil {
		return
	}
	if err := o.evolve.Close(); err != nil {
		o.emitFull("done", stream.KindDebug, "evolve", "", "evolve close: "+err.Error(), "", "")
	}
}

// Close releases every resource the orchestrator owns: MCP stdio subprocesses
// and the self-improvement engine.
//
// mcp.Manager.Close had ZERO call sites, so every stdio MCP server leaked for
// the process lifetime — and `slmcode studio` rebuilds orchestrators, so the
// subprocesses accumulated. Manager satisfies io.Closer and Close is
// idempotent, so callers can defer this without bookkeeping.
func (o *Orchestrator) Close() error {
	if o == nil {
		return nil
	}
	var err error
	if o.mcpMgr != nil {
		err = o.mcpMgr.Close()
	}
	o.closeEvolve()
	return err
}

// choose asks the bandit for an arm and records the decision for the run
// report. It is nil-safe: with no engine it returns the first arm, which is
// always the previous hardcoded behavior.
func (o *Orchestrator) choose(decision evolve.Decision, arms ...string) string {
	if len(arms) == 0 {
		return ""
	}
	if o == nil || o.evolve == nil {
		return arms[0]
	}
	c := o.evolve.Choose(decision, arms...)
	if strings.TrimSpace(c.Arm) == "" {
		return arms[0]
	}
	o.mu.Lock()
	o.decisions = append(o.decisions, evolve.DecisionRecord{Key: c.Key, Arm: c.Arm})
	o.mu.Unlock()
	o.emitFull("init", stream.KindDebug, "evolve", "",
		"policy "+string(decision)+" → "+c.Arm, "", o.evolve.Why(decision))
	return c.Arm
}

// recordGate appends a quality-gate outcome to the run report.
func (o *Orchestrator) recordGate(name string, passed bool, detail string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.gates = append(o.gates, evolve.GateResult{Name: name, Passed: passed, Detail: detail})
	o.mu.Unlock()
}

func (o *Orchestrator) snapshotDecisions() []evolve.DecisionRecord {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]evolve.DecisionRecord(nil), o.decisions...)
}

func (o *Orchestrator) snapshotGates() []evolve.GateResult {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]evolve.GateResult(nil), o.gates...)
}

// finishEvolveRun builds the full RunReport and hands it to the engine.
//
// Finish records the episode, credits/debits used rules, learns new candidate
// rules, updates the bandit, stores regression checks, distills semantic
// memory, prunes every store and writes REFLECTION.md. RecordMetrics then
// appends one row to .slmcode/metrics/runs.jsonl.
//
// A nil summarizer is supported: reflection is deterministic and the LLM
// commentary is strictly additive.
func (o *Orchestrator) finishEvolveRun(ctx context.Context, res *Result, board *plan.Board, runner *loop.Runner) {
	if o == nil || o.evolve == nil || res == nil {
		return
	}
	rep := o.buildRunReport(res, board, runner)
	ref, err := o.evolve.Finish(ctx, rep, o.memorySummarizer())
	if err != nil {
		o.emitFull("done", stream.KindDebug, "evolve", "", "reflect: "+err.Error(), "", "")
	}
	if o.cfg != nil {
		if merr := evolve.RecordMetrics(o.cfg.Root, rep, ref); merr != nil {
			o.emitFull("done", stream.KindDebug, "evolve", "", "metrics: "+merr.Error(), "", "")
		}
		// Materialize the edges the records just written already imply. Best
		// effort by design: the graph is derived data, so losing it costs one
		// backfill and must never cost a run.
		if n, gerr := graph.Backfill(o.cfg.Root); gerr != nil {
			o.emitFull("done", stream.KindDebug, "graph", "", "graph backfill: "+gerr.Error(), "", "")
		} else if n > 0 {
			o.emitFull("done", stream.KindDebug, "graph", "", "graph: +"+itoa(n)+" edge(s)", "", "")
		}
	}
	if strings.TrimSpace(ref.Markdown) != "" {
		o.emitFull("done", stream.KindLearn, "evolve", "",
			evolveHeadline(ref), "", truncate(ref.Markdown, 1200))
	}
}

// memorySummarizer adapts the orchestrator's LLM to memory.Summarizer.
// Reflection is deterministic; the model commentary it adds is advisory only,
// so returning an error (or passing nil here) leaves the report unchanged.
func (o *Orchestrator) memorySummarizer() memory.Summarizer {
	if o == nil || o.executor == nil {
		return nil
	}
	return func(ctx context.Context, prompt string) (string, error) {
		return o.runRole(ctx, "memory", prompt)
	}
}

func evolveHeadline(ref evolve.Reflection) string {
	return "reflection: " +
		itoa(ref.ResolvedFromMemory) + " fixed from memory, " +
		itoa(ref.ResolvedFromLLM) + " from a fresh call, " +
		itoa(ref.Unresolved) + " unresolved · " +
		itoa(len(ref.Candidates)) + " candidate rule(s)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// buildRunReport assembles everything the finished run knows about itself.
func (o *Orchestrator) buildRunReport(res *Result, board *plan.Board, runner *loop.Runner) evolve.RunReport {
	rep := evolve.RunReport{
		RunID:   res.ID,
		Query:   res.Query,
		Summary: res.Summary,
	}
	o.mu.Lock()
	rep.StartedAt = o.runStart
	rep.LLMCalls = o.llmCalls
	o.mu.Unlock()
	if rep.StartedAt.IsZero() {
		rep.StartedAt = time.Now().Add(-res.Duration)
	}
	rep.EndedAt = rep.StartedAt.Add(res.Duration)
	if o.cfg != nil {
		rep.Language = detectProjectLang(o.cfg.Root)
		rep.Model = o.cfg.Model
		rep.Provider = o.cfg.Provider
	}
	if u := res.Usage; u != nil {
		rep.TokensIn = u.PromptTokens
		rep.TokensOut = u.CompletionTokens
	}
	rep.Gates = o.snapshotGates()
	rep.Decisions = o.snapshotDecisions()

	if board != nil {
		files := map[string]bool{}
		for _, t := range board.Tasks {
			t.Normalize()
			rep.PlannedTasks++
			switch t.Column {
			case plan.ColDone:
				rep.CompletedTasks++
			case plan.ColBlocked:
				rep.BlockedTasks++
			}
			rep.Retries += t.Retries
			for _, f := range t.Files {
				if f = strings.TrimSpace(f); f != "" {
					files[f] = true
				}
			}
		}
		for f := range files {
			rep.FilesChanged = append(rep.FilesChanged, f)
		}
	}

	// Working memory has the authoritative tool counters and command log.
	if mem := o.evolve.Memory(); mem != nil {
		if w := mem.Working(); w != nil {
			calls, errs, redundant := w.Counters()
			rep.ToolCalls, rep.ToolErrors, rep.RedundantCalls = calls, errs, redundant
			snap := w.Snapshot()
			for _, c := range snap.Commands {
				rep.Commands = append(rep.Commands, memory.Command{
					Cmd: c.Command,
					OK:  strings.EqualFold(strings.TrimSpace(c.Status), "ok"),
				})
			}
			seenTool := map[string]bool{}
			for _, e := range snap.Events {
				if e.Tool == "" || seenTool[e.Tool] {
					continue
				}
				seenTool[e.Tool] = true
				rep.ToolsUsed = append(rep.ToolsUsed, e.Tool)
			}
			rep.FilesChanged = mergeUnique(rep.FilesChanged, snap.FilesEdited)
		}
	}

	// Edit accounting: read BEFORE draining, because DrainDecisionRecords
	// finalizes the edit-format arm's outcome from the same ledger.
	rep.EditFormat, rep.EditsAttempted, rep.EditsApplied, rep.EditsFirstAttempt =
		runnerEditStats(runner)

	// Failures + decisions the inner loop accumulated.
	fe, dr := drainRunnerEvolve(runner)
	rep.Failures = append(rep.Failures, fe...)
	rep.Decisions = append(rep.Decisions, dr...)
	return rep
}

func mergeUnique(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, v := range list {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
