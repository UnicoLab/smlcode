package orchestrator

import (
	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/loop"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// frontendInventoryLimit bounds the workspace scan behind the frontend choice.
//
// The question is only "does this project already have React components, and
// did it adopt a component library" — both answered by the first page of a
// prioritized inventory, which puts manifests and source roots first.
const frontendInventoryLimit = 64

// chooseFrontendMethod decides whether this run assembles UI from a component
// library or writes components by hand, and tells the operator which.
//
// Announced rather than silent, and announced with the way to change it: this
// is the one decision here that a person may simply disagree with. A run that
// quietly installed shadcn into someone's project would be the harness making a
// dependency choice on their behalf without saying so.
func (o *Orchestrator) chooseFrontendMethod(runner *loop.Runner, query string) {
	if o == nil || runner == nil || runner.HasRole == nil {
		return
	}
	inventory := plan.ListWorkspaceFiles(o.cfg.Root, frontendInventoryLimit)
	choice := agents.ChooseFrontend(query, inventory, !agents.HasReactFiles(inventory), runner.HasRole)
	if choice.Worker == "" {
		// Only worth a line when there was a real fork in the road. An
		// unrelated Go or Python run must not be told about component
		// libraries it will never touch.
		if choice.FromQuery {
			o.emit("init", "frontend: writing components by hand — "+choice.Why, "")
		}
		return
	}
	runner.FrontendAssembler = choice.Worker
	o.emit("init", "frontend: "+choice.Worker+" — "+choice.Why, "")
}
