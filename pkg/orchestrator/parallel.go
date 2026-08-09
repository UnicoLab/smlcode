package orchestrator

import (
	"context"
	"sync"
)

// phaseResult captures the outcome of one parallel phase run.
type phaseResult struct {
	name   string
	output string
	err    error
}

// runPhaseParallel runs each phase closure concurrently and returns a map
// of results keyed by phase name. All phases are waited on before returning.
func runPhaseParallel(ctx context.Context, phases ...func() phaseResult) map[string]phaseResult {
	results := make(map[string]phaseResult)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, fn := range phases {
		wg.Add(1)
		go func(f func() phaseResult) {
			defer wg.Done()
			r := f()
			mu.Lock()
			results[r.name] = r
			mu.Unlock()
		}(fn)
	}
	wg.Wait()
	return results
}
