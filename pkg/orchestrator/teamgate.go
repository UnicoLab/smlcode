package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// ── Proving each half on its own ─────────────────────────────────────────
//
// # WHY THIS EXISTS
//
// A squad's acceptance command is one of the three things a squad is: the
// command that proves THIS half works alone. It was written into the contract,
// shown on the approval card, editable on the Teams page — and never executed.
// "Green" meant every task in the lane had reached done, which is a statement
// about the BOARD, not about the code.
//
// Two consequences, and the second is the one that matters:
//
//   - a half could be "complete" and not build, and the first thing to notice
//     was integration — which then reported the seam as wrong when the real
//     defect was one team's own code;
//   - a user watching a run had no per-team green at all. The board says a team
//     finished its tasks. Nothing said its half actually works.
//
// So each half is proved before the halves are joined. Three rules make this
// safe to add to a run that previously had no such gate:
//
//  1. A command that CANNOT START is UNVERIFIED, never failed. A missing `npm`
//     or an uninstalled `node_modules` is a fact about the machine, not about
//     the code, and failing a team for it is how a working run turns red.
//  2. A team with no acceptance command is UNVERIFIED and says so once. It is a
//     warning at plan time; repeating it as a gate failure would punish a plan
//     the harness already accepted.
//  3. A red half raises a ticket owned by THAT team, so the fix lands in the
//     lane that owns the files — and integration is skipped, because joining
//     halves when one is known-broken tests nothing.

// TeamGate is one team's acceptance result, as the UI reads it.
type TeamGate struct {
	// Team is the squad id this result belongs to.
	Team string `json:"team"`
	// Command is what was run. Empty means the team named none.
	Command string `json:"command,omitempty"`
	// Ran is false when the command could not start at all — a missing runner,
	// not broken code. Verified is then false and OK is meaningless.
	Ran bool `json:"ran"`
	// OK is the verdict, only meaningful when Ran.
	OK bool `json:"ok"`
	// Summary is the one line a human reads.
	Summary string `json:"summary,omitempty"`
}

// Verified reports whether this team's half was actually proved.
func (g TeamGate) Verified() bool { return g.Ran }

// teamGates holds the last acceptance result per team for this run.
//
// Run state, not plan state: it belongs to this execution and must not be
// written into squads.json, which is the org chart the NEXT run inherits.
type teamGates struct {
	mu sync.RWMutex
	by map[string]TeamGate
	// lane records, per team, the fingerprint of the team's own changed files
	// at the moment its half was last proved. The between-wave gate re-proves
	// a half only when this differs — see earlyTeamGate.
	lane map[string]string
}

// provedOn records the lane fingerprint a team's gate was taken against.
func (t *teamGates) provedOn(team, fingerprint string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lane == nil {
		t.lane = map[string]string{}
	}
	t.lane[team] = fingerprint
}

// lastLane returns the lane fingerprint the team was last proved against and
// whether it was ever proved this run.
func (t *teamGates) lastLane(team string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	fp, ok := t.lane[team]
	return fp, ok
}

func (t *teamGates) set(g TeamGate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = map[string]TeamGate{}
	}
	t.by[g.Team] = g
}

// reset forgets every result. Called at the start of a run: a gate is evidence
// about THIS run's tree, and the orchestrator outlives a run.
func (t *teamGates) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.by = nil
	t.lane = nil
}

// resetTeamState clears every piece of per-run team state — the squad plan,
// the single staffing team and the team gates — under the lock.
//
// Why all three, and why at the START of both Run and Resume: the squad plan
// used to be cleared halfway through runSLM and never on Resume, and the
// gates were never cleared at all. runTeamAcceptance skips teams with no work
// (Total == 0), so a previous run's green gate for a team that sits idle in
// this run survived, and allHalvesProved then read two green gates on a run
// that proved one half — upgrading a failed run's verdict on stale evidence.
func (o *Orchestrator) resetTeamState() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.squadPlan = nil
	o.singleTeam = nil
	o.teamPick = nil
	o.mu.Unlock()
	o.teamGates.reset()
}

func (t *teamGates) snapshot() []TeamGate {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]TeamGate, 0, len(t.by))
	for _, g := range t.by {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Team < out[j].Team })
	return out
}

// TeamGates is the per-team acceptance result for this run, for Studio.
//
// Sorted and copied: the live view polls it while the run writes it, and a map
// handed out by reference is a data race with a UI on the other end.
func (o *Orchestrator) TeamGates() []TeamGate {
	if o == nil {
		return nil
	}
	return o.teamGates.snapshot()
}

// runTeamAcceptance proves every half on its own. Returns the teams that failed.
//
// Sequential rather than parallel on purpose: these are build and test commands
// against one working tree, and two of them running at once is exactly the
// interference the ownership rules exist to prevent — one team's `npm run
// build` writing dist/ while another's `go test` reads the same tree produces a
// failure neither team caused.
func (o *Orchestrator) runTeamAcceptance(ctx context.Context, board *plan.Board) []string {
	p := o.squadPlanNow()
	if o == nil || p == nil || board == nil || len(p.Squads) < 2 {
		return nil
	}

	progress := map[string]squads.Status{}
	for _, st := range squads.Progress(p, board.Tasks) {
		progress[st.ID] = st
	}

	var failed []string
	for _, s := range p.Squads {
		st := progress[s.ID]
		// A team with no work did nothing to prove. Running its acceptance
		// would report on the state of the repository, not on this run.
		if st.Total == 0 {
			continue
		}
		if ctx.Err() != nil {
			return failed
		}
		// Each half is a command run, and the finish path has a reserve to
		// keep: a run that cannot afford another command run must say so
		// rather than overrun on the way to its own report.
		if !o.gateRoundAffordable(ctx, 2) {
			o.emitWarn("verify", "team "+s.ID+": not enough time left to run its acceptance and still report — "+
				"its half is UNVERIFIED", "")
			o.teamGates.set(TeamGate{Team: s.ID, Command: strings.TrimSpace(s.Acceptance),
				Summary: "not run — out of time"})
			continue
		}
		if g, red := o.proveHalf(ctx, board, p, s); red {
			failed = append(failed, g.Team)
		}
	}
	return failed
}

// squadPlanNow reads the run's squad plan under the lock.
func (o *Orchestrator) squadPlanNow() *squads.Plan {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.squadPlan
}

// proveHalf runs ONE team's acceptance command, records the gate and raises the
// team's ticket when its half is red. red is true only for a verdict that RAN
// and failed; an unrunnable check is UNVERIFIED and never red.
//
// The command goes through runSmoke, so a half already proved on this exact
// tree — by the between-wave gate, say — is answered from the run's smoke memo
// rather than by running the suite again.
func (o *Orchestrator) proveHalf(ctx context.Context, board *plan.Board, p *squads.Plan, s squads.Squad) (TeamGate, bool) {
	lane := o.laneFingerprint(p, s.ID)
	cmd := strings.TrimSpace(s.Acceptance)
	if cmd == "" {
		g := TeamGate{Team: s.ID, Summary: "no acceptance command — this half was never proved"}
		o.teamGates.set(g)
		o.teamGates.provedOn(s.ID, lane)
		o.emitWarn("verify", "team "+s.ID+" has no acceptance command — its half is unproved, "+
			"so a break in it can only surface at integration", "")
		return g, false
	}

	// The team is not wrong about wanting its half to compile, only about
	// what THIS project calls that. A shipped team declares
	// `npm --prefix web run build`; a scaffold may name it `compile`, or
	// have only `typecheck`. Resolving it is the difference between the
	// half being proved and being permanently grey.
	root := ""
	if o.cfg != nil {
		root = o.cfg.Root
	}
	if resolved, note := quality.ResolveScriptCommand(root, cmd); note != "" {
		o.emit("verify", "team "+s.ID+": "+note, "")
		cmd = resolved
	}

	o.emit("verify", "team "+s.ID+": proving its half alone — "+cmd, "")
	res := o.runSmoke(ctx, cmd)
	g := TeamGate{Team: s.ID, Command: cmd, Ran: res.Ran, OK: res.OK, Summary: res.Summary}
	o.teamGates.set(g)
	o.teamGates.provedOn(s.ID, lane)

	// A check that never ran is a fact about the MACHINE or the project,
	// not about the code. `npm run build` with node_modules never
	// installed, or with no build script in package.json, exits non-zero
	// and says nothing whatsoever about what the team wrote — scoring that
	// red sends a corrector to rewrite source that was never at fault,
	// burns the retry budget, and shows the user a red team for something
	// no model can fix. Both were measured live; the second is why this
	// asks CheckDidNotRun rather than ToolingMissing alone.
	if why := quality.CheckDidNotRun(cmd, res.Output); res.Ran && !res.OK && why != "" {
		g.Ran = false
		g.Summary = why + ": " + res.Summary
		o.teamGates.set(g)
	}

	switch {
	case !g.Ran:
		// A missing runner is a fact about the machine. Failing a team for
		// it turns a working run red for a reason the model cannot fix.
		o.emitWarn("verify", "team "+s.ID+": acceptance could not run ("+g.Summary+
			") — its half is UNVERIFIED, not broken", "")
		return g, false
	case res.OK:
		o.emit("verify", "team "+s.ID+" is green: "+cmd, "")
		o.recordGate("team:"+s.ID, true, "")
		return g, false
	default:
		o.emitWarn("verify", "team "+s.ID+" is RED — its own half does not pass: "+res.Summary, res.Output)
		o.recordGate("team:"+s.ID, false, res.Summary)
		o.raiseTeamTicket(board, s, cmd, res.Summary, res.Output)
		return g, true
	}
}

// laneFingerprint identifies what this run has written INSIDE one team's
// territory: the changed files the plan says the team owns, each with its
// write count so a rewrite of an already-changed file counts. Two equal
// fingerprints mean the team's half has not moved since — and the OTHER
// team's writes do not move it, which is what lets a finished lane stay
// proved while its neighbour keeps working.
func (o *Orchestrator) laneFingerprint(p *squads.Plan, team string) string {
	if o == nil || p == nil {
		return ""
	}
	var mine []string
	for _, fw := range o.changedFileWrites() {
		if owner, ok := p.Owner(fw.Path); ok && owner == team {
			mine = append(mine, fmt.Sprintf("%s:%d", fw.Path, fw.Writes))
		}
	}
	return strings.Join(mine, "\n")
}

// earlyTeamGate proves a half the moment its lane finishes, between waves.
//
// The finish path proves every half once the WHOLE board has drained. On a
// two-team run that is late: the backend can finish in wave 1 with a broken
// build and sit green-on-the-board for every wave the frontend still needs,
// its ticket raised only after the last frontend task — at which point the fix
// is a whole extra wave the run may no longer have time for. Proving a lane
// when it completes puts the ticket on the board for the NEXT wave.
//
// A team is proved here when squads.Progress says it is Complete and its lane
// has changed since it was last proved (or was never proved this run). A team
// that never wrote inside its own territory is not proved: its acceptance
// would report on the repository, not on this run. Commands go through the
// smoke memo, so the finish-path gate re-runs nothing the tree has not moved
// under since.
func (o *Orchestrator) earlyTeamGate(ctx context.Context, board *plan.Board) {
	p := o.squadPlanNow()
	if o == nil || p == nil || board == nil || len(p.Squads) < 2 || ctx.Err() != nil {
		return
	}
	for _, st := range squads.Progress(p, board.Tasks) {
		if !st.Complete {
			continue
		}
		s, ok := p.Squad(st.ID)
		if !ok {
			continue
		}
		lane := o.laneFingerprint(p, s.ID)
		if lane == "" {
			// Nothing written in this team's territory: nothing to prove yet.
			continue
		}
		if last, proved := o.teamGates.lastLane(s.ID); proved && last == lane {
			continue
		}
		if !o.gateRoundAffordable(ctx, 2) {
			o.emit("verify", "team "+s.ID+" finished its lane, but there is not enough time left to prove "+
				"it between waves — deferring to the finish path", "")
			return
		}
		o.emit("verify", "team "+s.ID+" finished its lane — proving its half before the next wave", "")
		if g, red := o.proveHalf(ctx, board, p, s); red {
			o.emit("verify", "team "+g.Team+"'s correction ticket rides the next wave", "")
		}
	}
}

// maxTeamTicketsPerDefect bounds how many times ONE failing acceptance is
// ticketed to its team per run: the first ticket and one retry.
const maxTeamTicketsPerDefect = 2

// raiseTeamTicket turns a red half into a ticket the owning team can work.
//
// Scoped to the team's own lane. A ticket for a broken Go build must not name
// the frontend's files: the wave's write deny list is derived from the task's
// squad, so a ticket carrying another team's paths is one the tool layer will
// refuse on exactly the files it was told to fix.
func (o *Orchestrator) raiseTeamTicket(board *plan.Board, s squads.Squad, cmd, summary, output string) {
	if board == nil {
		return
	}
	var hasRole func(string) bool
	if o.factory != nil {
		hasRole = o.factory.HasRole
	}
	// Only files this team owns. The command's output names whatever it names,
	// and half of that can belong to the other side of the seam.
	var mine []string
	for _, path := range squads.PathsIn(output) {
		if owner, ok := o.squadPlanNow().Owner(path); ok && owner == s.ID {
			mine = append(mine, path)
		}
	}

	in := plan.CorrectionInput{
		Source:   plan.SourceTester,
		Failures: trimFailures([]string{firstSentence(summary)}, 3),
		Summary:  "team " + s.ID + " does not pass its own acceptance",
		Command:  cmd,
		Output:   output,
		Files:    limitList(mine, 6),
		Squad:    s.ID,
	}
	key := plan.CorrectionKey(in)
	// Counted under the SAME key the ticket is stamped with. It used to be
	// looked up under a reduced key (source, command, squad) that no stamped
	// ticket ever matched, so every ticket announced itself as attempt 1 and
	// nothing could tell a first ticket from a tenth.
	in.Attempt = board.CorrectionAttempts(key)
	// The same red half on a second pass is the SAME defect. A second ticket
	// would make the board look like it is losing ground while one unresolved
	// break stacks tickets on every gate run.
	if board.NoteRepeatedRejection(key) > 0 {
		return
	}
	// And the same defect, ticketed, worked to done, and red AGAIN, is a
	// defect the team cannot fix on its own. The finish-path gate ran once
	// and was bounded by that; the between-wave gate is not, and without this
	// cap a half that stays red would be ticketed on every wave until the run
	// hit its ceiling — measured at 200 tickets in a test before the cap
	// existed. Two attempts per defect, then a human.
	if in.Attempt >= maxTeamTicketsPerDefect {
		o.emitWarn("verify", fmt.Sprintf("team %s is still RED after %d correction ticket(s) for the same "+
			"failure — not raising another; this half needs a human", s.ID, in.Attempt), "")
		return
	}
	nt := plan.NewCorrectionTicket(in, hasRole)
	plan.StampCorrectionKey(&nt, key)
	plan.StampCorrectionAttempt(&nt, in.Attempt+1)
	nt.Squad = s.ID
	board.AddTask(nt)
	o.persistBoard(board)
	o.emit("verify", "raised a correction ticket for team "+s.ID, "")
}

// settleSquadStamps clears a stamp that still straddles when the run is over.
//
// The stamp is a write permission during the run, and on the finished board it
// is also the answer to "which team did this?". A task whose files grew past
// its team's ownership mid-run keeps its stamp on purpose: the condition
// usually resolves before the next dispatch, and clearing at every save would
// throw away routing the plan established — RetargetAssignments moves a stamp,
// the wave fence clears it, and both are right where they are.
//
// Nothing is dispatched after this point, so a stamp that still straddles is
// not transient any more. It is the board telling a reader that a team owns
// work that team is fenced out of. Measured live: a task stamped
// `frontend-react` finished holding `cmd/server/main.go`, which `backend-go`
// owns — the wave had already refused it those files, and the board said
// otherwise.
func (o *Orchestrator) settleSquadStamps(board *plan.Board) {
	if o == nil || o.squadPlan == nil || board == nil {
		return
	}
	fixed := squads.RepairAssignments(o.squadPlan, board.Tasks)
	if len(fixed) == 0 {
		return
	}
	o.emitWarn("verify", fmt.Sprintf(
		"cleared the team stamp on %s — their files ended up spanning two teams, "+
			"so no single team owns that work and the board should not claim one does",
		strings.Join(limitList(fixed, 5), ", ")), "")
}

// allHalvesProved reports whether EVERY team that had work proved its own half
// by running its own acceptance command.
//
// This is measured evidence of the same kind the objective gate produces:
// harness-run commands, on the tree that exists, judged by their exit status
// rather than by a model's opinion of a write-up. It is what licenses a teams
// run to report success over an escalated task — see the escalation logic in
// completeRun, which already states the principle: when a planner's guess at a
// decomposition and a measurement disagree, the measurement wins.
//
// Three things it deliberately refuses to count:
//
//   - an UNVERIFIED gate. A command that could not run said nothing about that
//     half, and the whole point of separating UNVERIFIED from RED is that it is
//     not evidence in either direction. Treating it as proof here would undo
//     that distinction at the one place it decides the headline verdict.
//   - fewer than two proved halves. "Both halves proved" needs both halves to
//     exist and be proved; one team doing all the work is not a teams run.
//   - a run with no team plan at all.
//
// It says nothing about the SEAM, which is why it never produces a bare
// success: the caller keeps the escalation on the board, names it in the
// summary, and reports success_with_failures.
func (o *Orchestrator) allHalvesProved() bool {
	if o == nil || o.squadPlan == nil || len(o.squadPlan.Squads) < 2 {
		return false
	}
	gates := o.TeamGates()
	if len(gates) < 2 {
		return false
	}
	for _, g := range gates {
		if !g.Ran || !g.OK {
			return false
		}
	}
	return true
}

// provedHalfNames lists the teams whose own acceptance command went green, for
// the line that has to say WHY an escalation was walked past.
func (o *Orchestrator) provedHalfNames() []string {
	var out []string
	for _, g := range o.TeamGates() {
		if g.Ran && g.OK {
			out = append(out, g.Team)
		}
	}
	sort.Strings(out)
	return out
}
