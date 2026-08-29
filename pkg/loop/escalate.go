package loop

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Failure escalation: reading the attempt ledger back to change HOW the next
// attempt is made.
//
// plan.Task.AttemptLog has always recorded "attempt N failed because X", and
// its own doc comment says why: "a retry that changes nothing is an infinite
// loop with extra steps". What it changed was the PROMPT — the next attempt is
// told not to repeat itself. What it never changed was the model, so a task
// that has failed twice on a 7B retried on the same 7B, and the ladder's only
// remaining move was to give up and ask a human.
//
// This picks the second lever. Past a threshold the task is dispatched to an
// escalation rung — a separately registered agent, identical to its base
// except for the model it is pinned to (see pkg/agents/escalate.go).
//
// Three properties make it safe to have on by default:
//
//   - it is opt-in by configuration. With no ladder configured EscalationRungs
//     is zero and every function here returns the base role unchanged.
//   - it never dispatches to an id that is not registered. HasRole is checked
//     every time, because an unregistered agent id is not a degraded run — it
//     is a hard task failure, and it would happen precisely on the tasks that
//     were already struggling.
//   - it is monotone in attempts, so it cannot oscillate between models while
//     a task is being retried.

// DefaultEscalateAfter is how many recorded failures a task takes before it
// steps up a rung.
//
// Two, not one. A single failure is the common case for work that then
// succeeds on the retry — the corrector was given a specific issue and fixed
// it — and escalating there would spend the big model on nearly every task in
// a run. Two consecutive failures is the point where the evidence stops
// pointing at the task and starts pointing at the model.
const DefaultEscalateAfter = 2

// baseRole strips an escalation rung from an agent id.
//
// Every helper in this package that switches on a role STRING must go through
// it. A rung is the same agent with a different model, so `corrector@esc1` has
// to answer every "is this a corrector?" question exactly as `corrector` does.
// The failure mode when it does not is silent and severe: acceptanceSmokeRole
// stops matching, and the escalated corrector — the agent handling the task
// that has already failed twice — runs with the acceptance gate switched off.
func baseRole(role string) string {
	base, _ := agents.BaseRoleID(role)
	return base
}

// escalationRung returns the ladder rung a task has earned, 0 for none.
func (r *Runner) escalationRung(t plan.Task) int {
	if r == nil || r.EscalationRungs <= 0 {
		return 0
	}
	after := r.EscalateAfter
	if after <= 0 {
		after = DefaultEscalateAfter
	}
	// Two counters record two different things and either can be the honest
	// one. AttemptLog holds the per-task failure ledger; GateRetries counts how
	// often the escalate gate answered "retry" for this task and accumulates
	// for the life of the board. A task can rack up gate retries with an empty
	// attempt log (the gate retried before any attempt recorded a reason), so
	// taking the larger is what keeps escalation from stalling on either.
	attempts := len(t.AttemptLog)
	if t.GateRetries > attempts {
		attempts = t.GateRetries
	}
	if attempts < after {
		return 0
	}
	rung := 1 + (attempts-after)/after
	if rung > r.EscalationRungs {
		rung = r.EscalationRungs
	}
	return rung
}

// escalate maps a base agent id to the rung this task has earned.
//
// Returns base unchanged when there is no ladder, no rung earned, or no
// registered agent for the rung.
func (r *Runner) escalate(base string, t plan.Task) string {
	rung := r.escalationRung(t)
	if rung <= 0 || base == "" {
		return base
	}
	id := agents.EscalatedRoleID(base, rung)
	if r.HasRole == nil || !r.HasRole(id) {
		// No ladder was registered for this role — a tester, a splitter, or a
		// custom agent nobody derived variants for. Staying on the base agent
		// is the whole fallback: the task simply retries as it always did.
		return base
	}
	return id
}

// execAgentFor returns the agent id that should implement a task, accounting
// for the language its files are in and for any escalation it has earned.
func (r *Runner) execAgentFor(t plan.Task) string {
	return r.escalate(r.specializeExecRole(r.normalizeExecRole(t.Role), t), t)
}

// specializeExecRole routes a GENERIC worker task to the specialist that owns
// its files, leaving every other role untouched.
//
// Two things are fixed here, and they are the same thing seen from two ends.
//
// The composition names ONE execute.default_role for the whole run, so "a Go
// backend and a React frontend" put every task on one language specialist. The
// splitter could not have corrected it either: its contract allows only the
// generic roles worker/tester/explorer/context, so it has no way to say "this
// task is the Go one". Routing on files — evidence the splitter already emits
// and the harness already reconciles against disk — is the missing half.
//
// And default_role was reaching almost nothing anyway. normalizeExecRole hands
// it back only for an EMPTY role, but plan.Task.Normalize fills an empty role
// with "worker" long before the runner sees the task, so a splitter-authored
// task always arrived with a role that fell through to the generic worker. The
// chosen specialist was displayed in the composition and then not used.
//
// Deliberately narrow: only a generic worker id is re-routed. A task the
// composer or an operator deliberately pinned to a named specialist keeps it,
// and a tester/explorer/context role is never touched. HasRole gates every
// result, so this can only ever select an agent the factory actually built.
func (r *Runner) specializeExecRole(role string, t plan.Task) string {
	if r == nil {
		return role
	}
	// The assembler is checked BEFORE the language specialist, and that order
	// is what makes the feature work with nothing installed. Language
	// specialists like react-worker are pack-only — a fresh project registers
	// none of them — so an assembler that only upgraded an inferred
	// react-worker would never fire until someone applied the react pack,
	// which is exactly the setup step this is meant not to need.
	//
	// It also supersedes a frontend specialist the board ALREADY carries.
	// Per-task routing staffs a task from its own file extensions before this
	// runs, so a .tsx task arrives as `react-worker` and a guard that accepted
	// only the generic `worker` skipped the assembler entirely. Measured on a
	// live shadcn run: the harness announced
	//
	//   · init frontend: shadcn-worker — the request named shadcn/ui
	//
	// and then executed the task as @react-worker — the dedicated assembler,
	// whose whole purpose is to REUSE published components instead of writing
	// them, was chosen and never used.
	if isGenericWorkerRole(role) || assemblerSupersedes(role) {
		if a := r.frontendAssemblerFor(t.Files); a != "" && a != role {
			return a
		}
	}
	if !isGenericWorkerRole(role) {
		return role
	}
	spec := agents.SpecialistForFiles(t.Files, r.HasRole)
	if spec == "" || spec == role {
		return role
	}
	return spec
}

// assemblerSupersedes reports whether role is a frontend implementer that a
// chosen assembler is the more specific version of.
//
// Only these: an assembler is a React-component specialist, so it may replace
// the generic React/TypeScript worker that routing inferred from a .tsx path
// and nothing else. A go-worker or a tester keeps its own role.
func assemblerSupersedes(role string) bool {
	switch baseRole(strings.ToLower(strings.TrimSpace(role))) {
	case "react-worker", "ts-worker", "typescript-worker", "vue-worker", "svelte-worker":
		return true
	}
	return false
}

// frontendAssemblerFor returns the component-library assembler this run chose,
// when this task's files are React component files.
//
// The language and the METHOD are two different questions and only the first is
// answerable from a file extension: .tsx says React, never whether this project
// installs its components or writes them. FrontendAssembler carries the answer
// to the second, decided once per run from the request and the workspace (see
// agents.ChooseFrontend), so a task does not re-derive it per file.
//
// Gated on React files specifically: a Go or Python task in the same run is not
// frontend work, and plain .html/.css is neither library's shape.
func (r *Runner) frontendAssemblerFor(files []string) string {
	if r.FrontendAssembler == "" || r.HasRole == nil || !r.HasRole(r.FrontendAssembler) {
		return ""
	}
	if !agents.HasReactFiles(files) {
		return ""
	}
	return r.FrontendAssembler
}

// isGenericWorkerRole reports whether an executor id is the unspecialized
// worker — the only one file-based routing may replace.
func isGenericWorkerRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case plan.RoleWorker, "implementer", "deep", "":
		return true
	}
	return false
}

// correctorIDFor returns the corrector for a task, escalated if earned.
//
// The corrector matters at least as much as the worker here. By the time a
// task has failed twice, what runs next is almost always a correction round
// against a specific reviewer issue — so the corrector is the agent actually
// holding the failing work, and leaving it on the model that could not fix it
// twice would make the whole ladder decorative.
func (r *Runner) correctorIDFor(t plan.Task) string {
	return r.escalate(r.correctorID(), t)
}

// escalationNote renders a one-line operator-facing explanation, or "".
func (r *Runner) escalationNote(t plan.Task) string {
	rung := r.escalationRung(t)
	if rung <= 0 {
		return ""
	}
	base := r.normalizeExecRole(t.Role)
	if id := agents.EscalatedRoleID(base, rung); r.HasRole != nil && r.HasRole(id) {
		return "escalated to model rung " + itoa(rung) + " after repeated failures"
	}
	return ""
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
