package orchestrator

import (
	"context"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// ArchitectEditorArm is the bandit arm name for the paired pipeline.
const (
	ArchEditorOn  = "architect_editor"
	ArchEditorOff = "solo_worker"
)

// architectEditorApplies reports whether a task is a candidate for the pair.
//
// Only implementation roles: a describer→editor split exists to separate
// "decide what the change is" from "emit it in the edit format", which is only
// a real division of labor when there is an edit to emit.
func architectEditorApplies(t plan.Task) bool {
	switch strings.ToLower(strings.TrimSpace(t.Role)) {
	case plan.RoleWorker, plan.RoleCorrector, "deep", agents.RoleEditor, "":
		return len(t.Files) > 0
	default:
		return false
	}
}

// useArchitectEditor decides whether THIS task runs paired.
//
// Off by default (it doubles the round-trips for a task), and even when
// enabled the bandit gets the choice, so a project where the pair does not pay
// stops paying for it.
func (o *Orchestrator) useArchitectEditor(t plan.Task) bool {
	if o == nil || !o.architectEditorEnabled() || !architectEditorApplies(t) {
		return false
	}
	if !o.knownAgent(agents.RoleDescriber) || !o.knownAgent(agents.RoleEditor) {
		return false
	}
	if o.evolve == nil {
		return true
	}
	return o.choose(evolve.DecEditFormat, ArchEditorOn, ArchEditorOff) == ArchEditorOn
}

// taskBrief is the task text handed to each half of the pair.
func taskBrief(t plan.Task) string {
	var b strings.Builder
	b.WriteString(t.Title)
	if d := strings.TrimSpace(t.Description); d != "" {
		b.WriteString("\n\n")
		b.WriteString(d)
	}
	if a := strings.TrimSpace(t.Acceptance); a != "" {
		b.WriteString("\n\nAcceptance:\n")
		b.WriteString(a)
	}
	if len(t.Files) > 0 {
		b.WriteString("\n\nFocus files (HARD SCOPE):\n- ")
		b.WriteString(strings.Join(t.Files, "\n- "))
	}
	return b.String()
}

// The pair exists because one model that must simultaneously solve the problem
// AND conform to an edit format divides its attention between the two; Aider
// measured every model tested scoring substantially higher paired than solo.
//
// applyArchitectEditorRoles retags eligible implementation tasks to the editor
// half of the pair, so the runner builds the EDITOR agent for them (its own
// model, constrained decoding, edit tools) and buildTaskInput runs the
// describer first. Retagging is how the pair gets two independently selectable
// models rather than one model doing both jobs in one prompt.
//
// Returns the number of tasks retagged.
func (o *Orchestrator) applyArchitectEditorRoles(board *plan.Board) int {
	if o == nil || board == nil || !o.architectEditorEnabled() {
		return 0
	}
	n := 0
	for i := range board.Tasks {
		if board.Tasks[i].Role == agents.RoleEditor {
			continue
		}
		if !o.useArchitectEditor(board.Tasks[i]) {
			continue
		}
		board.Tasks[i].Notes = strings.TrimSpace(board.Tasks[i].Notes +
			"\narchitect/editor pair: describer reasons, editor applies")
		board.Tasks[i].Role = agents.RoleEditor
		n++
	}
	if n > 0 {
		o.emit("execute", itoa(n)+" task(s) routed through the architect/editor pair", "")
	}
	return n
}

// describeForEditor runs the describer half and returns the editor-framed
// input, or "" when the pair could not run (caller keeps the solo prompt).
func (o *Orchestrator) describeForEditor(ctx context.Context, query string, t plan.Task) string {
	describer, _ := agents.ArchitectEditorPair()
	if o == nil || !o.knownAgent(describer) {
		return ""
	}
	pack, _ := o.packer.BuildPack(contextstore.BuildRequest{
		Role:            describer,
		Query:           query,
		TaskID:          t.ID,
		TaskTitle:       t.Title,
		TaskDescription: t.Description,
		Acceptance:      t.Acceptance,
		Docs:            contextstore.LeanDocsForRole(describer),
		Files:           t.Files,
		SkillsMarkdown:  o.skillPackFor(describer, query),
		FocusTerms:      focusTermsFor(t),
	})
	o.emitAgent("execute", describer, t.ID, "describing the change (no tools, no format)",
		strings.Join(t.Files, ", "), "")
	description, err := o.runRoleTracked(taskContext(ctx, t.ID), describer, t.ID,
		pack.Render()+"\n## Task\n\n"+taskBrief(t)+
			"\n\nDescribe the change to make, in prose. Do not write the edits.")
	if err != nil || strings.TrimSpace(description) == "" {
		o.emitWarn("execute", "describer produced nothing — editor works from the task alone", t.ID)
		return ""
	}
	o.emitFull("execute", stream.KindOutput, describer, t.ID, "change described", "",
		truncate(description, 1200))
	return agents.EditorInput(taskBrief(t), description)
}
