// Package squads models parallel virtual development teams.
//
// # WHY THIS EXISTS
//
// A query like "build a Go backend serving a React frontend" is not one stream
// of work; it is two, and they fail for two different reasons when run as one.
//
//   - Run them SEQUENTIALLY and the wall-clock is the sum of both halves, with
//     the second half re-deriving context the first half already established.
//   - Run them CONCURRENTLY with nothing but the existing file-disjointness
//     rule and the halves disagree about the seam between them: the frontend
//     invents `GET /todos` returning `{items:[…]}` while the backend builds
//     `GET /api/todos` returning a bare array. Both halves pass their own
//     tests. The application does not work.
//
// The fix is not more parallelism. It is an INTERFACE FROZEN BEFORE EITHER
// HALF STARTS, plus ownership boundaries that keep the halves from editing
// each other's files while they work.
//
// So a squad is three things, and it is useless without all three:
//
//  1. a DOMAIN it owns (path globs) — nobody else may write there;
//  2. its own ACCEPTANCE command — `go test ./...` and `npm run build` are not
//     interchangeable, and one global QA command cannot express both;
//  3. a CONTRACT it provides to or consumes from other squads — written to
//     disk before any worker runs, so both halves build against the same text
//     rather than against their own recollection of the prompt.
//
// This package is deliberately pure: no board, no LLM, no filesystem beyond
// explicit Save/Load. Everything here is a decision that must be reproducible
// and testable without a model in the loop.
package squads

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// Squad is one virtual development team.
type Squad struct {
	// ID is the stable handle used on tasks and in events ("backend").
	ID string `json:"id"`
	// Name is the human label ("Backend · Go API").
	Name string `json:"name,omitempty"`
	// Charter is the one-line mission, injected into every task pack for this
	// squad. It is what keeps a worker from drifting into the other half.
	Charter string `json:"charter,omitempty"`
	// Owns lists path globs (`**` supported) this squad may write to. Two
	// squads may never own the same path — see Plan.Validate.
	Owns []string `json:"owns"`
	// Acceptance is the command that proves THIS squad's half works on its own.
	Acceptance string `json:"acceptance,omitempty"`
	// Worker / Reviewer / Tester name the agents staffing the squad. Empty
	// means the pipeline's defaults.
	//
	// All three are consulted by task routing (plan.RoutePolicy), one rung
	// BELOW the language of the task's own files: when a squad says `go-worker`
	// and the task's files are all `.tsx`, the files are right and the squad
	// label is stale. A field the UI offers and routing ignores is worse than
	// one it does not offer, so all three reach the loop or none should exist.
	Worker   string `json:"worker,omitempty"`
	Reviewer string `json:"reviewer,omitempty"`
	Tester   string `json:"tester,omitempty"`
	// Manager is the agent that triages THIS squad's rejected work.
	//
	// A rejected delivery is a staffing decision — who takes it next, and what
	// do they need to know that the last attempt did not — and it is a decision
	// about one team's people, in one team's domain. A single run-wide manager
	// answering it for every squad is choosing from a roster it has no reason
	// to understand: the agents who can actually fix a failing Go handler are
	// not the ones staffing the React half.
	//
	// Empty means the run's default manager, which is still better than the
	// deterministic ladder. See pkg/loop/handoff.go.
	Manager string `json:"manager,omitempty"`
	// Agents is the squad's ROSTER beyond the named seats — the people this
	// team is made of, in whatever number its author chose. Its own members are
	// listed FIRST when its manager triages a rejected delivery, which is the
	// whole reason a per-team manager beats a run-wide one.
	Agents []string `json:"agents,omitempty"`
	// Skills are loaded into this squad's task packs.
	Skills []string `json:"skills,omitempty"`
}

// Interface is one clause of the frozen contract between squads.
type Interface struct {
	// ID is the interface's name — an HTTP route, an exported symbol, a file
	// format. Free-form on purpose: the seam between a Go API and a React SPA
	// is a route, but between a library and its CLI it is a function.
	ID string `json:"id"`
	// Provider is the squad id that implements it.
	Provider string `json:"provider"`
	// Consumers are the squad ids that depend on it.
	Consumers []string `json:"consumers,omitempty"`
	// Spec is the shape both sides must agree on — the request/response body,
	// the signature, the schema. This is the text that stops the two halves
	// from inventing different answers.
	Spec string `json:"spec,omitempty"`
}

// Contract is the full inter-squad interface, frozen before execution.
type Contract struct {
	Summary    string      `json:"summary,omitempty"`
	Interfaces []Interface `json:"interfaces,omitempty"`
}

// Integration describes the join step that runs after every squad is green.
//
// It is NOT a squad: it owns no paths of its own (it may touch anything) and it
// runs alone, so modeling it as one would make it overlap every squad and trip
// the ownership check that exists to keep squads apart.
type Integration struct {
	// Acceptance is the command that proves the halves work TOGETHER. A run
	// where both squads are green and this is red is a failed run — that is
	// the whole point of having it.
	Acceptance string `json:"acceptance,omitempty"`
	// Notes are wiring instructions: where the frontend's base URL comes from,
	// which port the API binds, how static assets get served.
	Notes []string `json:"notes,omitempty"`
}

// Plan is the project manager's output: who exists, what they own, and what
// they owe each other.
type Plan struct {
	Summary     string      `json:"summary,omitempty"`
	Contract    Contract    `json:"contract"`
	Squads      []Squad     `json:"squads"`
	Integration Integration `json:"integration"`
}

// Enabled reports whether this plan actually splits work across teams.
//
// One squad is not a team structure, it is the normal single-stream pipeline
// wearing a hat — and paying the contract/integration overhead for it would be
// pure cost. Callers use this to decide whether to take the squad path at all.
func (p *Plan) Enabled() bool { return p != nil && len(p.Squads) >= 2 }

// Squad returns the squad with the given id.
func (p *Plan) Squad(id string) (Squad, bool) {
	if p == nil {
		return Squad{}, false
	}
	for _, s := range p.Squads {
		if s.ID == id {
			return s, true
		}
	}
	return Squad{}, false
}

// ResolveRef maps a loosely-written squad reference onto a real squad id.
//
// # WHY THIS EXISTS
//
// The plan's squad ids come from the team library — `backend-go`,
// `frontend-react` — and the interface contract comes from a model that was
// handed those ids and asked to reuse them. A 7–32B model reliably writes
// `backend`. Both halves then build against a contract clause whose provider
// matches no squad, Validate rejects it, and the run loses the frozen seam it
// was supposed to be protected by — over a suffix.
//
// So a reference resolves when it is UNAMBIGUOUS: exact first, then a unique
// prefix, then a unique substring either way round. Ambiguity is not resolved
// by picking a favorite — `backend` against a plan holding both `backend-go`
// and `backend-node` is a genuine question only the author can answer, and
// guessing puts a clause on the wrong team.
func (p *Plan) ResolveRef(ref string) (string, bool) {
	ref = slug(ref)
	if p == nil || ref == "" {
		return "", false
	}
	for _, s := range p.Squads {
		if s.ID == ref {
			return s.ID, true
		}
	}
	match := func(pick func(id string) bool) (string, bool) {
		found := ""
		for _, s := range p.Squads {
			if !pick(s.ID) {
				continue
			}
			if found != "" {
				return "", false // ambiguous
			}
			found = s.ID
		}
		return found, found != ""
	}
	if id, ok := match(func(id string) bool { return strings.HasPrefix(id, ref+"-") }); ok {
		return id, true
	}
	if id, ok := match(func(id string) bool { return strings.HasPrefix(ref, id+"-") }); ok {
		return id, true
	}
	if id, ok := match(func(id string) bool {
		return strings.Contains(id, ref) || strings.Contains(ref, id)
	}); ok {
		return id, true
	}
	return "", false
}

// IDs lists squad ids in plan order.
func (p *Plan) IDs() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Squads))
	for _, s := range p.Squads {
		out = append(out, s.ID)
	}
	return out
}

// ── Ownership ────────────────────────────────────────────────────────────

// OwnsPath reports whether this squad's globs cover rel.
func (s Squad) OwnsPath(rel string) bool {
	rel = normalizePath(rel)
	if rel == "" {
		return false
	}
	for _, g := range s.Owns {
		if matchOwn(g, rel) {
			return true
		}
	}
	return false
}

// matchOwn matches a path against one ownership glob.
//
// An ownership pattern names a REGION, not a file, so `api` and `api/**` must
// both cover `api/server.go`. Without the prefix rule a plan that says it owns
// "web" would own nothing at all inside web/, and every frontend task would
// come back unassigned — the failure looks like the splitter being wrong when
// it is the matcher being too literal.
func matchOwn(glob, rel string) bool {
	glob = normalizePath(glob)
	if glob == "" {
		return false
	}
	if workspace.MatchGlob(glob, rel) {
		return true
	}
	// A bare directory owns its subtree.
	if !strings.ContainsAny(glob, "*?[") {
		return rel == glob || strings.HasPrefix(rel, glob+"/")
	}
	// "api/**" should also match "api" itself, so a task naming the directory
	// is not homeless.
	if trimmed := strings.TrimSuffix(strings.TrimSuffix(glob, "**"), "/"); trimmed != "" && trimmed != glob {
		return rel == trimmed || strings.HasPrefix(rel, trimmed+"/")
	}
	return false
}

// Owner returns the squad that owns rel.
//
// Ties are impossible by construction: Validate rejects a plan whose squads
// overlap, so at most one squad can match. The first match therefore is THE
// match, and the iteration order of Squads never becomes load-bearing.
func (p *Plan) Owner(rel string) (string, bool) {
	if p == nil {
		return "", false
	}
	for _, s := range p.Squads {
		if s.OwnsPath(rel) {
			return s.ID, true
		}
	}
	return "", false
}

// Assignment is the result of routing one unit of work to a squad.
type Assignment struct {
	// Squad is the owning squad id, empty when nothing owns these files.
	Squad string
	// Straddles lists every squad the files touch when they touch more than
	// one. A straddling unit of work is NOT assigned: it is the seam itself,
	// and handing it to one of the two teams is how a "frontend" task ends up
	// rewriting the API. It belongs to integration.
	Straddles []string
	// Unowned lists files no squad claims — a gap in the plan, not a task bug.
	Unowned []string
}

// Assign routes a set of files to the squad that owns them.
//
// Deterministic for a given plan and file set: no map iteration reaches the
// result, and Straddles comes back in plan order.
func (p *Plan) Assign(files []string) Assignment {
	var out Assignment
	if p == nil {
		return out
	}
	seen := map[string]bool{}
	var order []string
	for _, f := range files {
		rel := normalizePath(f)
		if rel == "" {
			continue
		}
		owner, ok := p.Owner(rel)
		if !ok {
			out.Unowned = append(out.Unowned, rel)
			continue
		}
		if !seen[owner] {
			seen[owner] = true
			order = append(order, owner)
		}
	}
	// Plan order, not first-touched order, so the report reads the same way
	// the org chart does.
	sortByPlanOrder(order, p.IDs())
	switch len(order) {
	case 0:
		return out
	case 1:
		out.Squad = order[0]
		return out
	default:
		out.Straddles = order
		return out
	}
}

func sortByPlanOrder(ids, planOrder []string) {
	rank := make(map[string]int, len(planOrder))
	for i, id := range planOrder {
		rank[id] = i
	}
	sort.SliceStable(ids, func(i, j int) bool {
		ri, oki := rank[ids[i]]
		rj, okj := rank[ids[j]]
		if oki && okj {
			return ri < rj
		}
		if oki != okj {
			return oki
		}
		return ids[i] < ids[j]
	})
}

func normalizePath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return p
}

// ── Normalization ────────────────────────────────────────────────────────

// Normalize fills defaults and removes the junk a small model reliably emits:
// blank entries, duplicate globs, squad ids in mixed case, consumers naming
// their own provider.
func (p *Plan) Normalize() {
	if p == nil {
		return
	}
	p.Summary = strings.TrimSpace(p.Summary)
	p.Contract.Summary = strings.TrimSpace(p.Contract.Summary)

	kept := p.Squads[:0]
	seenID := map[string]bool{}
	for _, s := range p.Squads {
		s.ID = slug(s.ID)
		if s.ID == "" || seenID[s.ID] {
			// A squad with no id cannot be assigned to, referenced by a
			// contract, or reported on. Dropping it is the only honest
			// option; Validate then reports the plan as too small if that
			// leaves fewer than two.
			continue
		}
		seenID[s.ID] = true
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" {
			s.Name = s.ID
		}
		s.Charter = strings.TrimSpace(s.Charter)
		s.Acceptance = strings.TrimSpace(s.Acceptance)
		s.Worker = strings.TrimSpace(s.Worker)
		s.Reviewer = strings.TrimSpace(s.Reviewer)
		s.Tester = strings.TrimSpace(s.Tester)
		s.Manager = strings.TrimSpace(s.Manager)
		s.Owns = dedupePaths(s.Owns)
		s.Agents = dedupeStrings(s.Agents)
		s.Skills = dedupeStrings(s.Skills)
		kept = append(kept, s)
	}
	p.Squads = kept

	valid := map[string]bool{}
	for _, s := range p.Squads {
		valid[s.ID] = true
	}

	ifaces := p.Contract.Interfaces[:0]
	seenIface := map[string]bool{}
	for _, in := range p.Contract.Interfaces {
		in.ID = strings.TrimSpace(in.ID)
		if in.ID == "" || seenIface[in.ID] {
			continue
		}
		seenIface[in.ID] = true
		in.Provider = slug(in.Provider)
		in.Spec = strings.TrimSpace(in.Spec)
		cons := make([]string, 0, len(in.Consumers))
		for _, c := range in.Consumers {
			c = slug(c)
			// A provider listed as its own consumer is noise that makes the
			// dependency read as circular in the rendered contract.
			if c == "" || c == in.Provider {
				continue
			}
			cons = append(cons, c)
		}
		in.Consumers = dedupeStrings(cons)
		ifaces = append(ifaces, in)
	}
	p.Contract.Interfaces = ifaces

	p.Integration.Acceptance = strings.TrimSpace(p.Integration.Acceptance)
	p.Integration.Notes = dedupeStrings(p.Integration.Notes)
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dedupePaths(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = normalizePath(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ── Parsing ──────────────────────────────────────────────────────────────

// Parse reads a project-manager response into a Plan.
//
// Tolerant of the wrappers small models put around JSON (prose, fences, a
// leading "Here is the plan:") because the alternative is discarding a
// perfectly good org chart over a code fence.
func Parse(raw string) (Plan, error) {
	body := extractJSONObject(raw)
	if body == "" {
		return Plan{}, fmt.Errorf("squads: no JSON object found in %d bytes of output", len(raw))
	}
	var p Plan
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return Plan{}, fmt.Errorf("squads: %w", err)
	}
	p.Normalize()
	return p, nil
}

// extractJSONObject finds the outermost {...} in a blob, ignoring braces inside
// strings so a charter containing "{" cannot truncate the object.
func extractJSONObject(raw string) string {
	start := strings.Index(raw, "{")
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}
