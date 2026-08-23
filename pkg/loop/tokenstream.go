package loop

import (
	"context"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// Live token streaming: the loop side of the wiring.
//
// pkg/backends tees a provider's stream deltas to whatever sink is registered
// for a (role, task) pair; Runner.TokenSink turns those deltas into
// stream.KindToken events. This file is the only thing that connects the two,
// and it does so around every single place the loop drives an agent:
//
//   - dispatchWave   — the parallel worker wave (max_parallel agents at once)
//   - execOne        — every sequential round-trip (review, correct, critique…)
//   - speculate      — the speculative review race
//
// Registration is always scoped by a deferred unregister, so a cancelled or
// failed call leaks nothing. That matters more than it looks: the sink closes
// over the task id, so a leaked registration would keep attributing a later
// agent's output to a task that already finished.

// streamTokens registers this runner's token sink for one agent/task pair and
// returns the cleanup function. It is a no-op — and costs nothing — when no
// event consumer is attached, which is the case for most tests and for any
// embedder that only wants the final result.
//
// ALWAYS use it as `defer r.streamTokens(agent, taskID)()`.
func (r *Runner) streamTokens(agent, taskID string) func() {
	if r == nil || agent == "" || (r.OnEventFull == nil && r.OnEvent == nil) {
		return func() {}
	}
	return backends.RegisterTokenSink(agent, taskID, r.TokenSink(agent, taskID))
}

// streamTokensCtx is streamTokens for the call sites that carry the task id on
// the context rather than in a request struct (the speculative review race
// builds its requests without a TaskID field).
func (r *Runner) streamTokensCtx(ctx context.Context, agent string) func() {
	return r.streamTokens(agent, workspace.TaskIDFrom(ctx))
}
