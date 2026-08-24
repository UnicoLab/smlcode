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
	"github.com/UnicoLab/slmcode/pkg/blocks"
	"github.com/UnicoLab/slmcode/pkg/compact"
	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/hooks"
	"github.com/UnicoLab/slmcode/pkg/instructions"
	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/learning"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/mcp"
	"github.com/UnicoLab/slmcode/pkg/models"
	"github.com/UnicoLab/slmcode/pkg/multipass"
	"github.com/UnicoLab/slmcode/pkg/pipeline"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/refine"
	"github.com/UnicoLab/slmcode/pkg/repomap"
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
	cfg        *config.Config
	store      *contextstore.Store
	boardStore *plan.LiveStore
	// packer carries every context-engineering knob, per-role budgets
	// (context_role_budget) included — see context_wiring.go.
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
	onEscalate    EscalateHandler

	hooksRunner *hooks.Runner
	mcpMgr      *mcp.Manager
	rewindMgr   *rewind.Manager
	waveCounter int
	pipe        *pipeline.Config // config-driven phases / slots / loop agents

	// workspace / repoMap / tracker come from the tool layer so the
	// orchestrator can reset the per-task loop guard and seed focus discovery.
	workspace *workspace.Workspace
	tracker   *workspace.CallTracker
	repoMap   *repomap.Map

	// evolve is the self-improvement engine (memory + repair rules + bandit +
	// regression checks). Nil-safe: every call site tolerates a nil engine.
	evolve *evolve.Engine
	// decisions accumulates the bandit choices this run made, for the
	// end-of-run RunReport.
	decisions []evolve.DecisionRecord
	// gates accumulates quality-gate outcomes for the RunReport.
	gates []evolve.GateResult

	// projectInstructions is AGENTS.md / CLAUDE.md / PROJECT instructions,
	// consumed by skillPackFor so they reach the STABLE PREFIX of every pack
	// instead of dead-ending in SCRATCH.md.
	projectInstructions string

	// dynamicSkills maps specialist roles → composer-selected skills for the
	// in-flight dynamic-pipeline run. Guarded by mu.
	dynamicSkills map[string][]string
	// dynamicBrief is the compact collaboration contract emitted by the
	// composer and injected into later role-scoped packs. Guarded by mu.
	dynamicBrief       string
	dynamicComposition *composer.Composition

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc

	// evMu guards onEvent alone. Studio swaps the sink (setOrch →
	// wireOrchestratorEvents) while a run is emitting, and the emit path must
	// not take mu — several callers already hold it.
	evMu sync.RWMutex

	// currentTurn scopes plan/tasks/summary to one user query (rewritten each Run).
	currentTurn *session.Turn

	// latencyMs accumulates phase/role durations for the in-flight run.
	latencyMs map[string]int64
	// usage accumulates token counts for the in-flight run.
	usage TokenUsage
	// refineRound counts auto-refine passes in the current run.
	refineRound int
	// llmCalls counts LLM round-trips this run (evolve RunReport).
	llmCalls int
	// eventSubscribed records that a UI attached an event handler.
	eventSubscribed bool
	// runStart is when the in-flight run began (evolve RunReport).
	runStart time.Time
	// activeRunner is the inner loop for the in-flight run, kept so completeRun
	// can drain its accumulated failure events and decision records.
	activeRunner *loop.Runner

	// liveFeedback is a free-form steering message from the user, injected into
	// the next agent prompts mid-run (see runRoleTracked).
	liveFeedback   string
	liveFeedbackAt string // RFC3339 when liveFeedback was last set

	// changedFiles is the set of workspace paths this run has written, fed by
	// the tool layer's OnFileChange hook. The QA gate formats exactly these —
	// quality.FormatChangedFiles refuses to format anything else.
	changedFiles map[string]bool
}

func New(cfg *config.Config) (*Orchestrator, error) {
	if cfg == nil {
		cfg = config.Default("")
	}
	cfg.ResolveAPIKey()
	store := contextstore.New(cfg.SlmDir())
	boardStore := plan.NewLiveStore(cfg.SlmDir())
	_ = boardStore.Load()

	// Token-native packer. NewPacker's byte budget silently capped a 32K model
	// at ~3.2K tokens (MaxContextKB defaults to 16 → 16*1024/4 with the legacy
	// reserves), which is the single biggest context regression in the harness.
	profile := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
	contextLimit := profile.ContextLimit
	if contextLimit <= 0 {
		contextLimit = contextstore.TokensFromKB(cfg.MaxContextKB)
	}
	// One repo map per run, cached under .slmcode. Build failures are
	// non-fatal: the packer simply has no symbol index.
	repoMap, repoErr := repomap.Build(cfg.Root, repomap.Options{CacheDir: cfg.SlmDir()})

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

	ws, tracker, err := workspace.RegisterCodingToolsWithWorkspace(toolReg, cfg.Root, workspace.ToolOpts{
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
				// The QA gate's formatting pass is scoped to exactly this set.
				o.noteChangedFiles(path)
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
		// Tool-layer knobs. Zero keeps the package default. These read the
		// config fields (disable_syntax_check, read_window_lines,
		// max_tool_chars, shell_timeout) with the legacy SLMCODE_* variables
		// still honored as overrides — see options.go.
		DisableSyntaxCheck: o.syntaxCheckDisabled(),
		ReadWindowLines:    o.readWindowLines(),
		MaxToolChars:       o.maxToolChars(),
		ShellTimeout:       o.shellTimeout(),
	})
	if err != nil {
		return nil, err
	}
	// Credentials the workspace cannot discover for itself. It scans the
	// environment and .slmcode/auth.json, but a key that arrived as `--api-key`
	// or as `api_key:` in a user-level config file lives ONLY in the resolved
	// Config — and an agent that runs `env` or `cat` in a repo that happens to
	// contain it would have got it back verbatim.
	ws.AddSecrets(cfg.APIKey, cfg.EmbeddingAPIKey)
	if err := models.RegisterFindModelsTool(toolReg, cfg); err != nil {
		return nil, err
	}
	// ws_skill closes the progressive-disclosure loop: pkg/skills renders cards
	// for every match and only expands a body on an explicit reference, so an
	// agent needs a way to ask for the body it just saw a card for.
	if err := registerSkillTool(toolReg, loader); err != nil {
		return nil, err
	}

	var mcpMgr *mcp.Manager
	if len(cfg.MCPServers) > 0 {
		mcpMgr = &mcp.Manager{Log: func(f string, a ...interface{}) {
			if o != nil {
				o.emitFull("init", stream.KindDebug, "mcp", "", fmt.Sprintf(f, a...), "", "")
			}
		}}
		mcpMgr.Servers = mcpServerConfigs(cfg.MCPServers)
		infos, _ := mcpMgr.Connect(context.Background())
		_ = mcpMgr.RegisterTools(toolReg, infos)
	}

	// AgentConfig.Provider must match a name registered in the ProviderManager.
	providerName := config.NormalizeProvider(cfg.Provider)
	cfg.Provider = providerName
	factory := agents.NewFactory(llmManager, toolReg, cfg.Model, providerName)
	// Also load agent blocks from the project's .slmcode/blocks/agents dir so
	// pipeline presets referencing go-tester / go-worker / react-tester etc.
	// resolve to real registered roles even without explicit materialization.
	factory.CustomDirs = append([]string{cfg.AgentsDir(),
		filepath.Join(blocks.ProjectBlocksDir(cfg.Root), "agents")}, agents.GlobalAgentRoots()...)
	// Register every agent block from the blocks registry (builtin + project +
	// user + extra) as a factory role — on-disk custom files still win on id
	// clash because ExtraCustoms are merged last.
	if reg, regErr := blocks.Load(cfg.Root); regErr == nil {
		for _, ab := range reg.Agents {
			spec := ab.Spec
			_ = agents.NormalizeCustom(&spec)
			factory.ExtraCustoms = append(factory.ExtraCustoms, spec)
		}
	}
	factory.ModelProfiles = cfg.ModelProfiles
	if prof := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model); prof.MaxTokens > 0 || prof.MaxTurns > 0 || prof.Temperature > 0 {
		factory.ProfileMaxTokens = prof.MaxTokens
		factory.ProfileMaxTurns = prof.MaxTurns
		factory.ProfileTemp = prof.Temperature
	}
	// Use fast model for lightweight agents when configured
	if cfg.FastModel != "" {
		factory.SetFastModel(cfg.FastModel)
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

	registry, regErr := factory.BuildRegistry()
	if regErr != nil {
		return nil, regErr
	}
	exec := ggagent.NewSubAgentExecutor(registry)
	exec.SetParallel(true)
	exec.SetTimeout(cfg.TaskTimeout)

	o = &Orchestrator{
		cfg:         cfg,
		store:       store,
		boardStore:  boardStore,
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
		workspace:   ws,
		tracker:     tracker,
		repoMap:     repoMap,
	}
	// The packer carries the whole context-engineering config (repo map budget,
	// excerpt window, reserves, slack, per-role shares) — see context_wiring.go.
	o.buildPackers(repoMap, contextLimit)
	// structured_decoding: off means prompt-only JSON for every role. The
	// enforcement point is pkg/backends' capability cache, and it has to run
	// AFTER RegisterLLM (which points the cache at .slmcode and would otherwise
	// re-probe over the top of what we seed here).
	o.applyStructuredDecodingPolicy()
	if repoErr != nil {
		o.emitFull("init", stream.KindDebug, "repomap", "",
			"repo map unavailable: "+repoErr.Error(), "", "")
	}
	// Project instructions belong in the STABLE PREFIX of every specialist
	// pack, not only in SCRATCH.md (which nothing but the Studio API reads).
	o.projectInstructions = instructions.LoadProjectInstructions(cfg.Root)

	// Self-improvement engine. A degraded engine is still safe to call, and a
	// nil one is tolerated everywhere, so failures never stop a run.
	if o.evolveEnabled() {
		eng, evErr := evolve.OpenWith(cfg.Root, "", evolve.EngineOptions{
			Deterministic: o.deterministicMode(),
		})
		o.evolve = eng
		if evErr != nil {
			o.emitFull("init", stream.KindDebug, "evolve", "",
				"evolve degraded: "+evErr.Error(), "", "")
		}
	}

	// Reset the per-task tool loop guard whenever the runner starts a task.
	if tracker != nil {
		tracker.ResetAll()
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

// OnEvent registers the event sink. Registering one also marks this
// orchestrator as SUBSCRIBED, which is what the HITL gates use to decide
// whether an unanswered ask means "nobody was listening" (safe to proceed) or
// "somebody was listening and did not answer" (not safe to proceed).
func (o *Orchestrator) OnEvent(h EventHandler) {
	o.mu.Lock()
	o.eventSubscribed = h != nil
	o.mu.Unlock()
	if h == nil {
		h = func(Event) {}
	}
	// Guarded by its own lock, not o.mu: emitters must never take o.mu (many
	// of them are called from code paths that already hold it), and Studio
	// re-subscribes from setOrch while a run can be emitting.
	o.evMu.Lock()
	o.onEvent = h
	o.evMu.Unlock()
}

// eventSink returns the current handler. Never nil.
func (o *Orchestrator) eventSink() EventHandler {
	o.evMu.RLock()
	h := o.onEvent
	o.evMu.RUnlock()
	if h == nil {
		return func(Event) {}
	}
	return h
}

// Subscribed reports whether any UI is attached to this orchestrator.
func (o *Orchestrator) Subscribed() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.eventSubscribed || o.onPlanApprove != nil || o.onAsk != nil ||
		o.onContinue != nil || o.onEscalate != nil
}

func (o *Orchestrator) Store() *contextstore.Store { return o.store }
func (o *Orchestrator) Board() *plan.LiveStore     { return o.boardStore }
func (o *Orchestrator) Skills() *skills.Loader     { return o.skills }

// SetLiveFeedback stores a free-form steering message from the user. The next
// agent calls pick it up and adjust their work. Returns the stored text
// ("" when the input was empty, which also clears any previous feedback).
func (o *Orchestrator) SetLiveFeedback(text string) string {
	if o == nil {
		return ""
	}
	text = strings.TrimSpace(text)
	o.mu.Lock()
	o.liveFeedback = text
	if text == "" {
		o.liveFeedbackAt = ""
	} else {
		o.liveFeedbackAt = time.Now().UTC().Format(time.RFC3339)
	}
	o.mu.Unlock()
	if text == "" {
		return ""
	}
	if o.store != nil {
		_ = o.store.Append(contextstore.DocScratch, "Live feedback", text)
	}
	o.emitFull("execute", stream.KindIntervention, "user", "",
		"live feedback accepted — injected into next agent prompt", "", text)
	return text
}

// ClearLiveFeedback removes any pending live feedback.
func (o *Orchestrator) ClearLiveFeedback() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	o.liveFeedback = ""
	o.liveFeedbackAt = ""
	o.mu.Unlock()
	o.emitFull("execute", stream.KindIntervention, "user", "", "live feedback cleared", "", "")
	return ""
}

// LiveFeedback returns the current live feedback text ("" when none).
func (o *Orchestrator) LiveFeedback() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.liveFeedback
}

// LiveFeedbackInfo returns the current live feedback text and the RFC3339
// timestamp when it was last set.
func (o *Orchestrator) LiveFeedbackInfo() (text, at string) {
	if o == nil {
		return "", ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.liveFeedback, o.liveFeedbackAt
}
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
		o.clearPackCaches()
		// The model can be re-pointed between runs (stacks, Studio, --model), so
		// re-resolve the window rather than trusting the one New() saw.
		// Rebuild rather than SetContextLimitTokens: that setter resets the
		// budget to contextstore's DEFAULT reserves and would throw away every
		// context_reserve_* / context_slack_percent the operator configured.
		o.rebuildPacker(o.contextLimitTokens())
	}
	o.resetChangedFiles()
	o.refreshRepoMap()
	if o.cfg != nil {
		clearDynamicRunArtifacts(o.cfg.SlmDir())
		clearPendingHITL(o.cfg.SlmDir())
	}
	o.mu.Lock()
	o.latencyMs = map[string]int64{}
	o.usage = TokenUsage{}
	o.refineRound = 0
	o.llmCalls = 0
	o.runStart = start
	o.decisions = nil
	o.gates = nil
	o.dynamicSkills = nil
	o.dynamicBrief = ""
	o.dynamicComposition = nil
	o.loadPipelineLocked()
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
	o.startEvolveRun(runID, query)
	o.applyRoleModelPolicy()
	o.emitShellPolicyNotice()
	o.injectPriorKnowledge(ctx, query)
	o.seedAdaptiveLessons()
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

// mcpServerConfigs translates the config's MCP server list into mcp.ServerConfig.
//
// Both halves of the read-only decision are set from the one input. Writable is
// the field mcp.ServerConfig.IsReadOnly() actually consults — it reads
// `ReadOnly || !Writable`, so a zero-valued config is read-only and a config
// that set only `ReadOnly: false` stayed read-only anyway. A user's explicit
// `read_only: false` was therefore inert, and their MCP server's write tools
// were silently dropped.
func mcpServerConfigs(servers []config.MCPServerConfig) []mcp.ServerConfig {
	out := make([]mcp.ServerConfig, 0, len(servers))
	for _, sc := range servers {
		// Read-only unless the user says otherwise: an MCP server is arbitrary
		// third-party code holding tools this harness will call unattended.
		ro := true
		if sc.ReadOnly != nil {
			ro = *sc.ReadOnly
		}
		out = append(out, mcp.ServerConfig{
			Name: sc.Name, Command: sc.Command, Args: sc.Args, Env: sc.Env,
			URL: sc.URL, ReadOnly: ro, Writable: !ro,
		})
	}
	return out
}

// skillPackFor builds a role-targeted skill pack (pins + @skill + agent defaults
// + composer-selected skills for this role), prefixed with the project
// instructions and any memory worth saying.
//
// Ordering matters: instructions and the collaboration brief are STABLE across
// every call in a run, so they go first and stay byte-identical, which is what
// keeps the provider's KV-cache prefix reusable. Memory and the matched skills
// vary per role and come after.
func (o *Orchestrator) skillPackFor(role, query string) string {
	return o.skillPackForScoped(role, query, o.runFileScope())
}

// skillPackForScoped is skillPackFor with an explicit file scope.
//
// scope gates skills carrying `paths:` frontmatter (pkg/skills/scope.go): a
// Rust skill must not load into a Python project just because its description
// matched a word in the query. An empty scope disables gating, which is what
// every phase before the board exists passes, so nothing regresses where no
// scope is available.
func (o *Orchestrator) skillPackForScoped(role, query string, scope []string) string {
	if o == nil {
		return ""
	}
	var pins []string
	if o.cfg != nil {
		pins = append(pins, o.cfg.PinnedSkills...)
	}
	o.mu.Lock()
	pins = append(pins, o.dynamicSkills[strings.ToLower(strings.TrimSpace(role))]...)
	brief := o.dynamicBrief
	instr := o.projectInstructions
	o.mu.Unlock()

	var parts []string
	if s := strings.TrimSpace(instr); s != "" {
		parts = append(parts, "## Project instructions (authoritative)\n\n"+truncate(s, 6000))
	}
	if s := strings.TrimSpace(brief); s != "" {
		parts = append(parts, s)
	}
	if mem := o.memoryBlockFor(role); mem != "" {
		parts = append(parts, mem)
	}
	if o.skills != nil {
		// Progressive disclosure, not the pre-disclosure dump: RenderMatches
		// honors skill_disclosure (auto | cards | full) and skill_max_expanded,
		// which RenderPack ignored — so both knobs were inert in production.
		matches := o.skills.ResolveMatchesScoped(query, role, pins, 4, scope)
		if rendered := strings.TrimSpace(
			skills.RenderMatches(matches, skillPackOptions(o.cfg, 1600))); rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, "\n\n")
}

// maxRunScopePaths bounds the scope list handed to the skill gate. It is a
// matcher input, not a manifest: a 900-task board does not need every path to
// decide whether a Rust skill applies.
const maxRunScopePaths = 200

// runFileScope is the union of the live board's task files — the paths this run
// is actually about. It is empty before the board exists (plan/split), and an
// empty scope disables path gating.
func (o *Orchestrator) runFileScope() []string {
	if o == nil || o.boardStore == nil {
		return nil
	}
	board := o.boardStore.Snapshot()
	seen := make(map[string]bool)
	out := make([]string, 0, 16)
	for _, t := range board.AllTasks() {
		for _, f := range t.Files {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
			if len(out) >= maxRunScopePaths {
				return out
			}
		}
	}
	return out
}

// memoryBlockFor renders the evolve memory block for a role, budgeted from the
// role's own pack budget. It returns "" when there is nothing worth saying, so
// no heading is emitted around an empty string.
func (o *Orchestrator) memoryBlockFor(role string) string {
	if o == nil || o.evolve == nil {
		return ""
	}
	mem := o.evolve.Memory()
	if mem == nil {
		return ""
	}
	budget := o.memoryTokens()
	if p := o.packerNow(); p != nil {
		if b := p.BudgetTokensFor(role) / 6; b > 0 {
			budget = b
		}
	}
	return strings.TrimSpace(mem.RenderForPrompt(role, budget))
}

// refreshProjectInstructions reloads AGENTS.md / CLAUDE.md gated to the paths
// this run actually touches, so a repo whose instructions are path-scoped only
// pays for the sections that apply.
func (o *Orchestrator) refreshProjectInstructions(scopePaths []string) string {
	if o == nil || o.cfg == nil {
		return ""
	}
	instr := instructions.LoadForScope(o.cfg.Root, scopePaths)
	if strings.TrimSpace(instr) == "" {
		instr = instructions.LoadProjectInstructions(o.cfg.Root)
	}
	o.mu.Lock()
	o.projectInstructions = instr
	o.mu.Unlock()
	if o.packer != nil {
		// The stable prefix changed; cached packs carry the old one.
		o.clearPackCaches()
	}
	return instr
}

// resolvedThinkPasses is config think_passes, with the bandit picking between
// the configured value and its immediate neighbors when the engine is on.
// A user who pinned think_passes explicitly is never overridden by more than
// one step, so the policy tunes rather than reinterprets.
func (o *Orchestrator) resolvedThinkPasses() int {
	base := 1
	if o != nil && o.cfg != nil {
		base = o.cfg.ThinkPasses
	}
	if base <= 0 {
		base = 1
	}
	if o == nil || o.evolve == nil {
		return base
	}
	arms := []string{itoa(base)}
	if base > 1 {
		arms = append(arms, itoa(base-1))
	}
	if base < 3 {
		arms = append(arms, itoa(base+1))
	}
	switch o.choose(evolve.DecThinkPasses, arms...) {
	case "1":
		return 1
	case "2":
		return 2
	case "3":
		return 3
	}
	return base
}

// explorePolicyApplies reports whether the explore decision is genuinely
// discretionary for this query (no explicit user/config signal forcing it).
func (o *Orchestrator) explorePolicyApplies(query string) bool {
	if o == nil || o.evolve == nil || o.cfg == nil {
		return false
	}
	if o.cfg.ThinkPasses >= 3 || wantsForceExplore(query) {
		return false
	}
	return true
}

func boolArm(on bool) string {
	if on {
		return "on"
	}
	return "off"
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
		// `slmcode agent`, singular. The plural does not exist, so the one
		// remedy this error offered was a command that fails.
		return nil, fmt.Errorf("unknown specialist %q — run `slmcode agent list` to see the registered ones", role)
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
	tp, _ := o.packBuild(role, query, contextstore.DefaultDocsForRole(role), discovered, packSkills)
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
	// 0 Auto-load AGENTS.md / CLAUDE.md / PROJECT instructions (Claude Code style).
	//
	// These used to be appended to SCRATCH.md — a write-only sink, written in 25
	// places and read in exactly one (the Studio API) — and concatenated into a
	// skillPack that was threaded through runSLM → finalizeAfterExecute →
	// completeRun and discarded with `_ = skillPack`. Meanwhile every
	// packer.Build call used o.skillPackFor, which rebuilt from skills alone, so
	// no specialist prompt ever saw AGENTS.md. They now live on the orchestrator
	// and skillPackFor puts them in the STABLE PREFIX of every pack.
	//
	// LoadForScope gates sections by path glob, so a repo whose instructions are
	// scoped to subtrees only pays for the sections that apply to this query.
	if instr := o.refreshProjectInstructions(plan.DiscoverRelevantFiles(o.cfg.Root, query, "")); instr != "" {
		o.emit("init", "loaded project instructions (AGENTS.md/CLAUDE.md/PROJECT) into every pack prefix", "")
		o.shared.SetGlobal("project_instructions", instr)
	}

	// 0a Parse @file: / @folder: references from the query (Claude Code–style)
	if refs := extractFileRefs(query); len(refs) > 0 {
		o.shared.SetGlobal("query_file_refs", strings.Join(refs, ","))
		discoveredEarly := plan.ReconcileFiles(o.cfg.Root, refs, refs)
		_ = o.store.ReplaceSection(contextstore.DocContext, "User file refs", "- "+strings.Join(discoveredEarly, "\n- "))
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
		_ = o.store.ReplaceSection(contextstore.DocContext, "Workspace inventory", invMD)
		o.shared.SetGlobal("workspace_files", strings.Join(inventory, ","))
		skillPack = skillPack + "\n\n" + invMD + "\n"
		o.emit("init", fmt.Sprintf("indexed %d workspace file(s)", len(inventory)), "")
	}

	// 1+2 Context + Explore run in parallel after init.
	// context is fast (≤400 words CONTEXT.md); explore does a codebase deep-dive.
	var exploreOut, archOut, docsOut string

	parResults := runPhaseParallel(ctx,
		func() phaseResult {
			// --- 1 Context ---
			if err := o.runPipelineSlots(ctx, "context", "before", query, "", ""); err != nil {
				return phaseResult{name: "context", err: err}
			}
			ctxAgent := o.phaseAgent("context", plan.RoleContext)
			if o.phaseEnabled("context") && !o.Pipeline().HasReplace("context") {
				o.emitAgent("context", ctxAgent, "", "updating working context", "", "")
				pack, _ := o.packBuild(ctxAgent, query, contextstore.DefaultDocsForRole("context"), discoveredEarly, o.skillPackFor(ctxAgent, query))
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
					return phaseResult{name: "context", err: err}
				}
			}
			if err := o.runPipelineSlots(ctx, "context", "after", query, "", ""); err != nil {
				return phaseResult{name: "context", err: err}
			}
			// Re-assert authoritative paths after the context rewrite (SLMs often invent main.go).
			if len(inventory) > 0 {
				invMD := "- " + strings.Join(inventory, "\n- ")
				_ = o.store.ReplaceSection(contextstore.DocContext, "Workspace inventory (authoritative)", invMD)
				if real := plan.FilterExisting(o.cfg.Root, discoveredEarly); len(real) > 0 {
					_ = o.store.ReplaceSection(contextstore.DocContext, "Likely targets (existing files only)",
						"- "+strings.Join(real, "\n- "))
				}
			}
			// 1b Ensure PROJECT.md is populated (was empty scaffold during self-use)
			o.ensureProjectDoc(inventory)
			return phaseResult{name: "context"}
		},
		func() phaseResult {
			// --- 2 Explore ---
			if err := o.runPipelineSlots(ctx, "explore", "before", query, "", ""); err != nil {
				return phaseResult{name: "explore", err: err}
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
					return phaseResult{name: "explore", err: err}
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
				expPack, _ := o.packBuild(expAgent, query, contextstore.DefaultDocsForRole("explorer"), nil, o.skillPackFor(expAgent, query))
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
				var exploreErr error
				exploreOut, archOut, docsOut, exploreErr = o.speculateDigs(ctx, query, explorePrompt, inventory, needDocs, needArch)
				if exploreErr != nil {
					return phaseResult{name: "explore", err: fmt.Errorf("explorer: %w", exploreErr)}
				}
				// Build the document ONCE. The two Appends used to be clobbered
				// three lines later by the Write, so the docs and architecture
				// sections never survived to disk.
				var doc strings.Builder
				doc.WriteString("# Exploration\n\n")
				if docsOut != "" {
					exploreOut += "\n\n" + docsOut
					o.shared.SetGlobal("docs_exploration", docsOut)
				}
				doc.WriteString(exploreOut)
				if archOut != "" {
					doc.WriteString("\n\n## Architecture\n\n")
					doc.WriteString(archOut)
					o.shared.SetGlobal("architecture", archOut)
				}
				_ = o.store.Write(contextstore.DocScratch, doc.String())
				o.shared.SetGlobal("exploration", exploreOut)
				o.shared.SetGlobal("explore_mode", "deep")
			}
			if err := o.runPipelineSlots(ctx, "explore", "after", query, exploreOut, ""); err != nil {
				return phaseResult{name: "explore", err: err}
			}
			return phaseResult{name: "explore", output: exploreOut}
		},
	)

	// Explore errors are fatal; context errors are non-blocking warnings.
	if err := canceledPhase(ctx, parResults); err != nil {
		return o.checkpointInterrupt(ctx, &plan.Board{QueryID: runID, Query: query}, session.PhasePlan, err)
	}
	if r, ok := parResults["explore"]; ok && r.err != nil {
		return nil, r.err
	}

	// 2a Dynamic pipeline composition (optional): the composer specialist assembles
	// a task-specific pipeline (phases, team, tools, skills) before design/plan.
	if o.cfg.DynamicPipeline {
		o.composeDynamicPipeline(ctx, query, inventory, exploreOut, archOut)
	}

	// 2b+2c Architect + Clarify run in parallel after explore.
	// Both depend on explore output but not on each other.
	var interview plan.ScopeInterview
	var clarify plan.ClarifyResult
	var prd plan.ScopePRD

	archClarifyResults := runPhaseParallel(ctx,
		func() phaseResult {
			// --- 2b Architect (skip if already ran in speculative digs) ---
			archWhen := o.Pipeline().PhaseWhen("architect")
			wantArch := o.phaseEnabled("architect") && archWhen != pipeline.WhenNever &&
				(archWhen == pipeline.WhenAlways || wantsArchitect(query))
			if wantArch && strings.TrimSpace(archOut) == "" {
				if err := o.runPipelineSlots(ctx, "architect", "before", query, exploreOut, ""); err != nil {
					return phaseResult{name: "architect", err: err}
				}
				if o.Pipeline().HasReplace("architect") {
					if err := o.runPipelineSlots(ctx, "architect", "replace", query, exploreOut, ""); err != nil {
						return phaseResult{name: "architect", err: err}
					}
				} else {
					archAgent := o.phaseAgent("architect", "architect")
					o.emitAgent("architect", archAgent, "", "minimal design pass", "", "")
					archPack, _ := o.packBuild(archAgent, query, contextstore.LeanDocsForRole("architect"), nil, o.skillPackFor(archAgent, query))
					archOut, _ = o.runRoleTracked(ctx, archAgent, "", archPack.Render()+"\nExploration:\n"+truncate(exploreOut, 2500)+"\nReturn STRICT JSON design.")
					if strings.TrimSpace(archOut) != "" {
						_ = o.store.Append(contextstore.DocScratch, "Architecture", archOut)
						o.shared.SetGlobal("architecture", archOut)
					}
				}
				if err := o.runPipelineSlots(ctx, "architect", "after", query, exploreOut, ""); err != nil {
					return phaseResult{name: "architect", err: err}
				}
			}
			return phaseResult{name: "architect"}
		},
		func() phaseResult {
			// --- 2c Clarify / scope interview ---
			// pipeline gate: phaseEnabled("clarify") — when=never skips this phase
			if !o.phaseEnabled("clarify") {
				o.emit("clarify", "phase disabled — skipping interview", "")
				return phaseResult{name: "clarify"}
			}
			interview = o.runScopeInterview(ctx, query, exploreOut)
			clarify = interview.ToClarifyResult()
			prd = interview.PRD
			return phaseResult{name: "clarify"}
		},
	)

	// Architect errors are fatal.
	if err := canceledPhase(ctx, archClarifyResults); err != nil {
		return o.checkpointInterrupt(ctx, &plan.Board{QueryID: runID, Query: query}, session.PhasePlan, err)
	}
	if r, ok := archClarifyResults["architect"]; ok && r.err != nil {
		return nil, r.err
	}

	// 3+4 Plan / Split / approval — extracted so runSLM reads as a pipeline
	// rather than one 500-line function.
	board, _, planOut, err := o.runPlanSplitApprove(ctx, planSplitInput{
		RunID:      runID,
		Query:      query,
		ExploreOut: exploreOut,
		ArchOut:    archOut,
		Inventory:  inventory,
		Discovered: discoveredEarly,
		PRD:        prd,
		Clarify:    clarify,
	})
	if err != nil {
		return nil, err
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
	o.applyArchitectEditorRoles(board)
	runner := o.buildRunner(query, runID, skillPack)
	snap := o.boardStore.Snapshot()
	board = &snap
	for i := range board.Tasks {
		board.Tasks[i].Description = loop.StripScopedPack(board.Tasks[i].Description)
	}
	o.persistBoard(board)

	session.SetPhase(o.cfg.SlmDir(), o.currentTurn, session.PhaseExecute)
	execStart := time.Now()
	// pipeline gate: phaseEnabled("execute") — when=never skips this phase
	if !o.phaseEnabled("execute") {
		o.emit("execute", "phase disabled — skipping board execution", "")
	} else if err := runner.RunBoard(ctx, board); err != nil {
		return o.checkpointInterrupt(ctx, board, session.PhaseExecute, err)
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
	// Without CacheDir the embedding cache and query-dir pruning never
	// activate, so every run re-embeds the whole corpus. retrieval_cache_dir
	// relocates it; empty keeps .slmcode.
	cacheDir := strings.TrimSpace(o.cfg.RetrievalCacheDir)
	if cacheDir == "" {
		cacheDir = o.cfg.SlmDir()
	} else if !filepath.IsAbs(cacheDir) {
		cacheDir = filepath.Join(o.cfg.Root, cacheDir)
	}
	body, mode, err := retrieval.RetrieveForQuery(ctx, o.cfg.SlmDir(), query, retrieval.Config{
		Enabled: enabled, Endpoint: endpoint, Model: model, APIKey: apiKey, TopK: topK,
		CacheDir: cacheDir,
		// retrieval_min_score raises the similarity floor above the calibrated
		// per-embedder default; 0 keeps the calibrated value.
		MinScore: o.cfg.RetrievalMinScore,
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
	pack, _ := o.packBuild("worker", query,
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

// resolveExecRole maps an unregistered role (e.g. go-tester from a pipeline
// preset whose agent blocks never materialized) to a known generic role so
// the executor never fails with "subagent not found".
func (o *Orchestrator) resolveExecRole(role string) string {
	// Without a factory we cannot verify registration — trust the role as-is
	// (test fakes and nil-safe callers rely on the original role passing through).
	if o == nil || o.factory == nil {
		return role
	}
	if o.knownAgent(role) {
		return role
	}
	mapped := genericRoleFor(role)
	if !o.knownAgent(mapped) {
		if strings.Contains(strings.ToLower(role), "tester") {
			return plan.RoleTester
		}
		return plan.RoleWorker
	}
	return mapped
}

// applyRoleModelPolicy lets the bandit choose whether the light roles
// (reviewer, context, memory, coordinator, escalate — short structured outputs,
// never implementation) run on the configured fast model or on the main one.
//
// It runs ONCE per run, before any agent is built. That is not a style choice:
// Factory.BuildRegistry resolves each role's effective model into an agent
// DEFINITION at construction time, ggagent's AgentRegistry has no way to
// replace a registered definition, and mutating FastModel while workers are
// running would be a data race on a shared field. So the model a role uses
// cannot change mid-run — which is why the escalate gate's retry does not
// "escalate the model" for the reopened task, however tempting that is: doing
// it honestly needs a registry rebuild plus an executor swap under the running
// wave, and a per-run decision is not worth that.
//
// agents.Factory.SetPreferFast now makes the arm expressible PER ROLE (it
// overrides isLightAgent(spec.ID) in both directions). This function still
// pulls one arm for the whole light set, because the DecRoleModel decision key
// is recorded per role-CLASS: making the arm per-role means recording it per
// role first, otherwise several roles credit and blame one shared statistic.
func (o *Orchestrator) applyRoleModelPolicy() {
	if o == nil || o.evolve == nil || o.cfg == nil || o.factory == nil {
		return
	}
	fast := strings.TrimSpace(o.cfg.FastModel)
	if fast == "" {
		return
	}
	if o.choose(evolve.DecRoleModel, "fast", "heavy") == "heavy" {
		o.factory.FastModel = ""
		o.emit("init", "policy: light roles on the main model this run", "")
		return
	}
	o.factory.FastModel = fast
}

// genericRoleFor strips language/kind affixes from a role id so
// go-tester → tester, python-worker → worker, react-tester → tester.
func genericRoleFor(role string) string {
	r := strings.TrimSpace(strings.ToLower(role))
	for _, p := range []string{"go-", "python-", "react-", "js-", "ts-", "rust-", "java-", "cpp-", "c-", "web-", "shell-", "bash-", "node-"} {
		if strings.HasPrefix(r, p) {
			r = strings.TrimPrefix(r, p)
			break
		}
	}
	for _, s := range []string{"-tester", "-worker", "-deep", "-reviewer", "-corrector", "-explorer", "-planner", "-splitter", "-memory"} {
		if strings.HasSuffix(r, s) {
			r = strings.TrimSuffix(r, s)
			break
		}
	}
	return r
}

func (o *Orchestrator) runRoleTracked(ctx context.Context, role, taskID, input string) (string, error) {
	// Live user steering: highest priority — the next agent call adjusts.
	if fb := o.LiveFeedback(); fb != "" {
		input = "\n\n## LIVE FEEDBACK FROM USER (highest priority — adjust your work now)\n" + fb + "\n" + input
	}
	execRole := o.resolveExecRole(role)
	if execRole != role {
		o.emit(role, fmt.Sprintf("fallback role %s → %s (agent not registered)", role, execRole), "")
	}
	o.emitFull(execRole, stream.KindAgentStart, execRole, taskID, "started", scopeFromInput(input), "")
	o.bumpLLMCalls(1)
	start := time.Now()
	timeout := o.roleTimeout(execRole)
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results, err := o.executor.ExecuteSubAgents(rctx, []ggagent.SubAgentRequest{{
		AgentID: execRole, Input: input, Timeout: timeout, ShareState: true,
	}}, o.shared)
	elapsed := time.Since(start)
	o.recordLatency(execRole, elapsed)
	if len(results) == 0 {
		o.emitFullL(execRole, stream.KindAgentEnd, execRole, taskID, "no result", "", "", stream.LevelError)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("no result from %s", execRole)
	}
	out := ""
	if results[0].Output != nil {
		out = fmt.Sprintf("%v", results[0].Output)
	}
	o.recordResultUsage(results[0], input, out)
	if results[0].Error != nil && out == "" {
		o.emitFullL(execRole, stream.KindAgentEnd, execRole, taskID,
			"error: "+results[0].Error.Error(), "", "", stream.LevelError)
		return "", results[0].Error
	}
	endLevel := stream.LevelSuccess
	if results[0].Error != nil {
		// Partial output with an error is still a failure for the ✔/✖ tally.
		endLevel = stream.LevelWarn
	}
	o.emitFullL(execRole, stream.KindAgentEnd, execRole, taskID,
		fmt.Sprintf("finished (%s)", elapsed.Round(time.Millisecond)),
		scopeFromInput(input), truncate(out, 1500), endLevel)
	o.emitFull(execRole, stream.KindLatency, execRole, taskID,
		fmt.Sprintf("%s %dms", execRole, elapsed.Milliseconds()), "", "")
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

// runRoleMultipassTracked runs the multi-pass cycle for a role with the same
// guarantees as its single-shot twin runRoleTracked: a timeout, latency and
// usage accounting, live-feedback injection, and a Level-tagged AgentEnd.
//
// It used to have none of those, which mattered because it is used for the two
// slowest roles in the harness (planner and splitter) where one call issues up
// to 1+2×passes LLM round-trips — an unbounded, unaccounted stall.
func (o *Orchestrator) runRoleMultipassTracked(ctx context.Context, role, taskID, input string) (string, error) {
	passes := o.resolvedThinkPasses()
	if o == nil || o.cfg == nil || passes <= 1 {
		return o.runRoleTracked(ctx, role, taskID, input)
	}
	if o.think == nil || o.factory == nil {
		return o.runRoleTracked(ctx, role, taskID, input)
	}
	o.think.Passes = thinkRefinePasses(passes)
	// Live user steering: same priority as the single-shot path.
	if fb := o.LiveFeedback(); fb != "" {
		input = "\n\n## LIVE FEEDBACK FROM USER (highest priority — adjust your work now)\n" + fb + "\n" + input
	}
	execRole := o.resolveExecRole(role)
	if execRole != role {
		o.emit(role, fmt.Sprintf("fallback role %s → %s (agent not registered)", role, execRole), "")
	}

	// Per-pass timeout is the single-shot budget; the whole cycle gets the
	// pass budget times the worst-case number of calls, capped at the task
	// timeout so a stuck planner cannot outlive the run.
	pass := o.roleTimeout(execRole)
	budget := time.Duration(1+2*thinkRefinePasses(passes)) * pass
	if full := o.cfg.TaskTimeout; full > 0 && budget > full {
		budget = full
	}
	o.think.SetPassTimeout(pass).SetBudget(budget).SetFactory(o.factory.Create)
	o.think.OnCall = func(ci multipass.CallInfo) {
		o.recordLatency(execRole, ci.Elapsed)
		o.emitFull(execRole, stream.KindLatency, execRole, taskID,
			fmt.Sprintf("%s %s#%d %dms", execRole, ci.Pass, ci.Index, ci.Elapsed.Milliseconds()), "", "")
	}
	o.think.OnUsage = func(u multipass.Usage) {
		o.recordEstimatedUsage(u.InputChars, u.OutputChars)
	}

	o.emitFull(execRole, stream.KindAgentStart, execRole, taskID,
		fmt.Sprintf("multipass×%d (budget %s)", passes, budget.Round(time.Second)),
		scopeFromInput(input), "")
	rctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	start := time.Now()
	// ExecuteRole caches and resets the agent instead of rebuilding it: a
	// rebuild re-resolves the provider, tools and profile caps every call.
	out, err := o.think.ExecuteRole(rctx, execRole, input)
	elapsed := time.Since(start)
	if err != nil {
		o.emitFullL(execRole, stream.KindAgentEnd, execRole, taskID,
			"error: "+err.Error(), "", "", stream.LevelError)
		return "", err
	}
	o.emitFullL(execRole, stream.KindAgentEnd, execRole, taskID,
		fmt.Sprintf("multipass finished (%s)", elapsed.Round(time.Millisecond)),
		scopeFromInput(input), truncate(out, 1500), stream.LevelSuccess)
	return out, nil
}

// shouldDeepExplore returns false when PROJECT/CONTEXT/MEMORY already carry enough
// shared knowledge — avoids re-scanning the repo on every run.
func (o *Orchestrator) shouldDeepExplore(query string) (doDeep bool, reason string) {
	if os.Getenv("SLMCODE_FORCE_EXPLORE") == "1" {
		return true, ""
	}
	// The heuristics below are the PRIOR; the bandit gets the final say on the
	// genuinely discretionary case. Explicit forcing signals (a query that asks
	// to explore, think_passes>=3) stay hard rules — the policy chooses between
	// two defensible options, it does not override an instruction.
	defer func() {
		if !o.explorePolicyApplies(query) {
			return
		}
		switch o.choose(evolve.DecExplorePhase, boolArm(doDeep), boolArm(!doDeep)) {
		case "on":
			doDeep, reason = true, "policy: explore=on"
		case "off":
			doDeep, reason = false, "policy: explore=off"
		}
	}()
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
	// pipeline gate: phaseEnabled("coord") — when=never skips this phase
	if !o.phaseEnabled("coord") {
		o.emitFull("coord", stream.KindCoord, "coordinator", "", "coordinator skipped — phase disabled", "", "")
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
			nt := plan.Task{
				Title: title, Description: a.Text,
				Role: plan.RoleWorker, Column: plan.ColReadyToDev, Files: wrap.FocusFiles,
			}
			if a.Role != "" {
				nt.Role = a.Role
			}
			// AddTask allocates the id under the board lock — the coordinator
			// can be appending while parallel review appends too.
			board.AddTask(nt)
		}
	}
	if len(wrap.FocusFiles) > 0 {
		o.shared.SetGlobal("focus_files", strings.Join(wrap.FocusFiles, ","))
	}
}

// evolveAfterWave updates CONTEXT + MEMORY from wave results and refreshes
// pending task packs so later specialists see evolving project knowledge.
func (o *Orchestrator) evolveAfterWave(ctx context.Context, query, skillPack string, board *plan.Board, wave []plan.Task) {
	// pipeline gate: phaseEnabled("learn") — when=never skips this phase
	if !o.phaseEnabled("learn") {
		return
	}
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
			fmt.Fprintf(&brief, "- %s [%s] %s | out=%s | err=%s\n",
				t.ID, t.Column, t.Title, truncate(t.Output, 240), truncate(t.Error, 120))
		}
		if distilled, err := o.runRole(ctx, "memory", agents.PromptLearner+"\n\n"+brief.String()); err == nil {
			if bullets := learning.JSONLessonsToMarkdown(distilled); bullets != "" {
				md = strings.TrimSpace(md + "\n" + bullets)
			}
		}
	}

	if md != "" {
		_ = o.store.Append(contextstore.DocMemory, "Wave lessons", md)
		_ = learning.AppendGlobalMemory("Wave lessons", md)
		o.shared.SetGlobal("latest_lessons", md)
		o.shared.SetGlobal("adaptive_lessons", md)
	}

	o.refineRound++
	if refine.ShouldRun(o.cfg.AutoRefine, o.cfg.AutoRefineMaxRounds, o.refineRound, len(lessons)) {
		out := refine.Build(refine.Input{
			Query: query, Lessons: lessons,
			WaveNote: learning.ContextDelta(wave), Round: o.refineRound,
		})
		if !out.Skip && out.Markdown != "" {
			_ = o.store.Append(contextstore.DocContext, "Refine", out.Markdown)
			o.emitFull("learn", stream.KindOutput, "refine", "",
				fmt.Sprintf("auto-refine round %d (%d lessons)", o.refineRound, len(lessons)),
				"", truncate(out.Markdown, 400))
		}
	}

	// Keep descriptions lean; BuildInput injects fresh packs at execute time.
	for i := range board.Tasks {
		t := board.Tasks[i]
		t.Normalize()
		t.Description = loop.StripScopedPack(t.Description)
		board.Tasks[i] = t
	}
}

func (o *Orchestrator) seedAdaptiveLessons() {
	if o == nil || o.store == nil || o.shared == nil {
		return
	}
	projectMemory, _ := o.store.Read(contextstore.DocMemory)
	globalMemory := learning.ReadGlobalMemory()
	adaptive := learning.RecentAdaptiveMemory(projectMemory, globalMemory, 1600)
	if adaptive == "" {
		return
	}
	o.shared.SetGlobal("adaptive_lessons", adaptive)
	o.emitFull("learn", stream.KindOutput, "memory", "",
		"seeded adaptive lessons from project/global memory", "", truncate(adaptive, 500))
}

func (o *Orchestrator) contextSummarizer() compact.Summarizer {
	return func(ctx context.Context, body string, maxBytes int) (string, error) {
		prompt := compact.BuildLLMCompactPrompt(body, maxBytes)
		return o.runRole(ctx, "memory", prompt)
	}
}

// contextBudgetKB returns the soft and hard CONTEXT.md byte budgets in KB.
func (o *Orchestrator) contextBudgetKB() (soft, hard int) {
	soft = 16
	if o != nil && o.cfg != nil && o.cfg.MaxContextKB > 0 {
		soft = o.cfg.MaxContextKB
	}
	return soft, soft * 2
}

// compactContext is the single compaction path for CONTEXT.md.
//
// It picks the engine with compact.EngineFor (which downgrades to the
// heuristic when the body is too far over budget for an SLM to summarize
// safely), targets compact.CompactTargetBytes (~70% of the trigger, so the very
// next Append does not re-trigger a full LLM compaction), SNAPSHOTS the
// pre-compaction body to CONTEXT.md.bak before overwriting — an LLM that ate
// CONTEXT.md must be recoverable — and reports res.Rejected when a candidate
// summary failed the acceptance gates and fell back to the heuristic.
func (o *Orchestrator) compactContext(ctx context.Context, body string) (compact.Result, error) {
	soft, hard := o.contextBudgetKB()
	preferred := "heuristic"
	if o.cfg != nil && strings.TrimSpace(o.cfg.ContextCompactEngine) != "" {
		preferred = o.cfg.ContextCompactEngine
	}
	engine := compact.EngineFor(preferred, body, soft, hard)
	var llmSummarize compact.Summarizer
	if engine == "llm" || engine == "auto" {
		llmSummarize = o.contextSummarizer()
	}
	res := compact.Summarize(ctx, engine, body, compact.CompactTargetBytes(soft, hard), llmSummarize)
	if !res.Compacted {
		return res, nil
	}
	if err := o.snapshotContextBackup(res.Original); err != nil {
		o.emitWarn("learn", "CONTEXT backup failed — not compacting: "+err.Error(), "")
		return compact.Result{Original: res.Original}, err
	}
	if err := o.store.Write(contextstore.DocContext, res.Summary); err != nil {
		return res, err
	}
	if rejected := strings.TrimSpace(string(res.Rejected)); rejected != "" {
		o.emitWarn("learn", "CONTEXT llm summary rejected ("+rejected+") — used heuristic engine", "")
	}
	o.emitFull("learn", stream.KindOutput, "compact", "",
		fmt.Sprintf("CONTEXT compacted %d→%d bytes (engine=%s, backup .slmcode/%s)",
			res.BeforeBytes, res.AfterBytes, engine, contextBackupName),
		"", truncate(res.Summary, 400))
	return res, nil
}

// contextBackupName is the pre-compaction snapshot of CONTEXT.md.
const contextBackupName = "CONTEXT.md.bak"

func (o *Orchestrator) snapshotContextBackup(original string) error {
	if o == nil || o.cfg == nil || strings.TrimSpace(original) == "" {
		return nil
	}
	path := filepath.Join(o.cfg.SlmDir(), contextBackupName)
	return atomicfile.Write(path, []byte(original), 0o600)
}

// maybeCompactContext summarizes CONTEXT.md when it exceeds the pack budget.
func (o *Orchestrator) maybeCompactContext(ctx context.Context) {
	if o == nil || o.cfg == nil || !o.cfg.ContextCompact || o.store == nil {
		return
	}
	body, err := o.store.Read(contextstore.DocContext)
	if err != nil || body == "" {
		return
	}
	soft, hard := o.contextBudgetKB()
	if !compact.NeedsCompact(body, soft, hard) {
		return
	}
	_, _ = o.compactContext(ctx, body)
}

// CompactContextNow forces a CONTEXT.md compaction (TUI /compact context).
func (o *Orchestrator) CompactContextNow() (compact.Result, error) {
	body, err := o.store.Read(contextstore.DocContext)
	if err != nil {
		return compact.Result{}, err
	}
	return o.compactContext(context.Background(), body)
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
	if o.store == nil {
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
	o.emitFullL(phase, kind, agent, taskID, msg, scope, output, stream.LevelInfo)
}

// emitWarn/emitError/emitSuccess/emitProblem surface severity-tagged events so
// the Studio live log can render problems/warnings distinctly from progress.
func (o *Orchestrator) emitWarn(phase, msg, taskID string) {
	o.emitFullL(phase, stream.KindPhase, "", taskID, msg, "", "", stream.LevelWarn)
}

func (o *Orchestrator) emitSuccess(phase, msg, taskID string) {
	o.emitFullL(phase, stream.KindPhase, "", taskID, msg, "", "", stream.LevelSuccess)
}

func (o *Orchestrator) emitProblem(phase, msg, taskID string) {
	o.emitFullL(phase, stream.KindPhase, "", taskID, msg, "", "", stream.LevelProblem)
}

func (o *Orchestrator) emitFullL(phase, kind, agent, taskID, msg, scope, output, level string) {
	o.emitFullDataL(phase, kind, agent, taskID, msg, scope, output, level, nil)
}

func (o *Orchestrator) emitFullDataL(phase, kind, agent, taskID, msg, scope, output, level string, data any) {
	if kind == "" {
		kind = stream.KindPhase
	}
	if level == "" {
		level = stream.LevelInfo
	}
	// The engine never writes to stdout. The CLI is the sole renderer (it owns
	// --log-level, -v/-vv, a sticky footer and an append-only transcript), so
	// the `if o.cfg.Verbose { fmt.Printf(...) }` that used to sit here both
	// double-printed every line and shredded the dashboard. Verbosity is a
	// RENDERER concern now: every event carries a Level and the CLI decides
	// which levels it shows.
	if o.cfg != nil && o.cfg.SessionEventLog && o.currentTurn != nil {
		_ = session.AppendEvent(o.cfg.SlmDir(), o.currentTurn.ID, session.EventRecord{
			Phase: phase, Kind: kind, Agent: agent, TaskID: taskID,
			Message: msg, Scope: scope, Output: stream.Truncate(output, 4000), Data: data,
			Model: o.cfg.Model,
		})
	}
	if sink := o.eventSink(); sink != nil {
		sink(Event{
			Phase: phase, Kind: kind, Level: level, Message: msg, TaskID: taskID,
			Agent: agent, Scope: scope, Output: stream.Truncate(output, 2000),
			Data: data,
			Time: time.Now(),
		})
	}
}

// MCPStatus returns connected MCP servers (nil-safe).
func (o *Orchestrator) MCPStatus() mcp.StatusReport {
	if o == nil || o.mcpMgr == nil {
		return mcp.StatusReport{MetaTool: "mcp_call", Pattern: "single meta-tool mcp_call", Servers: []mcp.ServerStatus{}}
	}
	return o.mcpMgr.Status(o.mcpMgr.LastInfos())
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
	_ = os.MkdirAll(cfg.SkillsDir(), 0o750)
	_ = os.MkdirAll(filepath.Join(cfg.SlmDir(), "errors"), 0o750)
	_ = os.MkdirAll(filepath.Join(cfg.SlmDir(), "archives"), 0o750)
	_ = os.MkdirAll(filepath.Join(cfg.SlmDir(), "queries"), 0o750)
	_ = os.MkdirAll(filepath.Join(cfg.SlmDir(), "summaries"), 0o750)
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
	// Auto-detect and apply language pack based on project files.
	reg, err := blocks.Load(root)
	if err == nil {
		// reg.DetectPack, not a range over reg.Packs: Go randomizes map
		// iteration, so when two packs referenced the same quality block —
		// react and typescript both point at the TS toolchain — which one a
		// fresh workspace got was decided by the runtime, differently on every
		// `slmcode init` of the same directory. DetectPack resolves it in a
		// defined order (the pack whose own id matches the quality id wins,
		// then sorted ids).
		if packID := reg.DetectPack(root); packID != "" {
			if _, err := blocks.ApplyPack(cfg, reg, packID, blocks.ApplyOptions{MaterializeAgents: true}); err == nil {
				lang := packID
				if p, ok := reg.Packs[packID]; ok && strings.TrimSpace(p.Language) != "" {
					lang = p.Language
				}
				fmt.Printf("  ✓ auto-applied %s pack (%s)\n", lang, packID)
			}
		}
	}
	// Empty board.json so Studio/CLI have a writable board (no seeded tasks).
	boardPath := filepath.Join(cfg.SlmDir(), "board.json")
	if _, err := os.Stat(boardPath); os.IsNotExist(err) {
		empty := plan.Board{}
		if data, err := json.MarshalIndent(empty, "", "  "); err == nil {
			_ = atomicfile.Write(boardPath, data, 0o644)
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
	base := formatWorkerPromptFor(t, o.langHint())
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
		opt.Language = detectProjectLang(o.cfg.Root)
		prof := o.resolvedProfileForRole(t.Role)
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
	return o.resolvedProfileForRole("")
}

// resolvedProfileForRole uses the agent's effective model (override ?? global).
func (o *Orchestrator) resolvedProfileForRole(role string) config.ModelProfile {
	if o == nil || o.cfg == nil {
		return config.ResolveModelProfile(nil, "")
	}
	model := o.cfg.Model
	if role != "" && o.factory != nil {
		for _, spec := range o.factory.AllSpecs() {
			if spec.ID == role {
				model = o.factory.EffectiveModel(spec)
				break
			}
		}
	}
	return config.ResolveModelProfile(o.cfg.ModelProfiles, model)
}

// formatWorkerPromptFor renders the task-adjacent worker (or tester) prompt.
//
// It delegates to agents.BuildWorkerPrompt, which is the ONE source of truth
// for the worker contract. The builder this function used to be dropped the
// checklist, the "no extra helper files" rule, the ws_patch re-read/retry rule,
// the language-appropriate ws_shell smoke step and the no-stubs rule — while
// the review gates went on rejecting for exactly those omissions. A 7B–32B
// model weights a task-adjacent restatement far above the same words in a
// system prompt thousands of tokens earlier, so a rule the gate enforces has to
// appear next to the task, every time.
func formatWorkerPromptFor(t plan.Task, langHint string) string {
	// Keep ephemeral scoped packs (injected by BuildInput); only strip when absent.
	desc := t.Description
	if !strings.Contains(desc, "# Scoped context") {
		desc = loop.StripScopedPack(desc)
	}
	return agents.BuildWorkerPrompt(t, agents.WorkerPromptOptions{
		LangHint:    langHint,
		Description: desc,
	})
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

// langHint returns a language-pinned verification hint so tester/worker
// prompts never drift to pytest/python in Go or JS/TS projects (and vice versa).
func (o *Orchestrator) langHint() string {
	root := ""
	if o != nil && o.cfg != nil {
		root = o.cfg.Root
	}
	switch detectProjectLang(root) {
	case "Go":
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return "Project language: Go. Use ONLY: go build ./..., go vet ./..., go test ./... -race -count=1. NEVER run pytest, pip, or python commands."
		}
		return "Project language: Go (single-file, no go.mod). Use gofmt -e FILE for syntax; do NOT run go build/go test without a go.mod."
	case "Python":
		return "Project language: Python. Use: python -m pytest -q (or uv run pytest -q), python -m py_compile. NEVER run go test."
	case "TypeScript", "JavaScript":
		return "Project language: JS/TS. Use: npm test, npx tsc --noEmit, npm run build. NEVER run pytest or go test."
	case "Rust":
		return "Project language: Rust. Use: cargo build --quiet, cargo test --quiet, cargo clippy. NEVER run pytest, go test, or npm test."
	case "Java":
		return "Project language: Java. Use: mvn -q test (or ./gradlew test), mvn -q -DskipTests compile. NEVER run pytest, go test, or npm test."
	case "C++", "C/Make":
		return "Project language: C/C++. Use: cmake --build build (or make), ctest (when present). NEVER run pytest, go test, or npm test."
	case "HTML":
		return "Project language: static web (HTML/CSS/JS, no build step). Verify: a usable index.html exists, asset refs resolve, node --check each .js. NEVER run pytest, go test, or npm test unless package.json exists."
	default:
		return "Use the project's actual language for verification (inspect files first; never assume Python)."
	}
}

// langCache memoises detectProjectLang per root.
//
// The uncached version does up to two full os.ReadDir passes over the project
// root and was called once per worker prompt AND once per review prompt — N
// tasks × 2 syscall storms per wave, for an answer that cannot change during a
// run. Keyed by root so a Studio process serving several projects stays correct.
var langCache sync.Map // root -> string

// detectProjectLang returns a human-readable project language label based on
// config files found at the project root. Used to steer the splitter away from
// hallucinating language-inappropriate files and acceptance commands.
//
// The result is cached for the process lifetime; ResetLangCache clears it when
// a project's shape genuinely changes (e.g. after a greenfield scaffold wrote
// the first go.mod / package.json).
func detectProjectLang(root string) string {
	if root == "" {
		return ""
	}
	if v, ok := langCache.Load(root); ok {
		return v.(string)
	}
	lang := detectProjectLangUncached(root)
	langCache.Store(root, lang)
	return lang
}

// ResetLangCache drops the memoised language for root (all roots when empty).
func ResetLangCache(root string) {
	if root == "" {
		langCache.Range(func(k, _ any) bool {
			langCache.Delete(k)
			return true
		})
		return
	}
	langCache.Delete(root)
}

// detectProjectLangUncached names the project language.
//
// It is a THIN wrapper over pkg/blocks, which is where the language markers
// live, because that is where the packs that consume them are defined. The
// previous version was an independent marker list — the fourth copy in the
// tree — and it disagreed with the packs: it returned "" for Kotlin, Ruby, PHP,
// Swift and .NET (so the evolve bandit keyed every one of those runs as
// `…|*` and lost all cross-run learning for them) and "C/Make" for a CMake
// project, a label no pack answers to.
//
// Two disambiguations stay here because the block registry genuinely cannot
// make them:
//
//   - Gradle. The kotlin (priority 16) and java (15) packs claim the same
//     build files, so a Java project with build.gradle.kts scores as Kotlin.
//     The source layout is the only unambiguous signal and it is not a marker.
//   - tsconfig.json. One pack ("typescript") covers both TS and JS, and the
//     orchestrator's specialist routing splits them (ts-worker vs web-worker).
func detectProjectLangUncached(root string) string {
	if root == "" {
		return ""
	}
	if isGradleProject(root) {
		return gradleLang(root)
	}
	switch blocks.DetectPack(root, root) {
	case "go":
		return "Go"
	case "python":
		return "Python"
	case "typescript", "react":
		// The typescript pack claims a bare package.json too; tsconfig.json is
		// what makes it TypeScript rather than JavaScript.
		if _, err := os.Stat(filepath.Join(root, "tsconfig.json")); err == nil {
			return "TypeScript"
		}
		return "JavaScript"
	case "web":
		return "HTML"
	case "rust":
		return "Rust"
	case "java":
		return "Java"
	case "kotlin":
		return "Kotlin"
	case "dotnet":
		return "C#"
	case "ruby":
		return "Ruby"
	case "php":
		return "PHP"
	case "swift":
		return "Swift"
	case "cpp":
		return "C++"
	}
	return ""
}

// isGradleProject reports whether the root carries a Gradle build description.
func isGradleProject(root string) bool {
	for _, f := range []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			return true
		}
	}
	return false
}

// gradleLang disambiguates a Gradle project. The .kts build DSL is used by Java
// projects too, so the source layout decides: src/main/kotlin is Kotlin's
// standard and unambiguous.
func gradleLang(root string) string {
	if st, err := os.Stat(filepath.Join(root, "src", "main", "kotlin")); err == nil && st.IsDir() {
		return "Kotlin"
	}
	return "Java"
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

func (o *Orchestrator) buildPlannerPrompt(query, runID, planAgent, exploreOut, archOut string, prd plan.ScopePRD, clarify plan.ClarifyResult, replanNotes []string) string {
	planDocs := contextstore.LeanDocsForRole("planner")
	planPack, _ := o.packBuild(planAgent, query, planDocs, nil, o.skillPackFor(planAgent, query))
	exploreCap := 2500
	if o.cfg.ThinkPasses >= 3 {
		exploreCap = 4000
	}
	prompt := planPack.Render() + "\nExploration:\n" + truncate(exploreOut, exploreCap)
	if archOut != "" {
		prompt += "\n\nArchitecture:\n" + truncate(archOut, 1500)
	}
	if prd.Summary != "" || len(prd.Acceptance) > 0 || len(clarify.Assumptions) > 0 ||
		clarify.Language != "" || clarify.Entrypoint != "" {
		prompt += "\n\n" + plan.FormatPRDMarkdown(prd, clarify.Assumptions)
		prompt += "\nTreat Locked PRD as hard requirements unless contradicted by the query.\n"
	}
	prompt += projectLanguageGuidance(o.cfg.Root)
	if block := replanInstructionBlock(replanNotes, "Revise the previous approach accordingly. Make the plan smaller, safer, and easier for SLM specialists to execute."); block != "" {
		prompt += block
	}
	prompt += "\n\nIMPORTANT: Brand-new plan for THIS query only (query_id=" + runID + "). " +
		"Do NOT continue prior plans. STRICT JSON plan only."
	return prompt
}

func (o *Orchestrator) buildSplitterPrompt(query, splitAgent, planOut string, prd plan.ScopePRD, clarify plan.ClarifyResult, replanNotes []string) string {
	splitDocs := contextstore.LeanDocsForRole("splitter")
	splitPack, _ := o.packBuild(splitAgent, query, splitDocs, nil, o.skillPackFor(splitAgent, query))
	prompt := splitPack.Render() + "\nPlan:\n" + truncate(planOut, 3500)
	if prd.Summary != "" || len(prd.Acceptance) > 0 || clarify.Language != "" {
		prompt += "\n\n" + plan.FormatPRDMarkdown(prd, clarify.Assumptions)
		prompt += "\nEvery task must inherit Locked PRD acceptance/constraints.\n"
	}
	if block := replanInstructionBlock(replanNotes, "Reflect these instructions in task scope, file focus, dependencies, and acceptance criteria."); block != "" {
		prompt += block
	}
	prompt += projectLanguageGuidance(o.cfg.Root)
	prompt += "\n\nFresh task list for THIS query. STRICT JSON tasks."
	if o.cfg.ThinkPasses >= 2 {
		prompt += "\nPrefer <=5 tiny tasks with files + acceptance. Tester when code changes."
	}
	return prompt
}

func projectLanguageGuidance(root string) string {
	if lang := detectProjectLang(root); lang != "" {
		out := fmt.Sprintf("\n\nProject language: %s.", lang)
		switch lang {
		case "Go":
			out += " Go modules: main package lives in root (main.go), library packages in subdirs (e.g. pkg/calc/calc.go). Acceptance: go build, go test ./..., go vet. NEVER use pytest/go run for non-main packages."
		case "Python":
			out += " Python: src/ or flat layout. Acceptance: pytest -q, python -m py_compile, python main.py. NEVER use go test."
		case "TypeScript", "JavaScript":
			out += " JS/TS: src/ or app/. Acceptance: npm test, npx tsc --noEmit, npm run build. NEVER use pytest or go test."
		case "Rust":
			out += " Rust: src/ + Cargo.toml. Acceptance: cargo build --quiet, cargo test --quiet. NEVER use pytest/go test/npm test."
		case "Java":
			out += " Java: src/main/java + pom.xml (or build.gradle). Acceptance: mvn -q test / ./gradlew test, mvn -q -DskipTests compile. NEVER use pytest/go test/npm test."
		case "C++", "C/Make":
			out += " C/C++: CMakeLists.txt (or Makefile). Acceptance: cmake --build build / make, ctest when present. NEVER use pytest/go test/npm test."
		case "HTML":
			out += " Static web (vanilla HTML/CSS/JS): produce an index.html entrypoint plus style.css/script.js (or one self-contained index.html). Acceptance: index.html exists & is usable in a browser; asset refs resolve; node --check each .js. NEVER use pytest, go test, or npm test (no package.json)."
		default:
			out += " Use language-appropriate acceptance criteria and real project file paths."
		}
		return out + " Do NOT invent files that don't exist in the workspace inventory."
	}
	return "\n\nUse language-appropriate acceptance criteria based on the project's actual files. " +
		"Do NOT invent files that don't exist in the workspace inventory."
}

func replanInstructionBlock(notes []string, instruction string) string {
	body := formatReplanNotes(notes)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return "\n\n## User replan instructions\n\n" + body + "\n" + instruction + "\n"
}

func formatReplanNotes(notes []string) string {
	var b strings.Builder
	for _, note := range notes {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(note)
		b.WriteByte('\n')
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}
