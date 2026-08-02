package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/augment"
	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/hooks"
	"github.com/UnicoLab/slmcode/pkg/instructions"
	"github.com/UnicoLab/slmcode/pkg/learning"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/mcp"
	"github.com/UnicoLab/slmcode/pkg/multipass"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/retrieval"
	"github.com/UnicoLab/slmcode/pkg/rewind"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/skills"
	"github.com/UnicoLab/slmcode/pkg/stream"
	"github.com/UnicoLab/slmcode/pkg/workspace"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// Event is streamed live to CLI + Studio (Antigravity-style progress).
type Event = stream.Event

type EventHandler func(Event)

type Result struct {
	ID          string     `json:"id"`
	Query       string     `json:"query"`
	Board       plan.Board `json:"board"`
	Success     bool       `json:"success"`
	FailedTasks int        `json:"failed_tasks"`
	Duration    time.Duration
	Summary     string `json:"summary"`
	Backend     string `json:"backend"`
	// LatencyMs is wall time per phase/role for SLM tuning (plan/split/worker/…).
	LatencyMs map[string]int64 `json:"latency_ms,omitempty"`
	// Usage aggregates prompt/completion tokens (estimated when providers omit on early_exit).
	Usage *TokenUsage `json:"usage,omitempty"`
}

type Orchestrator struct {
	cfg           *config.Config
	store         *contextstore.Store
	boardStore    *plan.LiveStore
	packer        *contextstore.Packer
	skills        *skills.Loader
	llm           *llm.ProviderManager
	tools         *tools.ToolRegistry
	focus         *workspace.FocusGuard
	factory       *agents.Factory
	registry      *ggagent.AgentRegistry
	executor      loop.SubAgentRunner
	shared        *ggagent.SharedState
	think         *multipass.Runner
	claude        *backends.ClaudeCodeRunner
	onEvent       EventHandler
	onAsk         AskHandler
	onPlanApprove PlanApproveHandler
	onContinue    ContinueHandler

	hooksRunner *hooks.Runner
	mcpMgr      *mcp.Manager
	rewindMgr   *rewind.Manager
	waveCounter int
	pipe        *pipeline.Config // config-driven phases / slots / loop agents

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc

	// currentTurn scopes plan/tasks/summary to one user query (rewritten each Run).
	currentTurn *session.Turn

	// latencyMs accumulates phase/role durations for the in-flight run.
	latencyMs map[string]int64
	// usage accumulates token counts for the in-flight run.
	usage TokenUsage
}

func New(cfg *config.Config) (*Orchestrator, error) {
	if cfg == nil {
		cfg = config.Default("")
	}
	cfg.ResolveAPIKey()
	store := contextstore.New(cfg.SlmDir())
	boardStore := plan.NewLiveStore(cfg.SlmDir())
	_ = boardStore.Load()
	packer := contextstore.NewPacker(store, cfg.Root, cfg.MaxContextKB)

	bundledDir := filepath.Join(cfg.SlmDir(), "skills", "_bundled")
	_ = skills.MaterializeBundled(bundledDir)
	skillRoots := []string{
		filepath.Join(cfg.SlmDir(), "skills"),
		bundledDir,
	}
	skillRoots = append(skillRoots, globalSkillRoots()...)
	skillRoots = append(skillRoots, cfg.SkillsDirs...)
	loader := skills.NewLoader(skillRoots...)

	llmManager := llm.NewProviderManager()
	if err := backends.RegisterLLM(llmManager, cfg); err != nil {
		return nil, err
	}

	var o *Orchestrator
	focus := workspace.NewFocusGuard()
	toolReg := tools.NewToolRegistry()

	var hooksRunner *hooks.Runner
	if cfg.HooksEnabled {
		hc, _ := hooks.Load(hooks.DefaultPath(cfg.SlmDir()))
		if len(hc.Hooks) > 0 {
			hooksRunner = &hooks.Runner{Root: cfg.Root, Cfg: hc}
		}
	}

	if err := workspace.RegisterCodingToolsOpts(toolReg, cfg.Root, workspace.ToolOpts{
		ShellPermission: cfg.ShellPermission,
		DryRun:          cfg.DryRun, Permission: cfg.Permission, SlmDir: cfg.SlmDir(),
		Focus: focus, Hooks: hooksRunner,
		ShellAskTimeout:        cfg.ShellAskTimeout,
		AutoApprove:            cfg.AutoApprove,
		DisableWriteGuard:      !cfg.WriteGuard,
		DisableReadBeforeEdit:  !cfg.ReadBeforeEdit,
		DisableShellWriteGuard: !cfg.ShellWriteGuard,
		DisableOverEditGuard:   !cfg.OverEditGuard,
		ReadHeadLines:          cfg.ReadHeadLines,
		MaxContextKB:           cfg.MaxContextKB,
		QualityMonitor:         cfg.QualityMonitor,
		ShellWhitelist:         cfg.ShellWhitelist,
		ShellAllow:             cfg.ShellAllow,
		Checkpoints:            cfg.FileCheckpoints,
		OnIntervention: func(reason, message string) {
			if o != nil {
				code := quality.ClassifyIntervention(reason)
				o.emitFull("execute", stream.KindIntervention, "harness", "",
					message, code, reason)
			}
		},
		OnFileChange: func(path, kind, detail string) {
			if o != nil {
				o.emitFull("execute", stream.KindFileChange, "worker", "",
					fmt.Sprintf("%s %s", kind, path), path, detail)
			}
		},
		OnShellAsk: func(ask workspace.ShellAsk) {
			if o != nil {
				b, _ := json.Marshal(ask)
				o.emitFull("execute", stream.KindAsk, "shell", "",
					"shell approval required: "+truncate(ask.Command, 120), "", string(b))
			}
		},
	}); err != nil {
		return nil, err
	}

	var mcpMgr *mcp.Manager
	if len(cfg.MCPServers) > 0 {
		mcpMgr = &mcp.Manager{Log: func(f string, a ...interface{}) {
			if o != nil {
				o.emitFull("init", stream.KindDebug, "mcp", "", fmt.Sprintf(f, a...), "", "")
			}
		}}
		for _, sc := range cfg.MCPServers {
			ro := true
			if sc.ReadOnly != nil {
				ro = *sc.ReadOnly
			}
			mcpMgr.Servers = append(mcpMgr.Servers, mcp.ServerConfig{
				Name: sc.Name, Command: sc.Command, Args: sc.Args, Env: sc.Env,
				URL: sc.URL, ReadOnly: ro,
			})
		}
		infos, _ := mcpMgr.Connect(context.Background())
		_ = mcpMgr.RegisterTools(toolReg, infos)
	}

	// AgentConfig.Provider must match a name registered in the ProviderManager.
	providerName := config.NormalizeProvider(cfg.Provider)
	cfg.Provider = providerName
	factory := agents.NewFactory(llmManager, toolReg, cfg.Model, providerName)
	factory.CustomDirs = append([]string{cfg.AgentsDir()}, agents.GlobalAgentRoots()...)
	if prof := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model); prof.MaxTokens > 0 || prof.MaxTurns > 0 || prof.Temperature > 0 {
		factory.ProfileMaxTokens = prof.MaxTokens
		factory.ProfileMaxTurns = prof.MaxTurns
		factory.ProfileTemp = prof.Temperature
	}

	// Auto-register providers for per-agent overrides (custom agents / builtin patches)
	// so rebuild never leaves an agent pointing at an unregistered provider.
	var agentOverrides []backends.AgentProviderOverride
	for _, n := range factory.ProviderOverrides() {
		agentOverrides = append(agentOverrides, backends.AgentProviderOverride{
			Provider: n.Provider, Model: n.Model, Endpoint: n.Endpoint,
		})
	}
	if err := backends.EnsureAgentProviders(llmManager, cfg, agentOverrides); err != nil {
		return nil, err
	}

	registry, err := factory.BuildRegistry()
	if err != nil {
		return nil, err
	}
	exec := ggagent.NewSubAgentExecutor(registry)
	exec.SetParallel(true)
	exec.SetTimeout(cfg.TaskTimeout)

	o = &Orchestrator{
		cfg:         cfg,
		store:       store,
		boardStore:  boardStore,
		packer:      packer,
		skills:      loader,
		llm:         llmManager,
		tools:       toolReg,
		focus:       focus,
		factory:     factory,
		registry:    registry,
		executor:    exec,
		shared:      ggagent.NewSharedState(),
		think:       multipass.New(thinkRefinePasses(cfg.ThinkPasses)),
		claude:      backends.NewClaudeCodeRunner(cfg),
		onEvent:     func(Event) {},
		hooksRunner: hooksRunner,
		mcpMgr:      mcpMgr,
		rewindMgr:   &rewind.Manager{SlmDir: cfg.SlmDir(), Root: cfg.Root},
	}
	_ = pipeline.EnsureFile(cfg.SlmDir())
	o.loadPipelineLocked()
	if hooksRunner != nil {
		hooksRunner.Log = func(f string, a ...interface{}) {
			o.emitFull("execute", stream.KindDebug, "hook", "", fmt.Sprintf(f, a...), "", "")
		}
	}
	boardStore.OnChange(func(b *plan.Board) {
		p, t := b.ToMarkdown()
		_ = store.Write(contextstore.DocPlan, p)
		_ = store.Write(contextstore.DocTasks, t)
	})
	return o, nil
}

func (o *Orchestrator) OnEvent(h EventHandler)       { o.onEvent = h }
func (o *Orchestrator) Store() *contextstore.Store   { return o.store }
func (o *Orchestrator) Board() *plan.LiveStore       { return o.boardStore }
func (o *Orchestrator) Skills() *skills.Loader       { return o.skills }
func (o *Orchestrator) Config() *config.Config       { return o.cfg }
func (o *Orchestrator) Packer() *contextstore.Packer { return o.packer }

func (o *Orchestrator) Stop() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancel != nil {
		o.cancel()
	}
}

func (o *Orchestrator) Run(ctx context.Context, query string) (*Result, error) {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return nil, fmt.Errorf("a run is already in progress")
	}
	ctx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	o.running = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.running = false
		o.cancel = nil
		o.mu.Unlock()
		cancel()
	}()

	start := time.Now()
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}
	runID := fmt.Sprintf("run-%d", start.UnixNano())
	if o.packer != nil {
		o.packer.ClearCache()
	}
	o.mu.Lock()
	o.latencyMs = map[string]int64{}
	o.usage = TokenUsage{}
	o.mu.Unlock()

	o.emit("init", "starting "+runID, "")
	// Fresh query-scoped plan/tasks — never patch the previous interaction's board
	// as if it were the same job. Prior turns enrich MEMORY via summaries only.
	turn, terr := session.BeginTurn(o.cfg.SlmDir(), runID, query)
	if terr != nil {
		return nil, fmt.Errorf("begin query turn: %w", terr)
	}
	o.mu.Lock()
	o.currentTurn = turn
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.currentTurn = nil
		o.mu.Unlock()
	}()
	if o.boardStore != nil {
		_ = o.boardStore.Replace(turn.Board)
	}
	o.emit("init", "query-scoped plan/tasks reset for "+runID, "")

	_ = o.store.SetQuery(query)
	o.injectPriorKnowledge(ctx, query)
	o.shared.SetGlobal("query", query)
	o.shared.SetGlobal("query_id", runID)
	o.shared.SetGlobal("root", o.cfg.Root)
	o.shared.SetGlobal("mode", o.cfg.Mode)
	if o.cfg.Specialist != "" {
		o.shared.SetGlobal("specialist", o.cfg.Specialist)
	}

	roleHint := ""
	if o.cfg.Mode == config.ModeSpecialist {
		roleHint = o.cfg.Specialist
	}
	matched := o.skills.ResolveForRun(query, roleHint, o.cfg.PinnedSkills, 4)
	skillPack := skills.RenderPack(matched, 2000)
	if len(matched) > 0 {
		names := make([]string, len(matched))
		for i, s := range matched {
			names[i] = s.Name
		}
		o.emit("skills", "loaded: "+strings.Join(names, ", "), "")
	}
	if refs, _ := skills.ExtractRefs(query); len(refs) > 0 {
		o.emit("skills", "referenced: "+strings.Join(refs, ", "), "")
	}

	if o.cfg.Backend == config.BackendClaudeCode {
		return o.runClaudeCode(ctx, runID, query, skillPack, start)
	}
	if o.cfg.Mode == config.ModeSpecialist {
		return o.runSpecialist(ctx, runID, query, start)
	}
	return o.runSLM(ctx, runID, query, skillPack, start)
}

// skillPackFor builds a role-targeted skill pack (pins + @skill + agent defaults).
func (o *Orchestrator) skillPackFor(role, query string) string {
	list := o.skills.ResolveForRun(query, role, o.cfg.PinnedSkills, 4)
	return skills.RenderPack(list, 1600)
}

// thinkRefinePasses maps config think_passes → multipass critique loops.
// think_passes=1 → unused (single-shot path). =2 → one critique. ≥3 → two.
func thinkRefinePasses(thinkPasses int) int {
	if thinkPasses <= 1 {
		return 1
	}
	if thinkPasses == 2 {
		return 1
	}
	return 2
}

func (o *Orchestrator) runSpecialist(ctx context.Context, runID, query string, start time.Time) (*Result, error) {
	role := strings.TrimSpace(o.cfg.Specialist)
	if role == "" {
		role = plan.RoleWorker
	}
	// Accept built-in and custom agents from the factory registry.
	if !o.knownAgent(role) {
		return nil, fmt.Errorf("unknown specialist %q — use: slmcode agents / Studio → Agents", role)
	}

	o.emit("init", fmt.Sprintf("specialist mode · %s", role), "")
	if instr := instructions.LoadProjectInstructions(o.cfg.Root); instr != "" {
		_ = o.store.Append(contextstore.DocScratch, "Project instructions", truncate(instr, 8000))
	}
	inventory := plan.ListWorkspaceFiles(o.cfg.Root, 48)
	discovered := plan.DiscoverRelevantFiles(o.cfg.Root, query, "")
	invMD := ""
	if len(inventory) > 0 {
		invMD = "## Workspace files (authoritative — do NOT invent paths)\n\n- " + strings.Join(inventory, "\n- ")
		if len(discovered) > 0 {
			invMD += "\n\n## Likely targets\n\n- " + strings.Join(discovered, "\n- ")
		}
		_ = o.store.Append(contextstore.DocContext, "Workspace inventory", invMD)
	}

	packSkills := o.skillPackFor(role, query)
	if invMD != "" {
		packSkills = packSkills + "\n\n" + invMD
	}
	tp, _ := o.packer.Build(role, query, contextstore.DefaultDocsForRole(role), discovered, packSkills)
	input := tp.Render() + "\n## Request\n\n" + query +
		"\n\nYou are running in **specialist mode** as `" + role + "`. Complete this request directly."

	o.emitAgent("specialist", role, "", "running single specialist", "", "")
	out, err := o.runRoleMultipassTracked(ctx, role, "", input)

	status := plan.StatusDone
	errStr := ""
	if err != nil {
		status = plan.StatusFailed
		errStr = err.Error()
	}
	board := &plan.Board{
		QueryID: runID, Query: query,
		Plan: plan.Plan{Summary: "Specialist: " + role, Steps: []string{query}},
		Tasks: []plan.Task{{
			ID: "S1", Title: "Specialist " + role, Description: query,
			Role: role, Status: status, Output: out, Error: errStr, Files: discovered,
		}},
	}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	o.persistBoard(board)
	if strings.TrimSpace(out) != "" {
		_ = o.store.Append(contextstore.DocScratch, "Specialist "+role, out)
	}
	res := &Result{
		ID: runID, Query: query, Board: *board,
		Success: err == nil, FailedTasks: board.FailedCount(),
		Duration: time.Since(start), Summary: firstSentence(out),
		Backend: config.BackendSLMCode, LatencyMs: o.snapshotLatency(),
		Usage: o.snapshotUsage(),
	}
	if res.Summary == "" {
		res.Summary = fmt.Sprintf("specialist %s finished", role)
	}
	if o.currentTurn != nil {
		_, _ = session.WriteTurnSummary(o.cfg.SlmDir(), o.currentTurn, *board, res.Summary)
	}
	o.emitLatencySummary(res)
	o.emit("done", res.Summary, "")
	return res, err
}

func (o *Orchestrator) runSLM(ctx context.Context, runID, query, skillPack string, start time.Time) (*Result, error) {
	var err error
	// 0 Auto-load AGENTS.md / CLAUDE.md / PROJECT instructions (Claude Code style)
	if instr := instructions.LoadProjectInstructions(o.cfg.Root); instr != "" {
		o.emit("init", "loaded project instructions (AGENTS.md/CLAUDE.md/PROJECT)", "")
		_ = o.store.Append(contextstore.DocScratch, "Project instructions", truncate(instr, 8000))
		o.shared.SetGlobal("project_instructions", instr)
		skillPack = skillPack + "\n\n## Project instructions\n\n" + truncate(instr, 6000)
	}

	// 0a Parse @file: / @folder: references from the query (Claude Code–style)
	if refs := extractFileRefs(query); len(refs) > 0 {
		o.shared.SetGlobal("query_file_refs", strings.Join(refs, ","))
		discoveredEarly := plan.ReconcileFiles(o.cfg.Root, refs, refs)
		_ = o.store.Append(contextstore.DocContext, "User file refs", "- "+strings.Join(discoveredEarly, "\n- "))
		o.emit("init", fmt.Sprintf("attached %d file ref(s)", len(discoveredEarly)), "")
	}

	// 0b Authoritative workspace inventory — stops SLMs inventing internal/... paths
	inventory := plan.ListWorkspaceFiles(o.cfg.Root, 48)
	discoveredEarly := plan.DiscoverRelevantFiles(o.cfg.Root, query, "")
	if refs := extractFileRefs(query); len(refs) > 0 {
		discoveredEarly = plan.ReconcileFiles(o.cfg.Root, append(discoveredEarly, refs...), inventory)
	}
	if len(inventory) > 0 {
		invMD := "## Workspace files (authoritative — do NOT invent paths)\n\n- " + strings.Join(inventory, "\n- ")
		if len(discoveredEarly) > 0 {
			invMD += "\n\n## Likely targets for this query\n\n- " + strings.Join(discoveredEarly, "\n- ")
		}
		_ = o.store.Append(contextstore.DocContext, "Workspace inventory", invMD)
		o.shared.SetGlobal("workspace_files", strings.Join(inventory, ","))
		skillPack = skillPack + "\n\n" + invMD + "\n"
		o.emit("init", fmt.Sprintf("indexed %d workspace file(s)", len(inventory)), "")
	}

	// 1 Context — shared memory for all specialists (pipeline-configurable agent)
	if err := o.runPipelineSlots(ctx, "context", "before", query, "", ""); err != nil {
		return nil, err
	}
	ctxAgent := o.phaseAgent("context", plan.RoleContext)
	if o.phaseEnabled("context") && !o.Pipeline().HasReplace("context") {
		o.emitAgent("context", ctxAgent, "", "updating working context", "", "")
		pack, _ := o.packer.Build(ctxAgent, query, contextstore.DefaultDocsForRole("context"), discoveredEarly, o.skillPackFor(ctxAgent, query))
		ctxOut, ctxErr := o.runRoleTracked(ctx, ctxAgent, "", pack.Render()+
			"\nRewrite CONTEXT.md for this query (markdown). ONLY reference files from the authoritative workspace list. Include: Active focus, Recent findings, Open questions.")
		if ctxErr != nil {
			o.emit("context", "warning: "+ctxErr.Error(), "")
		}
		if strings.TrimSpace(ctxOut) != "" {
			_ = o.store.Write(contextstore.DocContext, ensureHeading(ctxOut, "# Working Context"))
		} else if len(inventory) > 0 {
			_ = o.store.Write(contextstore.DocContext, "# Working Context\n\n## Active focus\n\n"+query+
				"\n\n## Recent findings\n\n(awaiting explorer)\n\n## Workspace inventory\n\n- "+
				strings.Join(inventory, "\n- ")+"\n")
		}
	} else if o.Pipeline().HasReplace("context") {
		if err := o.runPipelineSlots(ctx, "context", "replace", query, "", ""); err != nil {
			return nil, err
		}
	}
	if err := o.runPipelineSlots(ctx, "context", "after", query, "", ""); err != nil {
		return nil, err
	}
	// Re-assert authoritative paths after the context rewrite (SLMs often invent main.go).
	if len(inventory) > 0 {
		invMD := "- " + strings.Join(inventory, "\n- ")
		_ = o.store.Append(contextstore.DocContext, "Workspace inventory (authoritative)", invMD)
		if real := plan.FilterExisting(o.cfg.Root, discoveredEarly); len(real) > 0 {
			_ = o.store.Append(contextstore.DocContext, "Likely targets (existing files only)",
				"- "+strings.Join(real, "\n- "))
		}
	}

	// 1b Ensure PROJECT.md is populated (was empty scaffold during self-use)
	o.ensureProjectDoc(inventory)

	// 2 Explore — skip deep dive when CONTEXT + FS discovery already enough.
	// Higher think_passes forces deeper / parallel antigravity-style digs for SLMs.
	var exploreOut string
	var archOut string
	if err := o.runPipelineSlots(ctx, "explore", "before", query, "", ""); err != nil {
		return nil, err
	}
	exploreWhen := o.Pipeline().PhaseWhen("explore")
	deep, reason := o.shouldDeepExplore(query)
	if exploreWhen == pipeline.WhenNever || !o.phaseEnabled("explore") {
		deep = false
		reason = "pipeline: explore disabled"
	} else if exploreWhen == pipeline.WhenAlways {
		deep = true
		reason = "pipeline: explore=always"
	}
	if o.Pipeline().HasReplace("explore") {
		if err := o.runPipelineSlots(ctx, "explore", "replace", query, "", ""); err != nil {
			return nil, err
		}
		exploreOut = `{"summary":"pipeline slot replaced explore","relevant_files":[],"notes":"custom explore slot"}`
		o.shared.SetGlobal("exploration", exploreOut)
		o.shared.SetGlobal("explore_mode", "slot")
	} else if !deep {
		o.emit("explore", reason, "")
		discovered := plan.DiscoverRelevantFiles(o.cfg.Root, query, "")
		ctxDoc, _ := o.store.Read(contextstore.DocContext)
		memDoc, _ := o.store.Read(contextstore.DocMemory)
		exploreOut = fmt.Sprintf(`{"summary":"reused project memory","relevant_files":[%s],"notes":"skipped deep explore"}`,
			quoteJoin(discovered))
		_ = o.store.Write(contextstore.DocScratch, "# Exploration (cached)\n\n"+exploreOut+
			"\n\n## Context excerpt\n\n"+truncate(ctxDoc, 2000)+"\n\n## Memory excerpt\n\n"+truncate(memDoc, 1500))
		o.shared.SetGlobal("exploration", exploreOut)
		o.shared.SetGlobal("explore_mode", "cached")
	} else {
		expAgent := o.phaseAgent("explore", plan.RoleExplorer)
		o.emitAgent("explore", expAgent, "", "codebase deep-dive", "", "")
		expPack, _ := o.packer.Build(expAgent, query, contextstore.DefaultDocsForRole("explorer"), nil, o.skillPackFor(expAgent, query))
		explorePrompt := expPack.Render() + "\nExplore for this query. Return JSON."
		needDocs := (wantsDocsExplorer(query) || o.cfg.ThinkPasses >= 3) && o.phaseEnabled("docs") &&
			o.Pipeline().PhaseWhen("docs") != pipeline.WhenNever
		needArch := wantsArchitect(query) && o.cfg.ThinkPasses >= 2 && o.phaseEnabled("architect") &&
			o.Pipeline().PhaseWhen("architect") != pipeline.WhenNever
		if o.Pipeline().PhaseWhen("docs") == pipeline.WhenAlways {
			needDocs = true
		}
		if o.Pipeline().PhaseWhen("architect") == pipeline.WhenAlways {
			needArch = true
		}
		var docsOut string
		exploreOut, archOut, docsOut, err = o.speculateDigs(ctx, query, explorePrompt, inventory, needDocs, needArch)
		if err != nil {
			return nil, fmt.Errorf("explorer: %w", err)
		}
		if docsOut != "" {
			_ = o.store.Append(contextstore.DocScratch, "Docs exploration", docsOut)
			exploreOut += "\n\n" + docsOut
			o.shared.SetGlobal("docs_exploration", docsOut)
		}
		if archOut != "" {
			_ = o.store.Append(contextstore.DocScratch, "Architecture", archOut)
			o.shared.SetGlobal("architecture", archOut)
		}
		_ = o.store.Write(contextstore.DocScratch, "# Exploration\n\n"+exploreOut)
		o.shared.SetGlobal("exploration", exploreOut)
		o.shared.SetGlobal("explore_mode", "deep")
	}
	if err := o.runPipelineSlots(ctx, "explore", "after", query, exploreOut, ""); err != nil {
		return nil, err
	}

	// 2b Architect pass for larger / design-ish queries (skip if already ran in parallel)
	archWhen := o.Pipeline().PhaseWhen("architect")
	wantArch := o.phaseEnabled("architect") && archWhen != pipeline.WhenNever &&
		(archWhen == pipeline.WhenAlways || wantsArchitect(query))
	if wantArch && strings.TrimSpace(archOut) == "" {
		if err := o.runPipelineSlots(ctx, "architect", "before", query, exploreOut, ""); err != nil {
			return nil, err
		}
		if o.Pipeline().HasReplace("architect") {
			if err := o.runPipelineSlots(ctx, "architect", "replace", query, exploreOut, ""); err != nil {
				return nil, err
			}
		} else {
			archAgent := o.phaseAgent("architect", "architect")
			o.emitAgent("architect", archAgent, "", "minimal design pass", "", "")
			archPack, _ := o.packer.Build(archAgent, query, contextstore.LeanDocsForRole("architect"), nil, o.skillPackFor(archAgent, query))
			archOut, _ = o.runRoleTracked(ctx, archAgent, "", archPack.Render()+"\nExploration:\n"+truncate(exploreOut, 2500)+"\nReturn STRICT JSON design.")
			if strings.TrimSpace(archOut) != "" {
				_ = o.store.Append(contextstore.DocScratch, "Architecture", archOut)
				o.shared.SetGlobal("architecture", archOut)
			}
		}
		if err := o.runPipelineSlots(ctx, "architect", "after", query, exploreOut, ""); err != nil {
			return nil, err
		}
	}

	// 2c Scope interview (AskUserQuestion / pi-clarify style) → locked PRD.
	interview := o.runScopeInterview(ctx, query, exploreOut)
	clarify := interview.ToClarifyResult()
	prd := interview.PRD

	// 3 Plan (multipass when think_passes>1; single-shot otherwise)
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhasePlan)
	if err := o.runPipelineSlots(ctx, "plan", "before", query, exploreOut, ""); err != nil {
		return nil, err
	}
	planAgent := o.phaseAgent("plan", plan.RolePlanner)
	var planOut string
	if o.Pipeline().HasReplace("plan") {
		if err := o.runPipelineSlots(ctx, "plan", "replace", query, exploreOut, ""); err != nil {
			return nil, err
		}
		planOut = `{"summary":"pipeline slot replaced plan","goals":[],"steps":["Execute board tasks"]}`
	} else if o.phaseEnabled("plan") {
		o.emitAgent("plan", planAgent, "", "creating plan", "", "")
		planDocs := contextstore.LeanDocsForRole("planner")
		planPack, _ := o.packer.Build(planAgent, query, planDocs, nil, o.skillPackFor(planAgent, query))
		exploreCap := 2500
		if o.cfg.ThinkPasses >= 3 {
			exploreCap = 4000
		}
		planPrompt := planPack.Render() + "\nExploration:\n" + truncate(exploreOut, exploreCap)
		if archOut != "" {
			planPrompt += "\n\nArchitecture:\n" + truncate(archOut, 1500)
		}
		if prd.Summary != "" || len(prd.Acceptance) > 0 || len(clarify.Assumptions) > 0 ||
			clarify.Language != "" || clarify.Entrypoint != "" {
			planPrompt += "\n\n" + plan.FormatPRDMarkdown(prd, clarify.Assumptions)
			planPrompt += "\nTreat Locked PRD as hard requirements unless contradicted by the query.\n"
		}
		planPrompt += "\n\nIMPORTANT: Brand-new plan for THIS query only (query_id=" + runID + "). " +
			"Do NOT continue prior plans. STRICT JSON plan only."
		planOut, err = o.runRoleMultipassTracked(ctx, planAgent, "", planPrompt)
		if err != nil {
			if isCancelErr(err) {
				return o.checkpointInterrupt(&plan.Board{QueryID: runID, Query: query}, session.PhasePlan, err)
			}
			return nil, fmt.Errorf("planner: %w", err)
		}
		// Extra plan critique only when think_passes≥3.
		if o.cfg.ThinkPasses >= 3 && !multipass.LooksCompleteJSON(planOut) {
			revAgent := o.Pipeline().Execute.Reviewer
			if revAgent == "" {
				revAgent = plan.RoleReviewer
			}
			o.emitAgent("plan", revAgent, "", "plan critique pass", "", "")
			critiquePrompt := "Critique this SLM plan. Check missing files, oversized tasks, unclear acceptance, wrong order.\n" +
				"Query:\n" + truncate(query, 800) + "\n\nPlan:\n" + truncate(planOut, 3500) +
				"\n\nSTRICT JSON: {\"ok\":bool,\"issues\":[string],\"hints\":[string]}"
			critique, _ := o.runRoleTracked(ctx, revAgent, "", critiquePrompt)
			if strings.TrimSpace(critique) != "" {
				_ = o.store.Append(contextstore.DocScratch, "Plan critique", critique)
				o.emitFull("plan", stream.KindOutput, revAgent, "", "plan critique", "", truncate(critique, 800))
				if !strings.Contains(strings.ToLower(critique), `"ok": true`) &&
					!strings.Contains(strings.ToLower(critique), `"ok":true`) {
					o.emitAgent("plan", planAgent, "", "refining plan from critique", "", "")
					refine := planPrompt + "\n\n## Critique\n" + truncate(critique, 2000) +
						"\n\nRevise. Atomic for SLM. STRICT JSON plan."
					if refined, rerr := o.runRoleTracked(ctx, planAgent, "", refine); rerr == nil && strings.TrimSpace(refined) != "" {
						planOut = refined
					}
				}
			}
		}
	}
	if err := o.runPipelineSlots(ctx, "plan", "after", query, exploreOut, planOut); err != nil {
		return nil, err
	}
	pl, _ := plan.ParsePlanJSON(planOut)
	if strings.TrimSpace(pl.Summary) == "" || looksLikeJSONBlob(pl.Summary) {
		pl.Summary = firstSentence(stripJSONNoise(planOut))
		if strings.TrimSpace(pl.Summary) == "" || looksLikeJSONBlob(pl.Summary) {
			pl.Summary = "Implement request with locked PRD"
		}
	}
	pl = plan.MergeClarifyIntoPlan(pl, clarify)
	pl = plan.MergePRDIntoPlan(pl, prd)
	// Persist agent plan immediately so Studio PLAN.md / board update live mid-run.
	o.persistBoard(&plan.Board{QueryID: runID, Query: query, Plan: pl, Tasks: nil})
	o.emit("plan", "PLAN.md rewritten for this query", "")

	// 4 Split
	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseSplit)
	if err := o.runPipelineSlots(ctx, "split", "before", query, exploreOut, planOut); err != nil {
		return nil, err
	}
	splitAgent := o.phaseAgent("split", "splitter")
	var tasksOut string
	if o.Pipeline().HasReplace("split") {
		if err := o.runPipelineSlots(ctx, "split", "replace", query, exploreOut, planOut); err != nil {
			return nil, err
		}
		tasksOut = `{"tasks":[]}`
	} else if o.phaseEnabled("split") {
		o.emitAgent("split", splitAgent, "", "atomic task split", "", "")
		splitDocs := contextstore.LeanDocsForRole("splitter")
		splitPack, _ := o.packer.Build(splitAgent, query, splitDocs, nil, o.skillPackFor(splitAgent, query))
		splitPrompt := splitPack.Render() + "\nPlan:\n" + truncate(planOut, 3500)
		if prd.Summary != "" || len(prd.Acceptance) > 0 || clarify.Language != "" {
			splitPrompt += "\n\n" + plan.FormatPRDMarkdown(prd, clarify.Assumptions)
			splitPrompt += "\nEvery task must inherit Locked PRD acceptance/constraints.\n"
		}
		splitPrompt += "\n\nFresh task list for THIS query. STRICT JSON tasks."
		if o.cfg.ThinkPasses >= 2 {
			splitPrompt += "\nPrefer ≤5 tiny tasks with files + acceptance. Tester when code changes."
		}
		tasksOut, err = o.runRoleMultipassTracked(ctx, splitAgent, "", splitPrompt)
		if err != nil {
			if isCancelErr(err) {
				return o.checkpointInterrupt(&plan.Board{QueryID: runID, Query: query, Plan: pl}, session.PhaseSplit, err)
			}
			return nil, fmt.Errorf("splitter: %w", err)
		}
	}
	if err := o.runPipelineSlots(ctx, "split", "after", query, exploreOut, planOut); err != nil {
		return nil, err
	}
	tasks, err := plan.ParseTasksJSON(tasksOut)
	if err != nil || len(tasks) == 0 {
		tasks = fallbackTasks(pl)
	}
	discovered := plan.DiscoverRelevantFiles(o.cfg.Root, query, exploreOut)
	if len(discoveredEarly) > 0 {
		discovered = plan.ReconcileFiles(o.cfg.Root, append(discovered, discoveredEarly...), inventory)
	}
	tasks = plan.SanitizeTasksIn(tasks, exploreOut+"\n"+strings.Join(discovered, "\n"), query, o.cfg.Root)
	tasks = plan.EnsureTaskPRDs(tasks, prd, query)
	if len(discovered) > 0 {
		_ = o.store.Append(contextstore.DocContext, "Discovered files", "- "+strings.Join(discovered, "\n- "))
	}
	for i := range tasks {
		tasks[i].Files = plan.ReconcileFiles(o.cfg.Root, tasks[i].Files, discovered)
		// Keep persisted descriptions lean — scoped packs are injected at execute time.
		tasks[i].Description = loop.StripScopedPack(tasks[i].Description)
	}
	if len(tasks) > 8 {
		o.emit("split", fmt.Sprintf("capping tasks %d → 8 for SLM efficiency (preserving harness/tester)", len(tasks)), "")
		tasks = plan.CapTasksPreserveHarness(tasks, 8)
	}

	board := &plan.Board{QueryID: runID, Query: query, Plan: pl, Tasks: tasks}
	for i := range board.Tasks {
		t := board.Tasks[i]
		if t.Column == "" {
			t.Column = plan.ColReadyToDev
		}
		t.Normalize()
		board.Tasks[i] = t
	}
	// 4a Scope / PRD judge gate — enrich or rewrite weak tasks before execute.
	o.runScopeJudgeGate(ctx, query, board, prd)
	o.persistBoard(board)
	o.emit("split", fmt.Sprintf("TASKS.md + board: %d agent tasks", len(board.Tasks)), "")

	// 4b Coordinator reviews the board before execute
	o.coordinate(ctx, query, board, "pre-execute")

	// 4c Plan approval gate (Claude Code Plan Mode)
	ok, aerr := o.runPlanApprovalGate(ctx, query, board)
	if aerr != nil || !ok {
		if aerr == nil {
			aerr = fmt.Errorf("plan not approved")
		}
		return nil, aerr
	}

	// 5 Execute + review/correct (live board — human can edit/add mid-run)
	if err := o.runPipelineSlots(ctx, "execute", "before", query, exploreOut, planOut); err != nil {
		return nil, err
	}
	o.emit("execute", fmt.Sprintf("%d tasks · parallel=%d · think_passes=%d", len(board.Tasks), o.cfg.MaxParallel, o.cfg.ThinkPasses), "")
	// Clear focus during planning; runner re-enables per execute wave.
	if o.focus != nil {
		o.focus.Clear()
	}
	o.waveCounter = 0
	runner := loop.NewRunner(o.executor, o.shared)
	runner.Root = o.cfg.Root
	runner.SlmDir = o.cfg.SlmDir()
	runner.TurnID = runID
	runner.Store = o.boardStore
	runner.Focus = o.focus
	runner.MaxRetries = o.cfg.MaxRetries
	runner.MaxParallel = o.cfg.MaxParallel
	runner.Timeout = o.cfg.TaskTimeout
	runner.ReviewerRole = o.Pipeline().Execute.Reviewer
	runner.CorrectorRole = o.Pipeline().Execute.Corrector
	runner.PostWorkerSmoke = o.cfg.PostWorkerSmoke
	runner.WaveSnapshots = o.cfg.WaveSnapshots
	runner.RewindMgr = o.rewindMgr
	runner.FailureHandler = loop.NewEnhancedFailureHandler(o.cfg.Root)
	runner.Log = func(format string, args ...interface{}) {
		o.emitFull("execute", stream.KindDebug, "", "", fmt.Sprintf(format, args...), "", "")
	}
	runner.OnEvent = func(kind, agent, taskID, msg, scope, output string) {
		o.emitFull("execute", kind, agent, taskID, msg, scope, output)
	}
	runner.OnUsage = func(u llm.Usage, estimated bool, _, _ string) {
		o.recordUsage(u, estimated)
	}
	runner.AfterWave = func(ctx context.Context, board *plan.Board, wave []plan.Task) {
		o.evolveAfterWave(ctx, query, skillPack, board, wave)
		o.maybeCompactContext(ctx)
		o.coordinate(ctx, query, board, "after-wave")
	}
	// Ephemeral scoped packs — never persist fat context into TASKS.md / board descriptions.
	// Workers get lean docs + tight file excerpts for faster SLM inference.
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
	runner.BuildInput = func(t plan.Task) string {
		lean := loop.StripScopedPack(t.Description)
		docs := contextstore.LeanDocsForRole(t.Role)
		tp, _ := o.packer.Build(t.Role, query, docs, t.Files, o.skillPackFor(t.Role, query))
		tp.TaskID = t.ID
		tp.TaskTitle = t.Title
		t.Description = tp.Render() + "\n## Task instructions\n\n" + lean
		return o.formatWorkerPrompt(query, t)
	}
	snap := o.boardStore.Snapshot()
	board = &snap
	for i := range board.Tasks {
		board.Tasks[i].Description = loop.StripScopedPack(board.Tasks[i].Description)
	}
	o.persistBoard(board)

	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseExecute)
	execStart := time.Now()
	if err := runner.RunBoard(ctx, board); err != nil {
		return o.checkpointInterrupt(board, session.PhaseExecute, err)
	}
	o.recordLatency("execute", time.Since(execStart))
	o.emitFull("execute", stream.KindLatency, "worker", "",
		fmt.Sprintf("execute %dms", time.Since(execStart).Milliseconds()), "", "")
	snap = o.boardStore.Snapshot()
	board = &snap
	o.persistBoard(board)
	if err := o.runPipelineSlots(ctx, "execute", "after", query, exploreOut, planOut); err != nil {
		return nil, err
	}

	return o.finalizeAfterExecute(ctx, runID, query, skillPack, board, runner, start)
}

func (o *Orchestrator) injectPriorKnowledge(ctx context.Context, query string) {
	enabled, endpoint, model, apiKey, topK := o.cfg.RetrievalConfig()
	body, mode, err := retrieval.RetrieveForQuery(ctx, o.cfg.SlmDir(), query, retrieval.Config{
		Enabled: enabled, Endpoint: endpoint, Model: model, APIKey: apiKey, TopK: topK,
	})
	if err != nil {
		o.emit("init", "retrieval warning: "+err.Error(), "")
	}
	if strings.TrimSpace(body) != "" {
		_ = o.store.Append(contextstore.DocContext,
			"Retrieved prior knowledge (ranked — write a NEW plan for this query)",
			truncate(body, 3000))
		o.shared.SetGlobal("prior_summaries", truncate(body, 2000))
		o.emit("init", fmt.Sprintf("enriched context via %s retrieval", mode), "")
		return
	}
	// Fallback: recency index
	if prior := session.RecentSummaries(o.cfg.SlmDir(), 3); prior != "" {
		_ = o.store.Append(contextstore.DocContext, "Prior query summaries (knowledge only — write a NEW plan for this query)",
			truncate(prior, 2500))
		o.shared.SetGlobal("prior_summaries", truncate(prior, 2000))
		o.emit("init", "enriched context from prior query summaries (recency)", "")
	}
}

func (o *Orchestrator) emitLatencySummary(res *Result) {
	if res == nil {
		return
	}
	if len(res.LatencyMs) > 0 {
		parts := make([]string, 0, len(res.LatencyMs))
		for k, v := range res.LatencyMs {
			parts = append(parts, fmt.Sprintf("%s=%dms", k, v))
		}
		for i := 0; i < len(parts); i++ {
			for j := i + 1; j < len(parts); j++ {
				if parts[j] < parts[i] {
					parts[i], parts[j] = parts[j], parts[i]
				}
			}
		}
		msg := fmt.Sprintf("total=%s · %s", res.Duration.Round(time.Millisecond), strings.Join(parts, " "))
		o.emitFull("latency", stream.KindLatency, "", "", msg, "", "")
	}
	if res.Usage == nil {
		res.Usage = o.snapshotUsage()
	}
	if head := usageHead(res.Usage); head != "" {
		o.emitFull("usage", stream.KindUsage, "", "", head, "", "")
	}
}

func (o *Orchestrator) runClaudeCode(ctx context.Context, runID, query, skillPack string, start time.Time) (*Result, error) {
	if !o.claude.Available() {
		return nil, fmt.Errorf("claude-code backend selected but %q not found on PATH", o.cfg.ClaudeCodeBin)
	}
	o.emit("claude-code", "delegating scoped run to Claude Code CLI", "")
	pack, _ := o.packer.Build("worker", query,
		[]string{contextstore.DocProject, contextstore.DocContext, contextstore.DocQuery, contextstore.DocMemory},
		nil, skillPack)
	prompt := pack.Render() + "\n\n## Request\n\n" + query + "\n\nWork only on what is needed. Prefer small atomic edits."
	out, err := o.claude.Run(ctx, prompt)
	board := &plan.Board{
		QueryID: runID, Query: query,
		Plan: plan.Plan{Summary: "Claude Code backend", Steps: []string{query}},
		Tasks: []plan.Task{{
			ID: "T1", Title: "Claude Code execution", Description: query,
			Role: plan.RoleWorker, Status: plan.StatusDone, Output: out,
		}},
	}
	if err != nil {
		board.Tasks[0].Status = plan.StatusFailed
		board.Tasks[0].Error = err.Error()
	}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	o.persistBoard(board)
	_ = o.store.Append(contextstore.DocScratch, "Claude Code", out)
	res := &Result{
		ID: runID, Query: query, Board: *board,
		Success: err == nil, FailedTasks: board.FailedCount(),
		Duration: time.Since(start), Summary: firstSentence(out),
		Backend: config.BackendClaudeCode,
	}
	if o.currentTurn != nil {
		_, _ = session.WriteTurnSummary(o.cfg.SlmDir(), o.currentTurn, *board, res.Summary)
	}
	o.emit("done", res.Summary, "")
	return res, err
}

func (o *Orchestrator) runRole(ctx context.Context, role, input string) (string, error) {
	return o.runRoleTracked(ctx, role, "", input)
}

// roleTimeout gives planning/coord roles a tighter budget than full task timeout
// so slow oMLX multi-turn runs don't stall for 12 minutes on a stuck planner call.
// Floors avoid false failures on cold SLM loads; caps stop runaway generations.
func (o *Orchestrator) roleTimeout(role string) time.Duration {
	full := o.cfg.TaskTimeout
	if full <= 0 {
		full = config.DefaultTaskTimeout
	}
	switch role {
	case plan.RoleWorker, "deep", plan.RoleCorrector, plan.RoleExplorer, "docs",
		plan.RoleTester, plan.RolePlaceholder:
		return full
	case plan.RolePlanner, "splitter":
		// Local 30B SLMs often need several minutes for structured JSON plans.
		d := full / 2
		if d < 2*time.Minute {
			d = 2 * time.Minute
		}
		if d > 8*time.Minute {
			d = 8 * time.Minute
		}
		return d
	case plan.RoleReviewer, "coordinator", "architect", plan.RoleContext, "memory":
		d := full / 4
		if d < 60*time.Second {
			d = 60 * time.Second
		}
		if d > 3*time.Minute {
			d = 3 * time.Minute
		}
		return d
	default:
		d := full / 2
		if d < 90*time.Second {
			d = 90 * time.Second
		}
		if d > 5*time.Minute {
			d = 5 * time.Minute
		}
		return d
	}
}

func (o *Orchestrator) runRoleTracked(ctx context.Context, role, taskID, input string) (string, error) {
	o.emitFull(role, stream.KindAgentStart, role, taskID, "started", scopeFromInput(input), "")
	start := time.Now()
	timeout := o.roleTimeout(role)
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results, err := o.executor.ExecuteSubAgents(rctx, []ggagent.SubAgentRequest{{
		AgentID: role, Input: input, Timeout: timeout, ShareState: true,
	}}, o.shared)
	elapsed := time.Since(start)
	o.recordLatency(role, elapsed)
	if len(results) == 0 {
		o.emitFull(role, stream.KindAgentEnd, role, taskID, "no result", "", "")
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("no result from %s", role)
	}
	out := ""
	if results[0].Output != nil {
		out = fmt.Sprintf("%v", results[0].Output)
	}
	o.recordResultUsage(results[0], input, out)
	if results[0].Error != nil && out == "" {
		o.emitFull(role, stream.KindAgentEnd, role, taskID, "error: "+results[0].Error.Error(), "", "")
		return "", results[0].Error
	}
	o.emitFull(role, stream.KindAgentEnd, role, taskID,
		fmt.Sprintf("finished (%s)", elapsed.Round(time.Millisecond)),
		scopeFromInput(input), truncate(out, 1500))
	o.emitFull(role, stream.KindLatency, role, taskID,
		fmt.Sprintf("%s %dms", role, elapsed.Milliseconds()), "", "")
	return out, nil
}

func (o *Orchestrator) recordLatency(key string, d time.Duration) {
	if key == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.latencyMs == nil {
		o.latencyMs = map[string]int64{}
	}
	o.latencyMs[key] += d.Milliseconds()
}

func (o *Orchestrator) snapshotLatency() map[string]int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.latencyMs) == 0 {
		return nil
	}
	out := make(map[string]int64, len(o.latencyMs))
	for k, v := range o.latencyMs {
		out[k] = v
	}
	return out
}

func (o *Orchestrator) runRoleMultipass(ctx context.Context, role, input string) (string, error) {
	return o.runRoleMultipassTracked(ctx, role, "", input)
}

func (o *Orchestrator) runRoleMultipassTracked(ctx context.Context, role, taskID, input string) (string, error) {
	if o.cfg.ThinkPasses <= 1 {
		return o.runRoleTracked(ctx, role, taskID, input)
	}
	o.emitFull(role, stream.KindAgentStart, role, taskID, fmt.Sprintf("multipass×%d", o.cfg.ThinkPasses), "", "")
	a, err := o.factory.Create(role)
	if err != nil {
		return o.runRoleTracked(ctx, role, taskID, input)
	}
	out, err := o.think.Execute(ctx, a, input)
	if err != nil {
		o.emitFull(role, stream.KindAgentEnd, role, taskID, "error: "+err.Error(), "", "")
		return "", err
	}
	o.emitFull(role, stream.KindAgentEnd, role, taskID, "multipass finished", "", truncate(out, 1500))
	return out, nil
}

// shouldDeepExplore returns false when PROJECT/CONTEXT/MEMORY already carry enough
// shared knowledge — avoids re-scanning the repo on every run.
func (o *Orchestrator) shouldDeepExplore(query string) (doDeep bool, reason string) {
	if os.Getenv("SLMCODE_FORCE_EXPLORE") == "1" {
		return true, ""
	}
	// Deeper SLM planning: think_passes>=3 always digs; >=2 digs on non-trivial queries.
	if o.cfg != nil && o.cfg.ThinkPasses >= 3 {
		return true, ""
	}
	ctxDoc, _ := o.store.Read(contextstore.DocContext)
	memDoc, _ := o.store.Read(contextstore.DocMemory)
	projDoc, _ := o.store.Read(contextstore.DocProject)
	discovered := plan.DiscoverRelevantFiles(o.cfg.Root, query, ctxDoc+"\n"+memDoc)
	rich := len(ctxDoc) > 500 && (strings.Contains(ctxDoc, "Discovered files") || strings.Contains(ctxDoc, "Wave"))
	hasFiles := len(discovered) > 0
	hasMemory := len(memDoc) > 200 || len(projDoc) > 200
	if o.cfg != nil && o.cfg.ThinkPasses >= 2 && (wantsForceExplore(query) || len(strings.Fields(query)) > 8 || !rich) {
		return true, ""
	}
	if rich && hasFiles && hasMemory && !wantsForceExplore(query) {
		return false, fmt.Sprintf("reusing CONTEXT/MEMORY + %d known file(s) — skip deep explore", len(discovered))
	}
	return true, ""
}

func (o *Orchestrator) coordinate(ctx context.Context, query string, board *plan.Board, when string) {
	if board == nil {
		return
	}
	// Skip coordinator LLM on lean SLM runs (think_passes=1) — saves a slow
	// round-trip that rarely changes a freshly-split board.
	if o.cfg != nil && o.cfg.ThinkPasses <= 1 && (when == "pre-execute" || when == "after-wave") {
		o.emitFull("coord", stream.KindCoord, "coordinator", "", "coordinator @"+when+" (skipped — think_passes=1)", "", "")
		return
	}
	o.emitFull("coord", stream.KindCoord, "coordinator", "", "coordinator @"+when, "", "")
	_, tasksMD := board.ToMarkdown()
	prompt := agents.PromptCoordinator + "\n\nQuery:\n" + query +
		"\n\nWhen: " + when + "\n\nBoard:\n" + truncate(tasksMD, 5000) +
		"\n\nReturn STRICT JSON actions."
	out, err := o.runRoleTracked(ctx, "coordinator", "", prompt)
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	_ = o.store.Append(contextstore.DocScratch, "Coordinator "+when, out)
	o.shared.SetGlobal("coordinator_"+when, out)
	o.applyCoordinatorActions(board, out)
	o.persistBoard(board)
	o.emitFull("coord", stream.KindOutput, "coordinator", "", "coordinator advice", "", truncate(out, 1000))
}

func (o *Orchestrator) applyCoordinatorActions(board *plan.Board, raw string) {
	type action struct {
		Type   string `json:"type"`
		TaskID string `json:"task_id"`
		Role   string `json:"role"`
		Text   string `json:"text"`
	}
	var wrap struct {
		Actions    []action `json:"actions"`
		FocusFiles []string `json:"focus_files"`
		Summary    string   `json:"summary"`
	}
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
	}
	if err := jsonUnmarshal(raw, &wrap); err != nil {
		return
	}
	if wrap.Summary != "" {
		o.emit("coord", wrap.Summary, "")
	}
	for _, a := range wrap.Actions {
		switch strings.ToLower(a.Type) {
		case "promote":
			if t, ok := board.Get(a.TaskID); ok {
				t.MoveTo(plan.ColReadyToDev)
				if a.Text != "" {
					t.Notes = strings.TrimSpace(t.Notes + "\n" + a.Text)
				}
				board.UpdateTask(t)
			}
		case "reassign":
			if t, ok := board.Get(a.TaskID); ok && a.Role != "" {
				t.Delegate(a.Role)
				board.UpdateTask(t)
			}
		case "note":
			if t, ok := board.Get(a.TaskID); ok {
				t.Notes = strings.TrimSpace(t.Notes + "\n" + a.Text)
				board.UpdateTask(t)
			}
		case "add_task":
			if strings.TrimSpace(a.Text) == "" {
				continue
			}
			title := firstSentence(a.Text)
			if taskTitleExists(board, title) {
				o.emit("coord", "skip duplicate task: "+title, "")
				continue
			}
			if openTaskCoversFiles(board, wrap.FocusFiles) {
				o.emit("coord", "skip overlapping task for focus files: "+title, "")
				continue
			}
			if len(board.Tasks) >= 12 {
				o.emit("coord", "task cap reached — skip add_task", "")
				continue
			}
			id := board.NextID()
			nt := plan.Task{
				ID: id, Title: title, Description: a.Text,
				Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: wrap.FocusFiles,
			}
			if a.Role != "" {
				nt.Role = a.Role
			}
			nt.Normalize()
			board.Tasks = append(board.Tasks, nt)
		}
	}
	if len(wrap.FocusFiles) > 0 {
		o.shared.SetGlobal("focus_files", strings.Join(wrap.FocusFiles, ","))
	}
}

// evolveAfterWave updates CONTEXT + MEMORY from wave results and refreshes
// pending task packs so later specialists see evolving project knowledge.
func (o *Orchestrator) evolveAfterWave(ctx context.Context, query, skillPack string, board *plan.Board, wave []plan.Task) {
	o.emit("learn", fmt.Sprintf("evolving context after %d task(s)", len(wave)), "")
	_ = o.store.Append(contextstore.DocContext, "Wave", learning.ContextDelta(wave))

	var lessons []learning.Lesson
	for _, t := range wave {
		lessons = append(lessons, learning.Extract(t)...)
	}
	md := learning.RenderMarkdown(lessons)

	// Optional SLM distillation of the wave (cheap when think_passes>=2)
	if o.cfg.ThinkPasses >= 2 && len(wave) > 0 {
		var brief strings.Builder
		brief.WriteString("Wave results for learning:\n")
		for _, t := range wave {
			t.Normalize()
			brief.WriteString(fmt.Sprintf("- %s [%s] %s | out=%s | err=%s\n",
				t.ID, t.Column, t.Title, truncate(t.Output, 240), truncate(t.Error, 120)))
		}
		if distilled, err := o.runRole(ctx, "memory", agents.PromptLearner+"\n\n"+brief.String()); err == nil {
			if bullets := learning.JSONLessonsToMarkdown(distilled); bullets != "" {
				md = strings.TrimSpace(md + "\n" + bullets)
			}
		}
	}

	if md != "" {
		_ = o.store.Append(contextstore.DocMemory, "Wave lessons", md)
		o.shared.SetGlobal("latest_lessons", md)
	}

	// Keep descriptions lean; BuildInput injects fresh packs at execute time.
	for i := range board.Tasks {
		t := board.Tasks[i]
		t.Normalize()
		t.Description = loop.StripScopedPack(t.Description)
		board.Tasks[i] = t
	}
}

// maybeCompactContext summarizes CONTEXT.md when it exceeds the pack budget.
func (o *Orchestrator) maybeCompactContext(ctx context.Context) {
	_ = ctx
	if o == nil || o.cfg == nil || !o.cfg.ContextCompact || o.store == nil {
		return
	}
	body, err := o.store.Read(contextstore.DocContext)
	if err != nil || body == "" {
		return
	}
	soft := o.cfg.MaxContextKB
	if soft <= 0 {
		soft = 32
	}
	if !compact.NeedsCompact(body, soft, soft*2) {
		return
	}
	res := compact.HeuristicSummarize(body, soft*1024)
	if !res.Compacted {
		return
	}
	_ = o.store.Write(contextstore.DocContext, res.Summary)
	o.emitFull("learn", stream.KindOutput, "compact", "",
		fmt.Sprintf("CONTEXT compacted %d→%d bytes", res.BeforeBytes, res.AfterBytes),
		"", truncate(res.Summary, 400))
}

// CompactContextNow forces a CONTEXT.md compaction (TUI /compact context).
func (o *Orchestrator) CompactContextNow() (compact.Result, error) {
	body, err := o.store.Read(contextstore.DocContext)
	if err != nil {
		return compact.Result{}, err
	}
	max := o.cfg.MaxContextKB * 1024
	if max <= 0 {
		max = 24 * 1024
	}
	res := compact.HeuristicSummarize(body, max)
	if res.Compacted {
		_ = o.store.Write(contextstore.DocContext, res.Summary)
	}
	return res, nil
}

func (o *Orchestrator) persistBoard(board *plan.Board) {
	if board == nil {
		return
	}
	if o.currentTurn != nil {
		if board.QueryID == "" {
			board.QueryID = o.currentTurn.ID
		}
		if board.Query == "" {
			board.Query = o.currentTurn.Query
		}
		_ = session.SaveTurnBoard(o.cfg.SlmDir(), o.currentTurn, *board)
	}
	if o.boardStore != nil {
		_ = o.boardStore.Replace(*board)
		return
	}
	planMD, tasksMD := board.ToMarkdown()
	_ = o.store.Write(contextstore.DocPlan, planMD)
	_ = o.store.Write(contextstore.DocTasks, tasksMD)
}

func (o *Orchestrator) emit(phase, msg, taskID string) {
	o.emitFull(phase, stream.KindPhase, "", taskID, msg, "", "")
}

func (o *Orchestrator) emitAgent(phase, agent, taskID, msg, scope, output string) {
	o.emitFull(phase, stream.KindAgentStart, agent, taskID, msg, scope, output)
}

func (o *Orchestrator) emitFull(phase, kind, agent, taskID, msg, scope, output string) {
	if kind == "" {
		kind = stream.KindPhase
	}
	if o.cfg.Verbose {
		prefix := phase
		if agent != "" {
			prefix = phase + ":@" + agent
		}
		fmt.Printf("[%s] %s\n", prefix, msg)
	}
	if o.onEvent != nil {
		o.onEvent(Event{
			Phase: phase, Kind: kind, Message: msg, TaskID: taskID,
			Agent: agent, Scope: scope, Output: stream.Truncate(output, 2000),
			Time: time.Now(),
		})
	}
}

func wantsDocsExplorer(query string) bool {
	q := strings.ToLower(query)
	for _, k := range []string{"readme", "docs", "documentation", "api doc", "godoc", "adr", "changelog"} {
		if strings.Contains(q, k) {
			return true
		}
	}
	return false
}

func wantsArchitect(query string) bool {
	q := strings.ToLower(query)
	for _, k := range []string{"architect", "design", "refactor", "migrate", "multi-module", "system", "api design"} {
		if strings.Contains(q, k) {
			return true
		}
	}
	return len(strings.Fields(q)) > 24
}

func wantsForceExplore(query string) bool {
	q := strings.ToLower(query)
	for _, k := range []string{"explore", "find where", "search the codebase", "map the", "investigate"} {
		if strings.Contains(q, k) {
			return true
		}
	}
	return false
}

func quoteJoin(files []string) string {
	if len(files) == 0 {
		return ""
	}
	parts := make([]string, len(files))
	for i, f := range files {
		parts[i] = fmt.Sprintf("%q", f)
	}
	return strings.Join(parts, ",")
}

func scopeFromInput(input string) string {
	var files []string
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".go") || strings.Contains(line, "/") && strings.Contains(line, ".") {
			if len(line) < 80 {
				files = append(files, line)
			}
		}
		if len(files) >= 4 {
			break
		}
	}
	return strings.Join(files, ", ")
}

func jsonUnmarshal(raw string, v interface{}) error {
	return json.Unmarshal([]byte(raw), v)
}

func InitWorkspace(root string, cfg *config.Config) error {
	if cfg == nil {
		cfg = config.Default(root)
	}
	cfg.Root = root
	store := contextstore.New(cfg.SlmDir())
	if err := store.Init(filepath.Base(root)); err != nil {
		return err
	}
	_ = os.MkdirAll(cfg.SkillsDir(), 0o755)
	_ = os.MkdirAll(filepath.Join(cfg.SlmDir(), "errors"), 0o755)
	_ = os.MkdirAll(filepath.Join(cfg.SlmDir(), "archives"), 0o755)
	_ = os.MkdirAll(filepath.Join(cfg.SlmDir(), "queries"), 0o755)
	_ = os.MkdirAll(filepath.Join(cfg.SlmDir(), "summaries"), 0o755)
	// Materialize bundled skills only — CONTEXT/PLAN/TASKS stay empty until agents write them.
	_ = skills.MaterializeBundled(filepath.Join(cfg.SlmDir(), "skills", "_bundled"))
	_ = pipeline.EnsureFile(cfg.SlmDir())
	// Seed PROJECT.md from README / go.mod / layout so agents always have context.
	seeded := contextstore.SeedProjectMarkdown(root, filepath.Base(root))
	if cur, err := store.Read(contextstore.DocProject); err != nil || contextstore.ProjectNeedsSeed(cur) {
		_ = store.Write(contextstore.DocProject, seeded)
	} else {
		_ = store.Write(contextstore.DocProject, contextstore.MergeProjectSections(cur, seeded))
	}
	// Empty board.json so Studio/CLI have a writable board (no seeded tasks).
	boardPath := filepath.Join(cfg.SlmDir(), "board.json")
	if _, err := os.Stat(boardPath); os.IsNotExist(err) {
		empty := plan.Board{}
		if data, err := json.MarshalIndent(empty, "", "  "); err == nil {
			_ = os.WriteFile(boardPath, data, 0o644)
		}
	}
	return cfg.Save()
}

func (o *Orchestrator) ensureProjectDoc(inventory []string) {
	cur, _ := o.store.Read(contextstore.DocProject)
	seeded := contextstore.SeedProjectMarkdown(o.cfg.Root, filepath.Base(o.cfg.Root))
	if contextstore.ProjectNeedsSeed(cur) {
		if len(inventory) > 0 {
			// Enrich key paths from live inventory when seed table is thin.
			seeded = contextstore.MergeProjectSections(seeded, seeded)
		}
		_ = o.store.Write(contextstore.DocProject, seeded)
		o.emit("context", "seeded PROJECT.md from repository metadata", "")
		return
	}
	merged := contextstore.MergeProjectSections(cur, seeded)
	if merged != cur {
		_ = o.store.Write(contextstore.DocProject, merged)
		o.emit("context", "refreshed empty PROJECT.md sections", "")
	}
}

func globalSkillRoots() []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots,
			filepath.Join(home, ".slmcode", "skills"),
			filepath.Join(home, ".config", "slmcode", "skills"),
		)
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		roots = append(roots, filepath.Join(xdg, "slmcode", "skills"))
	}
	return roots
}

func taskTitleExists(board *plan.Board, title string) bool {
	norm := strings.ToLower(strings.TrimSpace(title))
	if norm == "" {
		return false
	}
	for _, t := range board.Tasks {
		other := strings.ToLower(strings.TrimSpace(t.Title))
		if other == norm {
			return true
		}
		if strings.Contains(other, norm) || strings.Contains(norm, other) {
			// Near-duplicate titles from coordinator spam.
			if abs(len(other)-len(norm)) <= 12 {
				return true
			}
		}
	}
	return false
}

// openTaskCoversFiles reports whether a non-done board task already targets the
// same focus files (coordinator often re-adds the splitter's work).
func openTaskCoversFiles(board *plan.Board, files []string) bool {
	if board == nil || len(files) == 0 {
		return false
	}
	want := map[string]bool{}
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f != "" {
			want[strings.ToLower(f)] = true
		}
	}
	if len(want) == 0 {
		return false
	}
	for _, t := range board.Tasks {
		t.Normalize()
		switch t.Column {
		case plan.ColDone, plan.ColBlocked:
			continue
		}
		for _, f := range t.Files {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if want[strings.ToLower(f)] {
				return true
			}
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func extractFileRefs(query string) []string {
	var out []string
	seen := map[string]bool{}
	fields := strings.Fields(query)
	for _, f := range fields {
		f = strings.Trim(f, "`,\"'")
		var path string
		switch {
		case strings.HasPrefix(f, "@file:"):
			path = strings.TrimPrefix(f, "@file:")
		case strings.HasPrefix(f, "@folder:"):
			path = strings.TrimPrefix(f, "@folder:")
		case strings.HasPrefix(f, "@") && strings.Contains(f, "/") && !strings.HasPrefix(f, "@skill:"):
			path = strings.TrimPrefix(f, "@")
		default:
			continue
		}
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func (o *Orchestrator) formatWorkerPrompt(query string, t plan.Task) string {
	base := formatWorkerPromptFor(t)
	if o == nil || o.cfg == nil {
		return base
	}
	var extras strings.Builder
	if o.cfg.ThinkingBudget && t.Role != plan.RoleTester {
		extras.WriteString(quality.ThinkingBudgetNudge(true))
	}
	if o.cfg.FinalizeWarn {
		maxIter := 16
		if spec := agents.FindSpec(t.Role); spec != nil && spec.MaxIter > 0 {
			maxIter = spec.MaxIter
		}
		extras.WriteString(quality.FinalizeWarnMessage(maxIter))
	}
	if o.cfg.ToolGuidance || o.cfg.KnowledgeInject {
		opt := augment.Options{}
		prof := o.resolvedProfile()
		if !o.cfg.ToolGuidance {
			opt.SkillBudget = -1
		} else if prof.SkillTokenBudget > 0 {
			opt.SkillBudget = prof.SkillTokenBudget
		}
		if !o.cfg.KnowledgeInject {
			opt.KnowledgeBudget = -1
		} else if prof.KnowledgeTokenBudget > 0 {
			opt.KnowledgeBudget = prof.KnowledgeTokenBudget
		}
		prompt := query + "\n" + t.Title + "\n" + t.Description + "\n" + t.Acceptance
		// Tail injection preserves any cached system/prefix tokens (little-coder #73).
		extras.WriteString(augment.InjectForPrompt(prompt, opt))
	}
	return base + extras.String()
}

func (o *Orchestrator) resolvedProfile() config.ModelProfile {
	if o == nil || o.cfg == nil {
		return config.ResolveModelProfile(nil, "")
	}
	return config.ResolveModelProfile(o.cfg.ModelProfiles, o.cfg.Model)
}

func formatWorkerPromptFor(t plan.Task) string {
	// Keep ephemeral scoped packs (injected by BuildInput); only strip when absent.
	desc := t.Description
	if !strings.Contains(desc, "# Scoped context") {
		desc = loop.StripScopedPack(desc)
	}
	var b strings.Builder
	b.WriteString("Atomic task — complete only this:\n\n")
	b.WriteString(fmt.Sprintf("ID: %s\nTitle: %s\nColumn: %s\nRole: %s\n\n", t.ID, t.Title, t.Column, t.Role))
	b.WriteString(desc)
	b.WriteString("\n")
	if len(t.Files) > 0 {
		b.WriteString("\n## Focus files (HARD SCOPE)\nOnly edit these paths or files in the same package directory:\n- ")
		b.WriteString(strings.Join(t.Files, "\n- "))
		b.WriteString("\nDo NOT create main.go / index.js / other entrypoints unless listed above.\n")
	}
	if t.Acceptance != "" {
		b.WriteString("\nAcceptance criteria:\n")
		b.WriteString(t.Acceptance)
		b.WriteString("\n")
	}
	if t.Notes != "" {
		b.WriteString("\nHuman notes:\n")
		b.WriteString(t.Notes)
		b.WriteString("\n")
	}
	if t.Role == plan.RoleTester {
		b.WriteString(`
## Required finish (tester)
1. Use ws_shell to install deps if needed, then run real tests or smoke commands.
2. Reading files alone is NOT verification — commands must exit 0.
3. End with STRICT JSON only:
{"passed":true|false,"commands":["exact shell…"],"summary":"...","failures":["T1: path — reason"]}
Never end on a tool call. Never soft-pass broken code.
`)
		return b.String()
	}
	b.WriteString(`
## Required finish
1. ws_read focus files first, then ws_edit / ws_patch (prefer over rewrites).
2. ws_write is for NEW files only — refused on existing paths. Never cat> overwrite via shell.
3. End with STRICT JSON only:
{"status":"done","summary":"...","files_changed":["real/path.go"],"notes":"..."}
Never claim done without tool edits. Never end on a tool call.
`)
	return b.String()
}

func ensureHeading(body, heading string) string {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "#") {
		return body + "\n"
	}
	return heading + "\n\n" + body + "\n"
}

func fallbackTasks(pl plan.Plan) []plan.Task {
	if len(pl.Steps) == 0 {
		return []plan.Task{{
			ID: "T1", Title: "Implement request", Description: pl.Summary,
			Role: plan.RoleWorker, Status: plan.StatusPending,
			Acceptance: "Query goals met",
		}}
	}
	var tasks []plan.Task
	for i, step := range pl.Steps {
		id := fmt.Sprintf("T%d", i+1)
		var deps []string
		if i > 0 {
			deps = []string{fmt.Sprintf("T%d", i)}
		}
		tasks = append(tasks, plan.Task{
			ID: id, Title: step, Description: step, Role: plan.RoleWorker,
			Status: plan.StatusPending, DependsOn: deps, Acceptance: "Step completed",
		})
	}
	return tasks
}

func summarize(board *plan.Board, pl plan.Plan) string {
	done, total := 0, len(board.Tasks)
	for _, t := range board.Tasks {
		t.Normalize()
		if t.Column == plan.ColDone {
			done++
		}
	}
	return fmt.Sprintf("%s — %d/%d tasks done, %d failed",
		firstSentence(pl.Summary), done, total, board.FailedCount())
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Run complete"
	}
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		return s[:i]
	}
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

func looksLikeJSONBlob(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") || strings.HasPrefix(s, "```")
}

func stripJSONNoise(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || looksLikeJSONBlob(line) || strings.HasPrefix(line, "```") {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(line)
		if b.Len() > 200 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}
