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

	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
	"github.com/piotrlaczkowski/slmcode/pkg/agents"
	"github.com/piotrlaczkowski/slmcode/pkg/backends"
	"github.com/piotrlaczkowski/slmcode/pkg/config"
	contextstore "github.com/piotrlaczkowski/slmcode/pkg/context"
	"github.com/piotrlaczkowski/slmcode/pkg/instructions"
	"github.com/piotrlaczkowski/slmcode/pkg/knowledge"
	"github.com/piotrlaczkowski/slmcode/pkg/learning"
	"github.com/piotrlaczkowski/slmcode/pkg/loop"
	"github.com/piotrlaczkowski/slmcode/pkg/multipass"
	"github.com/piotrlaczkowski/slmcode/pkg/plan"
	"github.com/piotrlaczkowski/slmcode/pkg/session"
	"github.com/piotrlaczkowski/slmcode/pkg/skills"
	"github.com/piotrlaczkowski/slmcode/pkg/stream"
	"github.com/piotrlaczkowski/slmcode/pkg/workspace"
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
}

type Orchestrator struct {
	cfg        *config.Config
	store      *contextstore.Store
	boardStore *plan.LiveStore
	packer     *contextstore.Packer
	skills     *skills.Loader
	llm        *llm.ProviderManager
	tools      *tools.ToolRegistry
	factory    *agents.Factory
	registry   *ggagent.AgentRegistry
	executor   *ggagent.SubAgentExecutor
	shared     *ggagent.SharedState
	think      *multipass.Runner
	claude     *backends.ClaudeCodeRunner
	onEvent    EventHandler

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
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
	skillRoots = append(skillRoots, cfg.SkillsDirs...)
	loader := skills.NewLoader(skillRoots...)

	llmManager := llm.NewProviderManager()
	if err := backends.RegisterLLM(llmManager, cfg); err != nil {
		return nil, err
	}

	toolReg := tools.NewToolRegistry()
	if err := workspace.RegisterCodingToolsOpts(toolReg, cfg.Root, workspace.ToolOpts{
		DryRun: cfg.DryRun, Permission: cfg.Permission, SlmDir: cfg.SlmDir(),
	}); err != nil {
		return nil, err
	}

	// AgentConfig Provider must match registered name
	providerName := cfg.Provider
	if providerName == "mlx" {
		providerName = "omlx"
	}
	factory := agents.NewFactory(llmManager, toolReg, cfg.Model, providerName)
	// OpenAI-compat providers are registered as omlx/openai — ensure agent provider matches
	if providerName == "omlx" || providerName == "openai" {
		factory = agents.NewFactory(llmManager, toolReg, cfg.Model, providerName)
	}

	registry, err := factory.BuildRegistry()
	if err != nil {
		return nil, err
	}
	exec := ggagent.NewSubAgentExecutor(registry)
	exec.SetParallel(true)
	exec.SetTimeout(cfg.TaskTimeout)

	o := &Orchestrator{
		cfg:        cfg,
		store:      store,
		boardStore: boardStore,
		packer:     packer,
		skills:     loader,
		llm:        llmManager,
		tools:      toolReg,
		factory:    factory,
		registry:   registry,
		executor:   exec,
		shared:     ggagent.NewSharedState(),
		think:      multipass.New(cfg.ThinkPasses),
		claude:     backends.NewClaudeCodeRunner(cfg),
		onEvent:    func(Event) {},
	}
	boardStore.OnChange(func(b *plan.Board) {
		p, t := b.ToMarkdown()
		_ = store.Write(contextstore.DocPlan, p)
		_ = store.Write(contextstore.DocTasks, t)
	})
	return o, nil
}

func (o *Orchestrator) OnEvent(h EventHandler)          { o.onEvent = h }
func (o *Orchestrator) Store() *contextstore.Store      { return o.store }
func (o *Orchestrator) Board() *plan.LiveStore          { return o.boardStore }
func (o *Orchestrator) Skills() *skills.Loader          { return o.skills }
func (o *Orchestrator) Config() *config.Config          { return o.cfg }
func (o *Orchestrator) Packer() *contextstore.Packer    { return o.packer }

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

	o.emit("init", "starting "+runID, "")
	_ = o.store.SetQuery(query)
	o.shared.SetGlobal("query", query)
	o.shared.SetGlobal("root", o.cfg.Root)
	o.shared.SetGlobal("mode", o.cfg.Mode)
	if o.cfg.Specialist != "" {
		o.shared.SetGlobal("specialist", o.cfg.Specialist)
	}

	roleHint := ""
	if o.cfg.Mode == config.ModeSpecialist {
		roleHint = o.cfg.Specialist
	}
	matched := o.skills.ResolveForRun(query, roleHint, o.cfg.PinnedSkills, 6)
	skillPack := skills.RenderPack(matched, 3500)
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
	list := o.skills.ResolveForRun(query, role, o.cfg.PinnedSkills, 6)
	return skills.RenderPack(list, 3000)
}

func (o *Orchestrator) runSpecialist(ctx context.Context, runID, query string, start time.Time) (*Result, error) {
	role := strings.TrimSpace(o.cfg.Specialist)
	if role == "" {
		role = plan.RoleWorker
	}
	// Validate role exists
	valid := false
	for _, s := range agents.Specs() {
		if s.ID == role {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("unknown specialist %q — use: slmcode agents", role)
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
		Plan: plan.Plan{Summary: "Specialist: " + role, Steps: []string{query}},
		Tasks: []plan.Task{{
			ID: "S1", Title: "Specialist " + role, Description: query,
			Role: role, Status: status, Output: out, Error: errStr, Files: discovered,
		}},
	}
	o.persistBoard(board)
	if strings.TrimSpace(out) != "" {
		_ = o.store.Append(contextstore.DocScratch, "Specialist "+role, out)
	}
	res := &Result{
		ID: runID, Query: query, Board: *board,
		Success: err == nil, FailedTasks: board.FailedCount(),
		Duration: time.Since(start), Summary: firstSentence(out),
		Backend: config.BackendSLMCode,
	}
	if res.Summary == "" {
		res.Summary = fmt.Sprintf("specialist %s finished", role)
	}
	o.emit("done", res.Summary, "")
	return res, err
}

func (o *Orchestrator) runSLM(ctx context.Context, runID, query, skillPack string, start time.Time) (*Result, error) {
	// 0 Auto-load AGENTS.md / CLAUDE.md / PROJECT instructions (Claude Code style)
	if instr := instructions.LoadProjectInstructions(o.cfg.Root); instr != "" {
		o.emit("init", "loaded project instructions (AGENTS.md/CLAUDE.md/PROJECT)", "")
		_ = o.store.Append(contextstore.DocScratch, "Project instructions", truncate(instr, 8000))
		o.shared.SetGlobal("project_instructions", instr)
		skillPack = skillPack + "\n\n## Project instructions\n\n" + truncate(instr, 6000)
	}

	// 0b Authoritative workspace inventory — stops SLMs inventing internal/... paths
	inventory := plan.ListWorkspaceFiles(o.cfg.Root, 48)
	discoveredEarly := plan.DiscoverRelevantFiles(o.cfg.Root, query, "")
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

	// 1 Context — shared memory for all specialists
	o.emitAgent("context", plan.RoleContext, "", "updating working context", "", "")
	pack, _ := o.packer.Build("context", query, contextstore.DefaultDocsForRole("context"), discoveredEarly, o.skillPackFor("context", query))
	ctxOut, err := o.runRoleTracked(ctx, plan.RoleContext, "", pack.Render()+
		"\nRewrite CONTEXT.md for this query (markdown). ONLY reference files from the authoritative workspace list. Include: Active focus, Recent findings, Open questions.")
	if err != nil {
		o.emit("context", "warning: "+err.Error(), "")
	}
	if strings.TrimSpace(ctxOut) != "" {
		_ = o.store.Write(contextstore.DocContext, ensureHeading(ctxOut, "# Working Context"))
	} else if len(inventory) > 0 {
		// Real inventory-backed stub only when the agent produced nothing — not a welcome seed.
		_ = o.store.Write(contextstore.DocContext, "# Working Context\n\n## Active focus\n\n"+query+
			"\n\n## Recent findings\n\n(awaiting explorer)\n\n## Workspace inventory\n\n- "+
			strings.Join(inventory, "\n- ")+"\n")
	}
	if len(inventory) > 0 {
		_ = o.store.Append(contextstore.DocContext, "Workspace inventory",
			"- "+strings.Join(inventory, "\n- "))
	}

	// 2 Explore — skip deep dive when CONTEXT + FS discovery already enough
	var exploreOut string
	deep, reason := o.shouldDeepExplore(query)
	if !deep {
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
		o.emitAgent("explore", plan.RoleExplorer, "", "codebase deep-dive", "", "")
		expPack, _ := o.packer.Build("explorer", query, contextstore.DefaultDocsForRole("explorer"), nil, o.skillPackFor("explorer", query))
		exploreOut, err = o.runRoleTracked(ctx, plan.RoleExplorer, "", expPack.Render()+"\nExplore for this query. Return JSON.")
		if err != nil {
			return nil, fmt.Errorf("explorer: %w", err)
		}
		_ = o.store.Write(contextstore.DocScratch, "# Exploration\n\n"+exploreOut)
		o.shared.SetGlobal("exploration", exploreOut)
		o.shared.SetGlobal("explore_mode", "deep")

		// Docs explorer when query smells like docs/API/README work
		if wantsDocsExplorer(query) {
			o.emitAgent("docs", "docs", "", "documentation explorer", "docs/, README*", "")
			docsPack, _ := o.packer.Build("docs", query, []string{contextstore.DocProject, contextstore.DocContext}, nil, o.skillPackFor("docs", query))
			docsOut, _ := o.runRoleTracked(ctx, "docs", "", docsPack.Render()+"\nMap docs/conventions for this query. Return JSON.")
			if strings.TrimSpace(docsOut) != "" {
				_ = o.store.Append(contextstore.DocScratch, "Docs exploration", docsOut)
				exploreOut += "\n\n" + docsOut
				o.shared.SetGlobal("docs_exploration", docsOut)
			}
		}
	}

	// 2b Architect pass for larger / design-ish queries
	var archOut string
	if wantsArchitect(query) {
		o.emitAgent("architect", "architect", "", "minimal design pass", "", "")
		archPack, _ := o.packer.Build("architect", query, contextstore.DefaultDocsForRole("planner"), nil, o.skillPackFor("architect", query))
		archOut, _ = o.runRoleTracked(ctx, "architect", "", archPack.Render()+"\nExploration:\n"+truncate(exploreOut, 4000)+"\nReturn STRICT JSON design.")
		if strings.TrimSpace(archOut) != "" {
			_ = o.store.Append(contextstore.DocScratch, "Architecture", archOut)
			o.shared.SetGlobal("architecture", archOut)
		}
	}

	// 3 Plan (multipass)
	o.emitAgent("plan", plan.RolePlanner, "", "creating plan (multi-pass)", "", "")
	planPack, _ := o.packer.Build("planner", query, contextstore.DefaultDocsForRole("planner"), nil, o.skillPackFor("planner", query))
	planPrompt := planPack.Render() + "\nExploration:\n" + truncate(exploreOut, 5000)
	if archOut != "" {
		planPrompt += "\n\nArchitecture:\n" + truncate(archOut, 2500)
	}
	planPrompt += "\n\nReturn STRICT JSON plan."
	planOut, err := o.runRoleMultipassTracked(ctx, plan.RolePlanner, "", planPrompt)
	if err != nil {
		return nil, fmt.Errorf("planner: %w", err)
	}
	pl, _ := plan.ParsePlanJSON(planOut)
	if strings.TrimSpace(pl.Summary) == "" {
		pl.Summary = firstSentence(planOut)
	}
	// Persist agent plan immediately so Studio PLAN.md / board update live mid-run.
	o.persistBoard(&plan.Board{Plan: pl, Tasks: nil})
	o.emit("plan", "PLAN.md updated from planner", "")

	// 4 Split
	o.emitAgent("split", "splitter", "", "atomic task split", "", "")
	splitPack, _ := o.packer.Build("splitter", query, contextstore.DefaultDocsForRole("splitter"), nil, o.skillPackFor("splitter", query))
	tasksOut, err := o.runRoleMultipassTracked(ctx, "splitter", "", splitPack.Render()+"\nPlan:\n"+planOut+"\n\nReturn STRICT JSON tasks.")
	if err != nil {
		return nil, fmt.Errorf("splitter: %w", err)
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
	if len(discovered) > 0 {
		_ = o.store.Append(contextstore.DocContext, "Discovered files", "- "+strings.Join(discovered, "\n- "))
	}
	for i := range tasks {
		tasks[i].Files = plan.ReconcileFiles(o.cfg.Root, tasks[i].Files, discovered)
	}

	board := &plan.Board{Plan: pl, Tasks: tasks}
	for i := range board.Tasks {
		t := board.Tasks[i]
		if t.Column == "" {
			t.Column = plan.ColReadyToDev
		}
		t.Normalize()
		board.Tasks[i] = t
	}
	o.persistBoard(board)
	o.emit("split", fmt.Sprintf("TASKS.md + board: %d agent tasks", len(board.Tasks)), "")

	// 4b Coordinator reviews the board before execute
	o.coordinate(ctx, query, board, "pre-execute")

	// 5 Execute + review/correct (live board — human can edit/add mid-run)
	o.emit("execute", fmt.Sprintf("%d tasks · parallel=%d · think_passes=%d", len(board.Tasks), o.cfg.MaxParallel, o.cfg.ThinkPasses), "")
	runner := loop.NewRunner(o.executor, o.shared)
	runner.Root = o.cfg.Root
	runner.Store = o.boardStore
	runner.MaxRetries = o.cfg.MaxRetries
	runner.MaxParallel = o.cfg.MaxParallel
	runner.Timeout = o.cfg.TaskTimeout
	runner.Log = func(format string, args ...interface{}) {
		o.emit("execute", fmt.Sprintf(format, args...), "")
	}
	runner.OnEvent = func(kind, agent, taskID, msg, scope, output string) {
		o.emitFull("execute", kind, agent, taskID, msg, scope, output)
	}
	runner.AfterWave = func(ctx context.Context, board *plan.Board, wave []plan.Task) {
		o.evolveAfterWave(ctx, query, skillPack, board, wave)
		o.coordinate(ctx, query, board, "after-wave")
	}
	// Enrich descriptions with scoped packs once
	snap := o.boardStore.Snapshot()
	board = &snap
	for i := range board.Tasks {
		t := board.Tasks[i]
		tp, _ := o.packer.Build(t.Role, query, contextstore.DefaultDocsForRole(t.Role), t.Files, o.skillPackFor(t.Role, query))
		tp.TaskID = t.ID
		tp.TaskTitle = t.Title
		if !strings.Contains(t.Description, "# Scoped context") {
			t.Description = tp.Render() + "\n## Task instructions\n\n" + t.Description
		}
		board.Tasks[i] = t
	}
	o.persistBoard(board)

	if err := runner.RunBoard(ctx, board); err != nil {
		return nil, err
	}
	snap = o.boardStore.Snapshot()
	board = &snap
	o.persistBoard(board)

	// 6 Test / validation
	o.emitAgent("test", plan.RoleTester, "", "verification pass", "", "")
	_, tasksMD := board.ToMarkdown()
	testPack, _ := o.packer.Build("tester", query, contextstore.DefaultDocsForRole("tester"), nil, o.skillPackFor("tester", query))
	testOut, _ := o.runRoleTracked(ctx, plan.RoleTester, "", testPack.Render()+"\nTasks:\n"+truncate(tasksMD, 4000))
	if strings.TrimSpace(testOut) != "" {
		_ = o.store.Append(contextstore.DocScratch, "Verification", testOut)
		o.emitFull("test", stream.KindOutput, plan.RoleTester, "", "tester output", "", truncate(testOut, 1200))
	}

	// 7 Memory — consolidate wave lessons + SLM distillation
	o.emitAgent("memory", "memory", "", "distilling long-term memory", "", "")
	var allLessons []learning.Lesson
	for _, t := range board.Tasks {
		allLessons = append(allLessons, learning.Extract(t)...)
	}
	lessonsMD := learning.RenderMarkdown(allLessons)
	if lessonsMD != "" {
		_ = o.store.Append(contextstore.DocMemory, "Auto-lessons", lessonsMD)
	}
	memPack, _ := o.packer.Build("memory", query, contextstore.DefaultDocsForRole("memory"), nil, o.skillPackFor("memory", query))
	memOut, _ := o.runRoleMultipassTracked(ctx, "memory", "", memPack.Render()+fmt.Sprintf(
		"\nFailed: %d\nWrite ≤8 durable bullets under ## Lessons (conventions, pitfalls, paths).", board.FailedCount()))
	if strings.TrimSpace(memOut) != "" {
		_ = o.store.Append(contextstore.DocMemory, "Session distillation", memOut)
		lessonsMD = strings.TrimSpace(lessonsMD + "\n" + memOut)
	}

	// 8 Auto-evolve SKILLS.md + learned skill (iterative project knowledge)
	o.emit("skills", "evolving SKILLS.md + learned skill", "")
	skillList, _ := o.skills.List()
	if ev, err := knowledge.Evolve(o.cfg.SlmDir(), query, board, lessonsMD, skillList); err == nil && ev != nil {
		o.emitFull("skills", stream.KindLearn, "memory", "",
			fmt.Sprintf("updated %s + %s", ev.SkillsIndex, ev.LearnedSkill), "", "")
		// Reload skills so next packs see learned/
		o.skills = skills.NewLoader(append([]string{
			filepath.Join(o.cfg.SlmDir(), "skills"),
			filepath.Join(o.cfg.SlmDir(), "skills", "_bundled"),
		}, o.cfg.SkillsDirs...)...)
	}

	_ = o.store.Append(contextstore.DocContext, "Run complete", summarize(board, pl))

	failed := board.FailedCount()
	res := &Result{
		ID: runID, Query: query, Board: *board,
		Success: failed == 0 && board.AllDone(), FailedTasks: failed,
		Duration: time.Since(start), Summary: summarize(board, pl),
		Backend: config.BackendSLMCode,
	}
	if path, err := session.Save(o.cfg.SlmDir(), session.Session{
		ID: runID, Query: query, Summary: res.Summary, Success: res.Success, Board: *board,
	}); err == nil {
		o.emit("session", "saved "+filepath.Base(path), "")
	}
	o.emit("done", res.Summary, "")
	return res, nil
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
	o.persistBoard(board)
	_ = o.store.Append(contextstore.DocScratch, "Claude Code", out)
	res := &Result{
		ID: runID, Query: query, Board: *board,
		Success: err == nil, FailedTasks: board.FailedCount(),
		Duration: time.Since(start), Summary: firstSentence(out),
		Backend: config.BackendClaudeCode,
	}
	o.emit("done", res.Summary, "")
	return res, err
}

func (o *Orchestrator) runRole(ctx context.Context, role, input string) (string, error) {
	return o.runRoleTracked(ctx, role, "", input)
}

func (o *Orchestrator) runRoleTracked(ctx context.Context, role, taskID, input string) (string, error) {
	o.emitFull(role, stream.KindAgentStart, role, taskID, "started", scopeFromInput(input), "")
	results, err := o.executor.ExecuteSubAgents(ctx, []ggagent.SubAgentRequest{{
		AgentID: role, Input: input, Timeout: o.cfg.TaskTimeout, ShareState: true,
	}}, o.shared)
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
	if results[0].Error != nil && out == "" {
		o.emitFull(role, stream.KindAgentEnd, role, taskID, "error: "+results[0].Error.Error(), "", "")
		return "", results[0].Error
	}
	o.emitFull(role, stream.KindAgentEnd, role, taskID, "finished", scopeFromInput(input), truncate(out, 1500))
	return out, nil
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
	ctxDoc, _ := o.store.Read(contextstore.DocContext)
	memDoc, _ := o.store.Read(contextstore.DocMemory)
	projDoc, _ := o.store.Read(contextstore.DocProject)
	discovered := plan.DiscoverRelevantFiles(o.cfg.Root, query, ctxDoc+"\n"+memDoc)
	rich := len(ctxDoc) > 500 && (strings.Contains(ctxDoc, "Discovered files") || strings.Contains(ctxDoc, "Wave"))
	hasFiles := len(discovered) > 0
	hasMemory := len(memDoc) > 200 || len(projDoc) > 200
	if rich && hasFiles && hasMemory && !wantsForceExplore(query) {
		return false, fmt.Sprintf("reusing CONTEXT/MEMORY + %d known file(s) — skip deep explore", len(discovered))
	}
	return true, ""
}

func (o *Orchestrator) coordinate(ctx context.Context, query string, board *plan.Board, when string) {
	if board == nil {
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
		Actions     []action `json:"actions"`
		FocusFiles  []string `json:"focus_files"`
		Summary     string   `json:"summary"`
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
			id := board.NextID()
			nt := plan.Task{
				ID: id, Title: firstSentence(a.Text), Description: a.Text,
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

	// Refresh scoped packs for remaining ready/pending work with latest CONTEXT/MEMORY
	for i := range board.Tasks {
		t := board.Tasks[i]
		t.Normalize()
		if t.Column != plan.ColReadyToDev && t.Column != plan.ColToScope && t.Column != plan.ColScoped {
			continue
		}
		// Strip previous scoped header if present
		desc := t.Description
		if idx := strings.Index(desc, "## Task instructions"); idx >= 0 {
			desc = strings.TrimSpace(desc[idx+len("## Task instructions"):])
		}
		tp, _ := o.packer.Build(t.Role, query, contextstore.DefaultDocsForRole(t.Role), t.Files, o.skillPackFor(t.Role, query))
		tp.TaskID = t.ID
		tp.TaskTitle = t.Title
		t.Description = tp.Render() + "\n## Task instructions\n\n" + desc
		board.Tasks[i] = t
	}
}

func (o *Orchestrator) persistBoard(board *plan.Board) {
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
	// Materialize bundled skills only — CONTEXT/PLAN/TASKS stay empty until agents write them.
	_ = skills.MaterializeBundled(filepath.Join(cfg.SlmDir(), "skills", "_bundled"))
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}
