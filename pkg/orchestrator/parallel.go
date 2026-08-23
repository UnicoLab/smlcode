package orchestrator

import (
	"context"
	"fmt"
	"sync"
)

// phaseResult captures the outcome of one parallel phase run.
type phaseResult struct {
	name   string
	output string
	err    error
}

// runPhaseParallel runs each phase closure concurrently and returns a map of
// results keyed by phase name. All phases are waited on before returning.
//
// ctx is honored, not decorative: it used to be accepted and never read, so
// the function's signature promised cancellation it could not deliver — a
// canceled run still blocked here until every phase finished on its own. A
// phase that has not started when ctx is already done is skipped, and a phase
// that finishes after cancellation has ctx.Err() folded into its result so the
// caller's `if r.err != nil` checks see the cancellation.
func runPhaseParallel(ctx context.Context, phases ...func() phaseResult) map[string]phaseResult {
	results := make(map[string]phaseResult)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, fn := range phases {
		wg.Add(1)
		go func(idx int, f func() phaseResult) {
			defer wg.Done()
			var r phaseResult
			if err := ctx.Err(); err != nil {
				// Never started: report the cancellation under a stable key so
				// the result map is not silently short.
				r = phaseResult{name: fmt.Sprintf("canceled-%d", idx), err: err}
			} else {
				r = f()
				if r.err == nil {
					if err := ctx.Err(); err != nil {
						r.err = err
					}
				}
			}
			mu.Lock()
			results[r.name] = r
			mu.Unlock()
		}(i, fn)
	}
	wg.Wait()
	return results
}

// canceledPhase returns the first cancellation error in a parallel result set.
func canceledPhase(results map[string]phaseResult) error {
	for _, r := range results {
		if r.err != nil && isCancelErr(r.err) {
			return r.err
		}
	}
	return nil
}
