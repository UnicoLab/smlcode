package orchestrator

import (
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/loop"
)

// loopContract is the set of values this layer owns on loop.Runner.
//
// It is a named struct rather than six inline assignments so the contract with
// pkg/loop is legible in one place and testable without building a Runner.
type loopContract struct {
	// ContextLimitTokens is the model's context window; 0 = unknown.
	ContextLimitTokens int
	// MaxTaskCalls is the per-task LLM call budget; 0 => loop.DefaultMaxTaskCalls
	// (10 = worker + self-critique + MaxRetries × (review + correct) at the
	// default MaxRetries=4, so the budget does not silently cap max_retries).
	MaxTaskCalls int
	// ResolveRole maps a slot role id -> a registered agent id.
	ResolveRole func(string) string
	// OnTaskStart fires when a task begins, so the tool layer can reset that
	// task's CallTracker bucket.
	OnTaskStart func(taskID string)
	// Evolve is the self-improvement engine; may be nil.
	Evolve *evolve.Engine
	// MemoryTokens budgets the injected memory block; 0 => loop's default.
	MemoryTokens int
}

// applyLoopContract writes the contract onto a runner.
func applyLoopContract(runner *loop.Runner, c loopContract) {
	if runner == nil {
		return
	}
	runner.ContextLimitTokens = c.ContextLimitTokens
	runner.MaxTaskCalls = c.MaxTaskCalls
	runner.ResolveRole = c.ResolveRole
	runner.OnTaskStart = c.OnTaskStart
	runner.Evolve = c.Evolve
	runner.MemoryTokens = c.MemoryTokens
}

// drainRunnerEvolve pulls the failure events and bandit decisions the inner
// loop accumulated, clearing them so a second call cannot double-count.
func drainRunnerEvolve(runner *loop.Runner) (
	failures []evolve.FailureEvent,
	decisions []evolve.DecisionRecord,
) {
	if runner == nil {
		return nil, nil
	}
	return runner.DrainFailureEvents(), runner.DrainDecisionRecords()
}

// runnerEditStats is the run's edit-format accounting, read off the inner loop.
//
// pkg/loop owns the edit-format decision (evolve.DecEditFormat) and rewards its
// own bandit arm, but the RunReport is built HERE — so leaving these four
// fields unset is what made `slmcode metrics` print a 0/0 apply rate and a
// blank edit format for every run ever recorded. The numbers exist; they just
// never crossed the layer boundary.
func runnerEditStats(runner *loop.Runner) (format string, attempted, applied, firstAttempt int) {
	if runner == nil {
		return "", 0, 0, 0
	}
	attempted, applied, firstAttempt = runner.EditStats()
	return runner.EditFormat(), attempted, applied, firstAttempt
}
