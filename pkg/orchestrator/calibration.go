package orchestrator

import (
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Seams for pkg/calibrate.
//
// Calibration measures a model server before the first role call and seeds
// what the harness would otherwise have to learn the slow way. It needs two
// things from here and nothing else: the store role budgets are derived from,
// and the list of roles worth seeding. The TIMEOUT POLICY itself
// (roleTimeoutFrom in roletimeout.go) is untouched — this only changes how
// much evidence it starts with, which is exactly the lever that turns "no
// latency measured yet (0/3 samples), use the whole ceiling" into an informed
// first run.

// LatencyStore exposes cross-project role-latency memory.
//
// Returns nil when the evolve engine is off (--no-evolve), in which case there
// is nothing to seed and callers must treat that as normal rather than an
// error.
func (o *Orchestrator) LatencyStore() *memory.Latencies {
	return o.latencyStore()
}

// ModelFamilyKey is the family latency memory is namespaced by, for the
// configured model. Exposed so a caller seeding the store keys it exactly the
// way the timeout policy will read it back.
func (o *Orchestrator) ModelFamilyKey() string {
	return o.modelFamily()
}

// SeedableRoles are the roles a calibration seed covers: every role the
// timeout policy classifies, so no role is left starting from zero evidence
// while its neighbors are informed.
func SeedableRoles() []string {
	return []string{
		plan.RoleWorker, plan.RoleExplorer, plan.RolePlanner, plan.RoleReviewer,
		plan.RoleCorrector, plan.RoleContext, plan.RoleTester, plan.RolePlaceholder,
		"coordinator", "splitter", "architect",
	}
}
