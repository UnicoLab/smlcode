package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/repomap"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
)

// buildRunner assembles the inner execute loop.
//
// This used to be ~60 lines of field assignment inlined twice — once in runSLM
// and once in finishFromExecute — which is how the two paths drifted apart
// (Resume never got OnOverflowCompact's ReactCompact notice, for one). One
// constructor, two callers, one behavior.
func (o *Orchestrator) buildRunner(query, runID, skillPack string) *loop.Runner {
	runner := loop.NewRunner(o.executor, o.shared)
	runner.Root = o.cfg.Root
	runner.SlmDir = o.cfg.SlmDir()
	runner.Feedback = o.LiveFeedback
	runner.TurnID = runID
	runner.Store = o.boardStore
	runner.Focus = o.focus
	// The gate side of the ws_shell scope guard. The tool layer records
	// out-of-scope shell writes on the Workspace; the loop drains them after
	// each worker turn for "## Disk evidence" and for the scope verdict.
	if o.workspace != nil {
		runner.TakeShellScope = o.workspace.TakeShellScopeEvents
	}
	runner.MaxRetries = o.cfg.MaxRetries
	runner.MaxParallel = o.cfg.MaxParallel
	runner.ReviewParallel = o.cfg.MaxParallel >= 2
	runner.Timeout = o.cfg.TaskTimeout
	runner.ReviewerRole = o.reviewStrictnessRole(o.Pipeline().Execute.Reviewer)
	runner.CorrectorRole = o.Pipeline().Execute.Corrector
	runner.DefaultRole = o.Pipeline().Execute.DefaultRole
	runner.MaxWaves = o.Pipeline().Execute.MaxWaves
	runner.PostWorkerSmoke = o.cfg.PostWorkerSmoke
	runner.WaveSnapshots = o.cfg.WaveSnapshots
	runner.RewindMgr = o.rewindMgr
	runner.FailureHandler = loop.NewEnhancedFailureHandler(o.cfg.Root)
	runner.QualityMonitor = o.cfg.QualityMonitor
	runner.StaticQuality = o.cfg.StaticQuality
	runner.RequireSmoke = o.cfg.RequireSmoke
	runner.ClaimsGate = o.cfg.ClaimsGate
	runner.WorkerCritique = o.cfg.WorkerCritique
	runner.ThinkPasses = o.cfg.ThinkPasses
	runner.ThinkingBudget = o.cfg.ThinkingBudget
	runner.ThinkingBudgetTokens = o.resolvedProfile().ThinkingBudgetTokens
	if runner.ThinkingBudgetTokens <= 0 {
		runner.ThinkingBudgetTokens = o.cfg.ThinkingBudgetTokens
	}
	runner.AutoTextTools = o.cfg.AutoTextTools
	runner.FinalizeWarn = o.cfg.FinalizeWarn
	runner.ReactCompact = o.cfg.ReactCompact
	runner.ReactCompactAtPercent = o.cfg.ReactCompactAtPercent
	runner.MaxContextKB = o.cfg.MaxContextKB
	// qa_bootstrap reaches the inner loop's acceptance smoke. Unset it defaults
	// to "" → NormalizeBootstrapPolicy → ask, which is the safe policy but not
	// necessarily the CONFIGURED one: an operator who set qa_bootstrap: auto
	// got nothing installed and a red acceptance run they could not explain.
	runner.BootstrapDeps = o.QABootstrapMode()

	runner.Log = func(format string, args ...interface{}) {
		o.emitFull("execute", stream.KindDebug, "", "", fmt.Sprintf(format, args...), "", "")
	}
	// The STRUCTURED sink, not the legacy one. loop.AgentEvent's six string
	// arguments cannot carry Level or Data, so routing the bridge through it
	// dropped exactly the two fields the CLI now depends on: agent_end levels
	// (which color the icon and count ✔/✖) and stream.Token payloads (which
	// render live token text). Only ONE sink is installed — loop.fireEvent
	// mirrors to both when both are set, which would double every event.
	runner.OnEventFull = func(ev loop.LoopEvent) {
		o.emitFullDataL("execute", ev.Kind, ev.Agent, ev.TaskID, ev.Message, ev.Scope, ev.Output,
			ev.Level, ev.Data)
	}
	runner.OnEscalate = func(ctx context.Context, board *plan.Board, t plan.Task, detail string) {
		o.runEscalateAsk(ctx, board, t, detail)
	}
	runner.OnUsage = func(u llm.Usage, estimated bool, _, _ string) {
		o.recordUsage(u, estimated)
		o.bumpLLMCalls(1)
	}
	runner.OnOverflowCompact = func(ctx context.Context) error {
		_, err := o.CompactContextNow()
		if o.cfg.ReactCompact {
			// Force react watchdog rearm so next resume compacts aggressively.
			o.emitFull("execute", stream.KindDebug, "compact", "",
				"overflow: CONTEXT compacted; ReAct will compact on resume", "", "")
		}
		return err
	}
	runner.AfterWave = func(ctx context.Context, board *plan.Board, wave []plan.Task) {
		o.evolveAfterWave(ctx, query, skillPack, board, wave)
		o.maybeCompactContext(ctx)
		o.coordinate(ctx, query, board, "after-wave")
	}
	// The objective gate, asked BETWEEN waves rather than only after the board
	// drains. A board that keeps rejecting one task never drains, so the
	// post-drain probes never fired on the run this exists for — the harness
	// spent ~15 minutes on corrective rounds after the objective was already
	// met. See objectiveMetBetweenWaves and loop.BetweenWaves.
	runner.BetweenWaves = o.objectiveMetBetweenWaves
	runner.BuildInput = o.buildTaskInput(query)

	// Retry-ladder ordering is the inner loop's to execute, but the CHOICE is a
	// policy one, so it is decided and recorded here and handed over through
	// shared state (the loop reads "retry_ladder" from SharedState).
	if o.shared != nil {
		o.shared.SetGlobal("retry_ladder", o.choose(evolve.DecRetryLadder, "correct_first", "retry_first"))
	}

	// max_task_calls and max_retries are one setting in two halves, and the
	// budget wins. worker + self-critique + max_retries × (review + correct) is
	// what a task needs to spend its retries; a budget below that caps
	// max_retries without saying so. Say so.
	budget := o.maxTaskCalls()
	if need := loop.MaxTaskCallsFor(o.cfg.MaxRetries); budget < need {
		o.emitProblem("init", fmt.Sprintf(
			"max_task_calls=%d caps max_retries=%d — a task gets %d correction round(s), not %d "+
				"(worker + self-critique + max_retries × (review + correct) needs %d)",
			budget, o.cfg.MaxRetries, maxCorrectionRounds(budget), o.cfg.MaxRetries, need), "")
	}

	contract := loopContract{
		ContextLimitTokens: o.contextLimitTokens(),
		MaxTaskCalls:       budget,
		ResolveRole:        o.resolveExecRole,
		MemoryTokens:       o.memoryTokens(),
		Evolve:             o.evolve,
		OnTaskStart: func(taskID string) {
			if o.tracker != nil {
				o.tracker.ResetTask(taskID)
			}
			if o.evolve != nil {
				if mem := o.evolve.Memory(); mem != nil {
					if w := mem.Working(); w != nil {
						w.SetTask(taskID)
					}
				}
			}
		},
	}
	o.mu.Lock()
	o.activeRunner = runner
	o.mu.Unlock()

	applyLoopContract(runner, contract)
	// The tool layer can only report to the engine once there IS a runner to
	// report to; buildRunner is the first moment that is true. See
	// tool_observer.go.
	o.installToolObserver()
	return runner
}

// contextLimitTokens is the model's real context window (0 = unknown).
func (o *Orchestrator) contextLimitTokens() int {
	if o == nil {
		return 0
	}
	if n := o.resolvedProfile().ContextLimit; n > 0 {
		return n
	}
	if o.cfg != nil && o.cfg.MaxContextKB > 0 {
		return contextstore.TokensFromKB(o.cfg.MaxContextKB)
	}
	return 0
}

// buildTaskInput builds one worker prompt with a relevance-windowed pack.
//
// The old version passed only `query` to the packer and stapled TaskID/TaskTitle
// onto the finished pack afterwards, which is too late: the excerpt engine keys
// its relevance windows on the task's title, description and acceptance, so a
// pack built from the query alone gets markedly worse windows for exactly the
// files the task is about.
func (o *Orchestrator) buildTaskInput(query string) func(plan.Task) string {
	return func(t plan.Task) string {
		lean := loop.StripScopedPack(t.Description)
		// Architect/editor pair: the describer half runs first and its prose
		// becomes the editor's instruction. BuildInput has no ctx, so the
		// describer runs on the background context bounded by its own role
		// timeout inside runRoleTracked.
		if t.Role == agents.RoleEditor {
			if framed := o.describeForEditor(context.Background(), query, t); framed != "" {
				lean = framed
			}
		}
		tp, _ := o.packBuildReq(contextstore.BuildRequest{
			Role:            t.Role,
			Query:           query,
			TaskID:          t.ID,
			TaskTitle:       t.Title,
			TaskDescription: lean,
			Acceptance:      t.Acceptance,
			Docs:            contextstore.LeanDocsForRole(t.Role),
			Files:           t.Files,
			// The TASK's own files are the sharpest scope available — sharper
			// than the whole board — so a task that only touches *.py cannot
			// pull in a skill declared `paths: ["**/*.rs"]`.
			SkillsMarkdown: o.skillPackForScoped(t.Role, query, o.taskFileScope(t)),
			FocusTerms:     focusTermsFor(t),
		})
		t.Description = tp.Render() + "\n## Task instructions\n\n" + lean
		return o.formatWorkerPrompt(query, t)
	}
}

// taskFileScope is the path scope for one task's skill gate: its own files,
// falling back to the run's scope when the task declares none (a scope of
// nothing would disable gating rather than tighten it).
func (o *Orchestrator) taskFileScope(t plan.Task) []string {
	if len(t.Files) > 0 {
		return t.Files
	}
	return o.runFileScope()
}

// focusTermsFor gives the excerpt engine the identifiers the task already
// names, so it does not have to rediscover them from prose.
func focusTermsFor(t plan.Task) []string {
	return contextstore.ExtractTerms(t.Title, t.Acceptance, strings.Join(t.Files, " "))
}

// taskContext tags ctx with the task id so the workspace tool layer can scope
// its loop guard. pkg/loop sets this too; doing it here keeps orchestrator-side
// tool calls (QA gate, placeholder pass) attributable.
func taskContext(ctx context.Context, taskID string) context.Context {
	if strings.TrimSpace(taskID) == "" {
		return ctx
	}
	return workspace.WithTaskID(ctx, taskID)
}

// lastRunner returns the inner loop built for the in-flight run (nil when the
// run never reached execute).
func (o *Orchestrator) lastRunner() *loop.Runner {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.activeRunner
}

// reviewStrictnessRole lets the bandit pick how strict review is for this run.
//
// "strict" routes to agents.RoleReviewerStrict, which approves only on
// complete demonstrated evidence; "lenient" and "normal" both use the
// configured reviewer — the difference between them is carried to the loop
// through shared state so it can size its evidence bar.
func (o *Orchestrator) reviewStrictnessRole(configured string) string {
	if o == nil || o.evolve == nil {
		return configured
	}
	arm := o.choose(evolve.DecReviewStrictness, "normal", "strict", "lenient")
	if o.shared != nil {
		o.shared.SetGlobal("review_strictness", arm)
	}
	if arm == "strict" && o.knownAgent(agents.RoleReviewerStrict) {
		return agents.RoleReviewerStrict
	}
	return configured
}

// repoMapNow returns the current repo map (nil when none was built).
func (o *Orchestrator) repoMapNow() *repomap.Map {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.repoMap
}

// refreshRepoMap rebuilds the ranked symbol index for this run and re-attaches
// it to the packer. It is cached under .slmcode, so a rebuild is incremental —
// but it must happen per RUN, not per orchestrator: the previous run's own
// edits are exactly the files the next run most needs indexed.
func (o *Orchestrator) refreshRepoMap() {
	if o == nil || o.cfg == nil {
		return
	}
	rm, err := repomap.Build(o.cfg.Root, repomap.Options{CacheDir: o.cfg.SlmDir()})
	if err != nil {
		o.emitFull("init", stream.KindDebug, "repomap", "",
			"repo map refresh failed: "+err.Error(), "", "")
		return
	}
	o.mu.Lock()
	o.repoMap = rm
	o.mu.Unlock()
	if o.packer != nil {
		o.setPackerRepoMap(rm)
	}
}

// maxCorrectionRounds reports how many review+correct rounds a per-task call
// budget actually pays for after the worker and self-critique passes.
func maxCorrectionRounds(budget int) int {
	if n := (budget - 2) / 2; n > 0 {
		return n
	}
	return 0
}
