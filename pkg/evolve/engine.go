package evolve

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/memory"
)

// Engine is the one-call facade over the whole self-improvement subsystem:
// memory, repair rules, the policy bandit and regression checks, opened
// together against the same project and user directories.
//
// A harness needs exactly four touch points:
//
//	eng.Memory().RenderForPrompt(role, budget)  before each specialist call
//	eng.OnFailure(sig, args)                    when anything fails
//	eng.Choose(decision, arms...)               at each discrete choice
//	eng.Finish(report)                          at the end of the run
//
// Every method is nil-safe and best-effort: a broken store degrades the
// harness to its un-evolved behavior, it never stops a run.
type Engine struct {
	mem    *memory.Store
	rules  *Rules
	bandit *Bandit
	regs   *Regressions

	projectDir string
	warnings   []string
}

// EngineOptions configures Open.
type EngineOptions struct {
	// Deterministic disables bandit exploration (`--no-explore`, CI).
	Deterministic bool
	// Seed makes exploration reproducible.
	Seed int64
	// Now is injectable for tests.
	Now func() time.Time
	// ReadOnly opens every store without writing.
	ReadOnly bool
	// ProjectPolicy keeps the bandit policy in the project instead of the
	// user directory. Cross-project transfer is the default because what
	// works for a MODEL is not project-specific.
	ProjectPolicy bool
	// NoSeedRules starts with an empty repair-rule store.
	NoSeedRules bool
}

// Open opens the whole subsystem. projectDir is the project root; userDir is
// the user's home (empty resolves to os.UserHomeDir). It never returns a nil
// Engine on error — a degraded Engine is still safe to call.
func Open(projectDir, userDir string) (*Engine, error) {
	return OpenWith(projectDir, userDir, EngineOptions{})
}

// OpenWith is Open with options.
func OpenWith(projectDir, userDir string, opt EngineOptions) (*Engine, error) {
	if strings.TrimSpace(userDir) == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userDir = home
		}
	}
	e := &Engine{projectDir: projectDir}
	var errs []error

	mem, err := memory.OpenWith(projectDir, userDir, memory.Options{Now: opt.Now, ReadOnly: opt.ReadOnly})
	if err != nil {
		errs = append(errs, err)
	}
	e.mem = mem
	if mem != nil {
		e.warnings = append(e.warnings, mem.Warnings()...)
	}

	rules, err := OpenRulesWith(projectDir, userDir, RulesOptions{Now: opt.Now, NoSeed: opt.NoSeedRules})
	if err != nil {
		errs = append(errs, err)
	}
	e.rules = rules
	if rules != nil {
		e.warnings = append(e.warnings, rules.Warnings()...)
	}

	policyDir := userDir
	if opt.ProjectPolicy {
		policyDir = projectDir
	}
	bandit, err := OpenBanditWith(policyDir, BanditOptions{
		Deterministic: opt.Deterministic, Seed: opt.Seed, Now: opt.Now,
	})
	if err != nil {
		errs = append(errs, err)
	}
	e.bandit = bandit
	if bandit != nil {
		e.warnings = append(e.warnings, bandit.Warnings()...)
	}

	regs, err := OpenRegressionsWith(projectDir, opt.Now)
	if err != nil {
		errs = append(errs, err)
	}
	e.regs = regs
	if regs != nil {
		e.warnings = append(e.warnings, regs.Warnings()...)
	}
	return e, errors.Join(errs...)
}

// Memory returns the memory store.
func (e *Engine) Memory() *memory.Store {
	if e == nil {
		return nil
	}
	return e.mem
}

// Rules returns the repair-rule store.
func (e *Engine) Rules() *Rules {
	if e == nil {
		return nil
	}
	return e.rules
}

// Bandit returns the policy bandit.
func (e *Engine) Bandit() *Bandit {
	if e == nil {
		return nil
	}
	return e.bandit
}

// Regressions returns the regression-check store.
func (e *Engine) Regressions() *Regressions {
	if e == nil {
		return nil
	}
	return e.regs
}

// Warnings returns every non-fatal problem found while opening.
func (e *Engine) Warnings() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.warnings...)
}

// Advice is what the harness gets back when something fails.
type Advice struct {
	// Found is false when nothing is known about this failure.
	Found bool
	// Fingerprint identifies the failure; record it on the FailureEvent so the
	// reflection pass can learn from it.
	Fingerprint Fingerprint
	// RuleID is the rule that produced this advice, for crediting later.
	RuleID string
	// Apply is true when the repair is confident enough to act on without
	// asking a model.
	Apply bool
	// Repair is the typed repair to perform.
	Repair Repair
	// NewArgs is the rewritten tool-call arguments when the repair was a
	// deterministic argument transform and it changed something. When this is
	// non-empty the harness can simply retry — no LLM round-trip at all.
	NewArgs string
	// Reason explains the match in one line, for logs and the UI.
	Reason string
}

// OnFailure is the "fail once" entry point. Call it the moment anything fails,
// passing the raw tool-call arguments when there are any.
//
// It also folds the failure into working memory so the current run's prompt
// block reflects it.
func (e *Engine) OnFailure(sig Signal, args string) Advice {
	if e == nil {
		return Advice{}
	}
	fp := Analyze(sig)
	if e.mem != nil {
		e.mem.Working().Fail(memory.Failure{
			Key:     fp.ID,
			Tool:    sig.Tool,
			Path:    sig.Path,
			Message: sig.Message,
		})
	}
	if e.rules == nil {
		return Advice{Fingerprint: fp}
	}
	s, ok := e.rules.Lookup(sig)
	if !ok {
		return Advice{Fingerprint: fp}
	}
	adv := Advice{
		Found:       true,
		Fingerprint: s.Fingerprint,
		RuleID:      s.Rule.ID,
		Apply:       s.Apply,
		Repair:      s.Rule.Repair,
		Reason:      s.Reason(),
	}
	if s.Rule.Repair.Kind == RepairTransformArgs && args != "" {
		if out, changed := ApplyTransform(s.Rule.Repair.Transform, args); changed {
			adv.NewArgs = out
		}
	}
	return adv
}

// Resolved tells the engine a failure was fixed, and by what. Pass the RuleID
// from the Advice when a stored rule did the work, or "" plus the repair that
// actually worked so a new rule can be synthesized at the end of the run.
func (e *Engine) Resolved(fp Fingerprint, ruleID, resolution string) {
	if e == nil || e.mem == nil {
		return
	}
	by := ruleID
	if by == "" {
		by = "llm"
	} else {
		by = "rule:" + ruleID
	}
	e.mem.Working().Resolve(fp.ID, resolution, by)
	if e.rules != nil && ruleID != "" {
		e.rules.BindFingerprint(ruleID, fp.ID)
	}
}

// Choose picks an arm for a decision in the current run's context.
func (e *Engine) Choose(decision Decision, arms ...string) Choice {
	if e == nil || e.bandit == nil {
		if len(arms) > 0 {
			return Choice{Arm: arms[0], Reason: "policy unavailable; using the first option"}
		}
		return Choice{}
	}
	return e.bandit.ChooseWithReason(e.keyFor(decision), arms)
}

// Why explains a decision's current state.
func (e *Engine) Why(decision Decision) string {
	if e == nil || e.bandit == nil {
		return "policy unavailable"
	}
	return e.bandit.Why(e.keyFor(decision))
}

func (e *Engine) keyFor(decision Decision) Key {
	k := Key{Decision: decision}
	if e.mem != nil {
		rc := e.mem.RunContext()
		k.ModelFamily = rc.ModelFamily
		k.Language = rc.Language
	}
	return k
}

// Finish runs the reflection loop: it turns the report into an episode, credits
// the rules that were used, learns new ones, updates the policy, records
// regression checks, distills semantic memory and writes REFLECTION.md.
//
// summarize is optional; pass nil for a fully deterministic reflection.
func (e *Engine) Finish(ctx context.Context, r RunReport, summarize memory.Summarizer) (Reflection, error) {
	ref := Reflect(r)
	if e == nil {
		return ref, nil
	}
	if summarize != nil {
		Enrich(ctx, &ref, summarize)
	}
	var errs []error
	if err := ref.Apply(e.mem, e.rules, e.bandit, e.regs); err != nil {
		errs = append(errs, err)
	}
	if e.mem != nil {
		if err := e.mem.Distill(ctx, summarize); err != nil {
			errs = append(errs, err)
		}
		if err := e.mem.Prune(memory.DefaultPrunePolicy()); err != nil {
			errs = append(errs, err)
		}
	}
	if e.rules != nil {
		e.rules.Prune(DefaultRulePolicy())
		if err := e.rules.Save(); err != nil {
			errs = append(errs, err)
		}
	}
	if e.bandit != nil {
		e.bandit.Prune(MaxBanditKeys)
		if err := e.bandit.Save(); err != nil {
			errs = append(errs, err)
		}
	}
	if e.regs != nil {
		e.regs.Prune(MaxChecks)
		if err := e.regs.Save(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := WriteReflection(e.projectDir, ref); err != nil {
		errs = append(errs, err)
	}
	return ref, errors.Join(errs...)
}

// Close flushes every store.
func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	var errs []error
	if e.mem != nil {
		errs = append(errs, e.mem.Close())
	}
	if e.rules != nil {
		errs = append(errs, e.rules.Save())
	}
	if e.bandit != nil {
		errs = append(errs, e.bandit.Save())
	}
	if e.regs != nil {
		errs = append(errs, e.regs.Save())
	}
	return errors.Join(errs...)
}

// Forget erases the evolve state (rules, policy, regressions) and, when
// scope is memory.ScopeAll, the memory stores too. This is the documented
// reversal path; deleting the directories by hand does the same thing.
func (e *Engine) Forget(scope memory.Scope) error {
	if e == nil {
		return nil
	}
	var errs []error
	if e.rules != nil {
		errs = append(errs, e.rules.Forget())
	}
	if e.bandit != nil {
		errs = append(errs, e.bandit.Forget())
	}
	if e.regs != nil {
		errs = append(errs, e.regs.Forget())
	}
	if e.mem != nil && scope != "" {
		errs = append(errs, e.mem.Forget(scope))
	}
	return errors.Join(errs...)
}
