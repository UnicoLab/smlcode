package orchestrator

import (
	"context"
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
}

func (t *teamGates) set(g TeamGate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = map[string]TeamGate{}
	}
	t.by[g.Team] = g
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
	if o == nil || o.squadPlan == nil || board == nil || len(o.squadPlan.Squads) < 2 {
		return nil
	}

	progress := map[string]squads.Status{}
	for _, st := range squads.Progress(o.squadPlan, board.Tasks) {
		progress[st.ID] = st
	}

	var failed []string
	for _, s := range o.squadPlan.Squads {
		st := progress[s.ID]
		// A team with no work did nothing to prove. Running its acceptance
		// would report on the state of the repository, not on this run.
		if st.Total == 0 {
			continue
		}
		if ctx.Err() != nil {
			return failed
		}

		cmd := strings.TrimSpace(s.Acceptance)
		if cmd == "" {
			o.teamGates.set(TeamGate{Team: s.ID, Summary: "no acceptance command — this half was never proved"})
			o.emitWarn("verify", "team "+s.ID+" has no acceptance command — its half is unproved, "+
				"so a break in it can only surface at integration", "")
			continue
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
		case res.OK:
			o.emit("verify", "team "+s.ID+" is green: "+cmd, "")
			o.recordGate("team:"+s.ID, true, "")
		default:
			failed = append(failed, s.ID)
			o.emitWarn("verify", "team "+s.ID+" is RED — its own half does not pass: "+res.Summary, res.Output)
			o.recordGate("team:"+s.ID, false, res.Summary)
			o.raiseTeamTicket(board, s, cmd, res.Summary, res.Output)
		}
	}
	return failed
}

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
		if owner, ok := o.squadPlan.Owner(path); ok && owner == s.ID {
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
		Attempt: board.CorrectionAttempts(plan.CorrectionKey(plan.CorrectionInput{
			Source: plan.SourceTester, Command: cmd, Squad: s.ID,
		})),
	}
	key := plan.CorrectionKey(in)
	// The same red half on a second pass is the SAME defect. A second ticket
	// would make the board look like it is losing ground while one unresolved
	// break stacks tickets on every gate run.
	if board.NoteRepeatedRejection(key) > 0 {
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
