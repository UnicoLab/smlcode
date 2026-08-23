package orchestrator

import (
	"context"
	"fmt"
	"strings"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/quality"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// runPlaceholderPass scans for stubs, asks the placeholder specialist to fill
// them, then re-scans. Remaining gaps are written to SCRATCH and returned for
// HITL continue prompts.
func (o *Orchestrator) runPlaceholderPass(ctx context.Context, query string, board *plan.Board, runner *loop.Runner) []quality.PreciseGap {
	if o == nil || o.cfg == nil || !o.cfg.PlaceholderPass {
		return nil
	}
	gaps := quality.ScanProjectPlaceholders(o.cfg.Root, board)
	if len(gaps) == 0 {
		o.emit("polish", "placeholder scan clean — no stubs found", "")
		return nil
	}

	report := quality.FormatPlaceholderReport(gaps)
	_ = o.store.Append(contextstore.DocScratch, "Placeholder scan", report)
	o.emitFull("polish", stream.KindIntervention, plan.RolePlaceholder, "",
		fmt.Sprintf("found %d placeholder gap(s) — attempting fill", len(gaps)),
		quality.InterventionReview, truncate(report, 1200))

	o.emitAgent("polish", plan.RolePlaceholder, "", "fill placeholder gaps", "", "")
	pack, _ := o.packBuild(plan.RolePlaceholder, query,
		contextstore.DefaultDocsForRole(plan.RolePlaceholder), nil,
		o.skillPackFor(plan.RolePlaceholder, query))
	prompt := pack.Render() + "\n" + report +
		"\n\n## Goal\nFill EVERY listed gap with real working code using ws_edit/ws_patch.\n" +
		"## Project language\n" + o.langHint() +
		"\n\nRe-smoke touched files. Return STRICT JSON status.\n" +
		"Query: " + truncate(query, 800)
	out, err := o.runRoleTracked(ctx, plan.RolePlaceholder, "", prompt)
	if strings.TrimSpace(out) != "" {
		o.emitFull("polish", stream.KindOutput, plan.RolePlaceholder, "",
			"placeholder agent output", "", truncate(out, 1200))
		_ = o.store.Append(contextstore.DocScratch, "Placeholder fill", truncate(out, 4000))
	}
	if err != nil {
		o.emit("polish", "placeholder agent warning: "+err.Error(), "")
	}

	// Reopen tasks that still touch remaining gap files so a continue wave can fix them.
	remaining := quality.ScanProjectPlaceholders(o.cfg.Root, board)
	if len(remaining) == 0 {
		o.emit("polish", "all placeholder gaps filled", "")
		return nil
	}

	reopenPlaceholderTasks(board, remaining)
	o.persistBoard(board)
	if runner != nil && board.AgentWorkRemaining() {
		o.emit("polish", "corrective wave for remaining placeholder gaps", "")
		ran, werr := runner.RunCorrectiveBoard(ctx, board)
		if !ran {
			o.emit("polish", "placeholder corrective wave skipped — max_waves budget exhausted", "")
		}
		if werr != nil && !isCancelErr(werr) {
			o.emit("polish", "placeholder corrective wave warning: "+werr.Error(), "")
		}
		snap := o.boardStore.Snapshot()
		*board = snap
		o.persistBoard(board)
		remaining = quality.ScanProjectPlaceholders(o.cfg.Root, board)
	}

	if len(remaining) > 0 {
		rep := quality.FormatPlaceholderReport(remaining)
		_ = o.store.Append(contextstore.DocScratch, "Placeholder gaps remaining", rep)
		o.emitFull("polish", stream.KindIntervention, plan.RolePlaceholder, "",
			fmt.Sprintf("%d gap(s) still need precise fill", len(remaining)),
			quality.InterventionEscalate, truncate(rep, 1200))
		o.emitLoop("polish", LoopEvent{
			Action:   "placeholder_gaps",
			Reason:   fmt.Sprintf("%d placeholder gap(s) remain — may restart scope/execute", len(remaining)),
			Failures: gapLines(remaining),
			From:     "polish",
			To:       "execute",
		})
	}
	return remaining
}

func reopenPlaceholderTasks(board *plan.Board, gaps []quality.PreciseGap) {
	if board == nil || len(gaps) == 0 {
		return
	}
	byPath := map[string]string{}
	for _, g := range gaps {
		byPath[g.Path] = g.Reason
	}
	for i := range board.Tasks {
		t := &board.Tasks[i]
		t.Normalize()
		if t.Role != plan.RoleWorker && t.Role != plan.RoleCorrector && t.Role != "deep" &&
			t.Role != plan.RolePlaceholder {
			continue
		}
		hit := ""
		for _, f := range t.Files {
			if why, ok := byPath[f]; ok {
				hit = f + ": " + why
				break
			}
		}
		if hit == "" {
			continue
		}
		t.Notes = strings.TrimSpace(t.Notes + "\nPLACEHOLDER GAP: " + hit)
		t.Error = ""
		t.Review = "reopened: placeholder/stub still present"
		t.MoveTo(plan.ColReadyToDev)
		board.Tasks[i] = *t
	}
}

func gapLines(gaps []quality.PreciseGap) []string {
	var out []string
	for _, g := range gaps {
		loc := g.Path
		if g.Line > 0 {
			loc = fmt.Sprintf("%s:%d", g.Path, g.Line)
		}
		out = append(out, loc+" — "+g.Reason)
	}
	return out
}
