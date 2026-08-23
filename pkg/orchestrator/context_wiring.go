package orchestrator

import (
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/config"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/repomap"
	"github.com/UnicoLab/slmcode/pkg/skills"
)

// Context-engineering configuration reaches the packer HERE.
//
// pkg/config grew repo_map_tokens, excerpt_window_lines, the three
// context_reserve_* budgets, context_slack_percent and context_role_budget,
// and every one of them mapped onto an existing contextstore Option — but the
// only constructor call in the harness passed WithRepoMap and nothing else, so
// setting any of them in config.yaml changed nothing at all. The functions
// below are the single place the config → packer translation happens; New and
// rebuildPacker both go through them so a run cannot silently drop the user's
// budget after a model re-point.

// packBudgetFor builds the token budget from config. Reserves and slack come
// straight off the config fields (already clamped by Config.Normalize), and a
// zero keeps the packer's own default.
func packBudgetFor(cfg *config.Config, contextLimitTokens int) contextstore.Budget {
	b := contextstore.DefaultBudget(contextLimitTokens)
	if cfg == nil {
		return b
	}
	if n := cfg.ContextReserveSystemTokens; n > 0 {
		b.ReserveSystemTokens = n
	}
	if n := cfg.ContextReserveToolTokens; n > 0 {
		b.ReserveToolTokens = n
	}
	if n := cfg.ContextReserveResponseTokens; n > 0 {
		b.ReserveResponseTokens = n
	}
	if n := cfg.ContextSlackPercent; n > 0 && n < 100 {
		b.SlackPercent = n
	}
	return b
}

// packerOptionsFor is the full option set for one packer.
func packerOptionsFor(cfg *config.Config, rm *repomap.Map, contextLimitTokens int) []contextstore.Option {
	opts := []contextstore.Option{
		// WithBudget first (it carries context_slack_percent, which has no
		// dedicated option), then WithReserves so the three context_reserve_*
		// fields map onto the API that exists for them. The two agree by
		// construction — packBudgetFor reads the same fields.
		contextstore.WithBudget(packBudgetFor(cfg, contextLimitTokens)),
		contextstore.WithRepoMap(rm),
	}
	if cfg == nil {
		return opts
	}
	opts = append(opts, contextstore.WithReserves(
		cfg.ContextReserveSystemTokens,
		cfg.ContextReserveToolTokens,
		cfg.ContextReserveResponseTokens,
	))
	// repo_map_tokens: 0 is meaningful (it disables the map), so this is not
	// guarded on > 0. Config.Normalize floors it at 0.
	opts = append(opts, contextstore.WithRepoMapTokens(cfg.RepoMapTokens))
	if n := cfg.ExcerptWindowLines; n > 0 {
		opts = append(opts, contextstore.WithExcerptOptions(contextstore.ExcerptOptions{Window: n}))
	}
	return opts
}

// roleScaledBudget re-expresses "role r may use want% of the window" in terms
// contextstore.Budget can carry.
//
// Budget.Available multiplies by the package-level RoleBudgetPercent table,
// which has no per-instance override. The reserves are subtracted BEFORE that
// multiplication, so scaling the window above the reserves by want/default
// yields exactly the requested share:
//
//	Available = (W - R) * (100-slack)/100 * pct/100
//
// Substituting W' = R + (W-R)*want/def makes the pct/100 factor land on
// want instead of def, and leaves every other term untouched.
func roleScaledBudget(base contextstore.Budget, role string, want int) (contextstore.Budget, bool) {
	def := contextstore.RoleBudgetPercent(role)
	if want <= 0 || def <= 0 || want == def {
		return base, false
	}
	window := base.ContextLimitTokens
	if window <= 0 {
		window = contextstore.TokensFromKB(contextstore.DefaultMaxContextKB)
	}
	reserved := base.ReserveSystemTokens + base.ReserveToolTokens + base.ReserveResponseTokens
	if window-reserved <= 0 {
		return base, false
	}
	out := base
	out.ContextLimitTokens = reserved + (window-reserved)*want/def
	return out, true
}

// buildPackers constructs the shared packer plus one packer per role that
// carries an explicit context_role_budget override.
func (o *Orchestrator) buildPackers(rm *repomap.Map, contextLimitTokens int) {
	if o == nil || o.store == nil {
		return
	}
	cfg := o.cfg
	root := ""
	if cfg != nil {
		root = cfg.Root
	}
	base := packerOptionsFor(cfg, rm, contextLimitTokens)
	o.packer = contextstore.NewPackerWithBudget(o.store, root, contextLimitTokens, base...)

	o.rolePackers = nil
	if cfg == nil || len(cfg.ContextRoleBudget) == 0 {
		return
	}
	budget := packBudgetFor(cfg, contextLimitTokens)
	scoped := map[string]*contextstore.Packer{}
	for role, pct := range cfg.ContextRoleBudget {
		key := strings.ToLower(strings.TrimSpace(role))
		if key == "" {
			continue
		}
		scaled, ok := roleScaledBudget(budget, key, pct)
		if !ok {
			continue
		}
		opts := append([]contextstore.Option{}, base...)
		opts = append(opts, contextstore.WithBudget(scaled))
		scoped[key] = contextstore.NewPackerWithBudget(o.store, root, scaled.ContextLimitTokens, opts...)
	}
	if len(scoped) > 0 {
		o.rolePackers = scoped
	}
}

// rebuildPacker re-derives the packer for a new context window.
//
// Packer.SetContextLimitTokens resets the budget to contextstore's DEFAULT
// reserves, which would silently discard every context_reserve_* and
// context_slack_percent the operator configured. Rebuilding keeps them.
func (o *Orchestrator) rebuildPacker(contextLimitTokens int) {
	if o == nil || o.packer == nil {
		return
	}
	o.buildPackers(o.repoMapNow(), contextLimitTokens)
}

// packerFor returns the packer that owns role's budget.
func (o *Orchestrator) packerFor(role string) *contextstore.Packer {
	if o == nil {
		return nil
	}
	if len(o.rolePackers) > 0 {
		if p := o.rolePackers[strings.ToLower(strings.TrimSpace(role))]; p != nil {
			return p
		}
	}
	return o.packer
}

// allPackers is every live packer, shared one first.
func (o *Orchestrator) allPackers() []*contextstore.Packer {
	if o == nil || o.packer == nil {
		return nil
	}
	out := []*contextstore.Packer{o.packer}
	for _, p := range o.rolePackers {
		if p != nil {
			out = append(out, p)
		}
	}
	return out
}

// clearPackCaches drops reused packs on every packer.
func (o *Orchestrator) clearPackCaches() {
	for _, p := range o.allPackers() {
		p.ClearCache()
	}
}

// setPackerRepoMap attaches a refreshed repo map to every packer.
func (o *Orchestrator) setPackerRepoMap(rm *repomap.Map) {
	for _, p := range o.allPackers() {
		p.SetRepoMap(rm)
	}
}

// packBuild is the historical Build signature, routed to the role's packer.
func (o *Orchestrator) packBuild(role, query string, docNames, filePaths []string,
	skillsMarkdown string) (*contextstore.TaskPack, error) {
	return o.packerFor(role).Build(role, query, docNames, filePaths, skillsMarkdown)
}

// packBuildReq is BuildPack routed to the role's packer.
func (o *Orchestrator) packBuildReq(req contextstore.BuildRequest) (*contextstore.TaskPack, error) {
	return o.packerFor(req.Role).BuildPack(req)
}

// skillPackOptions maps skill_disclosure / skill_max_expanded onto the
// progressive-disclosure renderer.
//
// skillPackFor used to call skills.RenderPack, the pre-disclosure renderer that
// inlines every matched body — so `skill_disclosure: cards` and
// `skill_max_expanded` had no effect on the packs production actually built.
func skillPackOptions(cfg *config.Config, maxChars int) skills.PackOptions {
	opts := skills.PackOptions{MaxChars: maxChars}
	if cfg == nil {
		return opts
	}
	switch config.NormalizeSkillDisclosure(cfg.SkillDisclosure) {
	case config.SkillDisclosureCards:
		opts.CardsOnly = true
	case config.SkillDisclosureFull:
		opts.ExpandAll = true
	}
	if n := cfg.SkillMaxExpanded; n > 0 {
		opts.MaxExpanded = n
	}
	return opts
}

// noteChangedFiles records the paths a run has modified.
//
// The QA gate's formatting pass needs the wave's changed files (formatting the
// whole project root is the regression pkg/quality removed), and the tool layer
// is the only component that sees every write, edit and patch.
func (o *Orchestrator) noteChangedFiles(paths ...string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.changedFiles == nil {
		o.changedFiles = map[string]bool{}
	}
	for _, p := range paths {
		rel := strings.TrimSpace(p)
		if rel == "" {
			continue
		}
		o.changedFiles[rel] = true
	}
}

// changedFilesSnapshot returns the sorted set of paths changed so far.
func (o *Orchestrator) changedFilesSnapshot() []string {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.changedFiles))
	for p := range o.changedFiles {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// resetChangedFiles clears the per-run change set.
func (o *Orchestrator) resetChangedFiles() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.changedFiles = map[string]bool{}
	o.mu.Unlock()
}
