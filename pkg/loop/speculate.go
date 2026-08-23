package loop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	ggagent "github.com/piotrlaczkowski/GoLangGraph/pkg/agent"
)

// SpecSlot is one speculative path (local probe or LLM role).
type SpecSlot struct {
	Role     string
	Prompt   string
	Required bool
	Local    func(ctx context.Context) (string, error)
	Timeout  time.Duration // per-slot timeout; if zero, falls back to r.Timeout
}

// SpecResult is the outcome of one speculative slot.
type SpecResult struct {
	Role    string
	Output  string
	Err     error
	Skipped bool
}

// speculate races slots (capped by maxParallel), canceling optional losers when
// required slots succeed — or on first optional success when none are required.
func (r *Runner) speculate(ctx context.Context, slots []SpecSlot) []SpecResult {
	if len(slots) == 0 {
		return nil
	}
	maxP := r.MaxParallel
	if maxP < 1 {
		maxP = 1
	}
	if maxP > len(slots) {
		maxP = len(slots)
	}

	type job struct {
		idx  int
		slot SpecSlot
	}
	jobs := make(chan job, len(slots))
	for i, s := range slots {
		jobs <- job{i, s}
	}
	close(jobs)

	results := make([]SpecResult, len(slots))
	var mu sync.Mutex
	requiredLeft := 0
	for _, s := range slots {
		if s.Required {
			requiredLeft++
		}
	}
	if requiredLeft == 0 {
		requiredLeft = -1
	}

	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for w := 0; w < maxP; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-gctx.Done():
					mu.Lock()
					if results[j.idx].Role == "" {
						results[j.idx] = SpecResult{Role: j.slot.Role, Skipped: true, Err: gctx.Err()}
					}
					mu.Unlock()
					continue
				default:
				}
				var out string
				var err error
				if j.slot.Local != nil {
					out, err = j.slot.Local(gctx)
				} else {
					if r.Executor == nil {
						err = fmt.Errorf("nil executor")
					} else {
						slotTimeout := r.Timeout
						if j.slot.Timeout > 0 {
							slotTimeout = j.slot.Timeout
						}
						// The racing reviewers stream too; the task id rides on
						// gctx (speculate is entered under r.taskCtx).
						stop := r.streamTokensCtx(gctx, j.slot.Role)
						res, e := r.Executor.ExecuteSubAgents(gctx, []ggagent.SubAgentRequest{{
							AgentID: j.slot.Role, Input: j.slot.Prompt,
							Timeout: slotTimeout, ShareState: true,
						}}, r.Shared)
						stop()
						err = e
						if len(res) > 0 {
							r.noteUsage(res[0], j.slot.Prompt, outputString(res[0]))
							out = outputString(res[0])
							if res[0].Error != nil && out == "" {
								err = res[0].Error
							}
						}
					}
				}
				mu.Lock()
				results[j.idx] = SpecResult{Role: j.slot.Role, Output: out, Err: err}
				if j.slot.Required && err == nil && strings.TrimSpace(out) != "" {
					requiredLeft--
					if requiredLeft == 0 {
						cancel()
					}
				}
				if requiredLeft < 0 && err == nil && strings.TrimSpace(out) != "" {
					requiredLeft = 0
					cancel()
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	for i := range results {
		if results[i].Role == "" {
			results[i] = SpecResult{Role: slots[i].Role, Skipped: true}
		}
	}
	return results
}

// acceptanceProbe repeatedly checks a predicate so disk acceptance can cancel a
// slower reviewer/tester LLM mid-flight.
func acceptanceProbe(ctx context.Context, win func() bool, approveJSON string) (string, error) {
	for i := 0; i < 8; i++ {
		if win() {
			return approveJSON, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("no acceptance")
}
