package orchestrator

import (
	"context"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/loop"
	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// ── One run-wide concurrency limit ───────────────────────────────────────
//
// max_parallel is the measured concurrency knee of the model endpoint
// (calibration, docs/calibration.md), and for the fastest local models it is
// often 1: a second in-flight request does not run alongside the first, it
// queues behind it and inflates BOTH latencies. The knee used to be enforced in
// exactly one place — the execute wave — while everything else fanned out on
// its own: context ran beside explore, architect beside clarify, the explore
// phase's speculative digs added slots, review races added slots, and a wave's
// reviews multiplied them. On a max_parallel=1 endpoint that is not a
// throughput loss, it is a latency loss on every request the harness makes,
// and the measured role timeouts were then blown by queueing the harness
// itself created.
//
// The gate is a weighted semaphore of max_parallel slots, acquired in the ONE
// adapter every model request passes through: runRoleTracked (phase roles),
// the executor handed to loop.NewRunner (workers, reviewers, correctors,
// critique, triage, speculative reviews) and the multipass runner (planner
// and splitter). Phase pairs that used to be unconditionally parallel also
// run sequentially when max_parallel is 1 — see runPhases.

// runGate is the run-wide semaphore. A nil gate admits everything.
type runGate struct {
	slots chan struct{}
	size  int
}

// newRunGate sizes the gate. n < 1 is normalized to 1: a zero-valued limit
// must mean "one at a time", never "nothing may run".
func newRunGate(n int) *runGate {
	if n < 1 {
		n = 1
	}
	return &runGate{slots: make(chan struct{}, n), size: n}
}

// acquire takes weight slots, clamped to [1, size] so a batch larger than the
// gate cannot deadlock on itself, and returns the matching release. It
// returns ctx.Err() when the run is canceled while waiting, in which case
// nothing is held and release is a no-op.
func (g *runGate) acquire(ctx context.Context, weight int) (release func(), err error) {
	if g == nil {
		return func() {}, nil
	}
	if weight < 1 {
		weight = 1
	}
	if weight > g.size {
		weight = g.size
	}
	held := 0
	release = func() {
		for ; held > 0; held-- {
			<-g.slots
		}
	}
	for held < weight {
		if ctx == nil {
			g.slots <- struct{}{}
			held++
			continue
		}
		select {
		case g.slots <- struct{}{}:
			held++
		case <-ctx.Done():
			release()
			return func() {}, ctx.Err()
		}
	}
	return release, nil
}

// runGateFor returns this run's gate, building it from max_parallel on first
// use. Run and Resume rebuild it at their start so a config patch between runs
// (Studio, stacks) takes effect; tests that call role helpers directly get a
// lazily built one.
func (o *Orchestrator) runGateFor() *runGate {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.gate == nil {
		o.gate = newRunGate(o.maxParallelLocked())
	}
	return o.gate
}

// resetRunGate rebuilds the gate for a new run. o.mu must NOT be held.
func (o *Orchestrator) resetRunGate() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.gate = newRunGate(o.maxParallelLocked())
	o.mu.Unlock()
}

// maxParallelLocked is the configured knee, floored at 1. o.mu held or not
// held both work: it reads config only.
func (o *Orchestrator) maxParallelLocked() int {
	if o == nil || o.cfg == nil || o.cfg.MaxParallel < 1 {
		return 1
	}
	return o.cfg.MaxParallel
}

// gatedExecutor is the adapter every loop-side model request passes through:
// it holds one gate slot per request for the duration of the call and folds
// the real number of LLM round-trips each result represents into the run's
// llm_requests tally.
type gatedExecutor struct {
	inner loop.SubAgentRunner
	gate  *runGate
	// onRequests receives the number of LLM round-trips one result stood for.
	onRequests func(n int)
}

// ExecuteSubAgents implements loop.SubAgentRunner.
func (e *gatedExecutor) ExecuteSubAgents(ctx context.Context, reqs []ggagent.SubAgentRequest,
	shared *ggagent.SharedState) ([]ggagent.SubAgentResult, error) {
	release, err := e.gate.acquire(ctx, len(reqs))
	if err != nil {
		return nil, err
	}
	defer release()
	res, err := e.inner.ExecuteSubAgents(ctx, reqs, shared)
	if e.onRequests != nil {
		for _, r := range res {
			e.onRequests(llmRequestsIn(r))
		}
	}
	return res, err
}

// gatedExecutor returns the run-gated view of the orchestrator's executor, or
// a nil interface when there is no executor at all (the loop treats a nil
// Executor as "nothing can be dispatched", and a non-nil wrapper around nil
// would turn that into a panic).
func (o *Orchestrator) gatedExecutor() loop.SubAgentRunner {
	if o == nil || o.executor == nil {
		return nil
	}
	return &gatedExecutor{inner: o.executor, gate: o.runGateFor(), onRequests: o.bumpLLMCalls}
}

// llmRequestsIn counts the LLM round-trips one sub-agent result cost: every
// assistant turn of its ReAct transcript is one request to the model. A result
// with no transcript (a fake, a provider that returns none) counts as one, so
// the tally can only ever be corrected upward from the old one-per-result.
//
// This is the number the operator waited on. One worker result used to be
// counted as ONE call while its transcript held eight tool turns, so a run
// that made 200 requests reported 40 and the queueing math in the calibration
// report was off by the same factor.
func llmRequestsIn(res ggagent.SubAgentResult) int {
	n := 0
	for _, m := range res.Messages {
		if strings.EqualFold(strings.TrimSpace(m.Role), "assistant") {
			n++
		}
	}
	if n < 1 {
		return 1
	}
	return n
}
