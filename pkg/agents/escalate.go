package agents

import (
	"strconv"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Model routing: an explicit role→model map, and a failure escalation ladder.
//
// Before this file the whole routing story was Factory.FastModel plus the
// isLightAgent classification: one lever, two models, and a per-class decision.
// The per-role bandit (SetPreferFast) made the lever finer but not wider — it
// still only ever chose between the same two models.
//
// Two things are added here, and they answer different questions.
//
// ── RoleModels answers "which model should this ROLE use" ────────────────
//
// A map from role id to model name, straight from config. It generalizes the
// fast/main binary to as many models as an operator wants to name: a reviewer
// on a 3B, workers on a 30B, the composer on a frontier endpoint.
//
// ── The escalation ladder answers "this TASK keeps failing" ──────────────
//
// Which is a genuinely different question, because it is about one task's
// history rather than a role's job. The state needed to answer it was already
// being recorded — plan.Task.AttemptLog is the "attempt N failed because X"
// ledger carried across retries — and nothing read it back to change HOW the
// next attempt is made. A task that has failed twice on the local 7B retrying
// on the same 7B with the same prompt shape is an infinite loop with extra
// steps.
//
// Agents resolve their model once, at construction, and the loop dispatches to
// them by id. So a rung is not a mutation of an existing agent — it is a
// SEPARATE registered agent, identical to its base in every way except the
// model it is pinned to, and reachable by an id the loop can compute. That
// keeps escalation entirely out of the hot path: no locking, no rebuild
// between waves, and a task that never escalates never pays anything.

// EscalationSuffix separates a base role id from its rung number.
//
// '@' is deliberate: it appears in no built-in or custom role id, and — unlike
// '-' — it cannot collide with a legitimately hyphenated name such as
// reviewer-strict or go-tester.
const EscalationSuffix = plan.EscalationSuffix

// MaxEscalationRungs bounds how many ladder steps may be registered.
//
// Every rung multiplies the registered agent count by the number of escalable
// roles, and a ladder deeper than a couple of steps describes a model lineup
// nobody has: if the 30B could not do it either, a third rung is not the
// answer.
const MaxEscalationRungs = 3

// EscalatedRoleID returns the agent id for a role at an escalation rung.
// Rung 0 (or less) is the base role itself.
func EscalatedRoleID(role string, rung int) string {
	role = strings.TrimSpace(role)
	if rung <= 0 || role == "" {
		return role
	}
	return role + EscalationSuffix + strconv.Itoa(rung)
}

// BaseRoleID splits an agent id into its base role and escalation rung.
//
// Every classification helper that switches on an id — isCodingRole,
// isLightAgent, schema role detection — must go through this, or an escalated
// worker silently stops being treated as a coding role and loses its model
// profile caps. That failure is invisible: the agent still runs, just with the
// wrong token budget.
func BaseRoleID(id string) (string, int) {
	i := strings.LastIndex(id, EscalationSuffix)
	if i < 0 {
		return id, 0
	}
	rung, err := strconv.Atoi(id[i+len(EscalationSuffix):])
	if err != nil || rung <= 0 {
		return id, 0
	}
	return id[:i], rung
}

// IsEscalated reports whether an agent id names an escalation rung.
func IsEscalated(id string) bool {
	_, rung := BaseRoleID(id)
	return rung > 0
}

// escalableRoles are the roles a ladder is built for.
//
// Only the roles that WRITE or JUDGE code. Escalating a splitter or a memory
// agent because a worker's task failed twice spends a bigger model on the one
// part of the run that was not the problem.
var escalableRoles = map[string]bool{
	plan.RoleWorker: true, "deep": true, plan.RoleCorrector: true,
	plan.RoleReviewer: true, RoleEditor: true, RoleReviewerStrict: true,
}

// IsEscalableRole reports whether a ladder should be registered for a role.
//
// Language specialists (go-worker, python-tester…) are escalable too: they are
// workers with a different prompt, and a custom worker is exactly the case
// where an operator most wants a bigger model on the second failure.
func IsEscalableRole(id string) bool {
	base, _ := BaseRoleID(id)
	base = strings.ToLower(strings.TrimSpace(base))
	if escalableRoles[base] {
		return true
	}
	return strings.HasSuffix(base, "-worker") || strings.HasSuffix(base, "-corrector")
}

// SetEscalation installs the model ladder. Rung N (1-based) uses models[N-1].
//
// The list is the models to escalate TO — the base role already covers rung 0,
// so a two-model setup ("the 7B normally, the 30B when it struggles") is a
// one-element list.
func (f *Factory) SetEscalation(models []string) {
	if f == nil {
		return
	}
	var clean []string
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || len(clean) >= MaxEscalationRungs {
			continue
		}
		clean = append(clean, m)
	}
	f.mu.Lock()
	f.escalation = clean
	f.mu.Unlock()
}

// Escalation returns the configured ladder.
func (f *Factory) Escalation() []string {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]string(nil), f.escalation...)
}

// EscalationRungs returns how many rungs are available above the base model.
func (f *Factory) EscalationRungs() int { return len(f.Escalation()) }

// SetRoleModel pins one role to a specific model, overriding both the fast
// preference and the light/heavy classification.
//
// Roles are matched case-insensitively. Agents resolve their model at
// construction, so set this BEFORE building the agents for a wave.
func (f *Factory) SetRoleModel(role, model string) {
	if f == nil {
		return
	}
	role = strings.ToLower(strings.TrimSpace(role))
	model = strings.TrimSpace(model)
	if role == "" || model == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.roleModels == nil {
		f.roleModels = map[string]string{}
	}
	f.roleModels[role] = model
}

// SetRoleModels replaces the whole role→model map.
func (f *Factory) SetRoleModels(m map[string]string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.roleModels = nil
	f.mu.Unlock()
	for role, model := range m {
		f.SetRoleModel(role, model)
	}
}

// RoleModel returns the pinned model for a role, if any.
func (f *Factory) RoleModel(role string) (string, bool) {
	if f == nil {
		return "", false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	m, ok := f.roleModels[strings.ToLower(strings.TrimSpace(role))]
	return m, ok
}

// escalatedSpecs derives the ladder variants for a base spec list.
//
// A variant is its base in every respect except two: the id carries the rung,
// and Model is pinned. Pinning Model is what makes the rest of the resolution
// chain fall away — EffectiveModel returns spec.Model first, so a variant
// needs no special case anywhere else.
func (f *Factory) escalatedSpecs(base []RoleSpec) []RoleSpec {
	lad := f.Escalation()
	if len(lad) == 0 {
		return nil
	}
	var out []RoleSpec
	for _, spec := range base {
		if !IsEscalableRole(spec.ID) || IsEscalated(spec.ID) {
			continue
		}
		for rung := 1; rung <= len(lad); rung++ {
			v := spec
			v.ID = EscalatedRoleID(spec.ID, rung)
			v.Model = lad[rung-1]
			v.Title = spec.Title + " (escalated)"
			// The variant is derived from an ALREADY-NORMALIZED spec, so it
			// inherits SchemaRole, SerialTools and StopSequences verbatim.
			// That is load-bearing: re-normalizing would derive the contract
			// from the VARIANT id, which names no schema role, and the rung
			// would decode unconstrained — the one thing this harness never
			// lets a structured role do.
			out = append(out, v)
		}
	}
	return out
}
