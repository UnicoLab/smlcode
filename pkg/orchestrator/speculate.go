package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// SpecSlot is one speculative specialist launch.
type SpecSlot struct {
	Role     string
	Prompt   string
	Required bool // required slots always waited for; optional losers are cancelled
}

// SpecResult is the outcome of one speculative slot.
type SpecResult struct {
	Role    string
	Output  string
	Err     error
	Skipped bool // cancelled before meaningful work / unused loser
}

// speculate launches slots in parallel (capped by max_parallel), cancels optional
// losers once all required slots finish successfully. Safe for SLMs: never exceeds
// MaxParallel concurrent role calls.
func (o *Orchestrator) speculate(ctx context.Context, slots []SpecSlot) []SpecResult {
	if len(slots) == 0 {
		return nil
	}
	maxP := o.cfg.MaxParallel
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
	var requiredLeft int
	for _, s := range slots {
		if s.Required {
			requiredLeft++
		}
	}
	if requiredLeft == 0 {
		// All optional: first success cancels the rest.
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
				o.emit("explore", fmt.Sprintf("speculative %s", j.slot.Role), "")
				out, err := o.runRoleTracked(gctx, j.slot.Role, "", j.slot.Prompt)
				mu.Lock()
				results[j.idx] = SpecResult{Role: j.slot.Role, Output: out, Err: err}
				if j.slot.Required && err == nil && strings.TrimSpace(out) != "" {
					requiredLeft--
					if requiredLeft == 0 {
						cancel() // cancel optional losers
					}
				}
				if requiredLeft < 0 && err == nil && strings.TrimSpace(out) != "" {
					// First optional winner cancels peers.
					requiredLeft = 0
					cancel()
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Mark unfinished as skipped.
	for i := range results {
		if results[i].Role == "" {
			results[i] = SpecResult{Role: slots[i].Role, Skipped: true}
		}
	}
	return results
}

// speculateDigs runs explorer (required) plus optional docs/architect when
// think_passes and max_parallel allow, cancelling optional losers when explorer wins.
func (o *Orchestrator) speculateDigs(ctx context.Context, query, explorePrompt string, inventory []string, wantDocs, wantArch bool) (exploreOut, archOut, docsOut string, err error) {
	slots := []SpecSlot{{
		Role: plan.RoleExplorer, Prompt: explorePrompt, Required: true,
	}}
	// Cap optional digs by remaining parallel capacity and think_passes.
	budget := o.cfg.MaxParallel - 1
	if budget < 0 {
		budget = 0
	}
	if o.cfg.ThinkPasses < 2 {
		budget = 0
	}
	if wantDocs && budget > 0 {
		docsPack, _ := o.packer.Build("docs", query,
			[]string{contextstore.DocProject, contextstore.DocContext}, nil, o.skillPackFor("docs", query))
		slots = append(slots, SpecSlot{
			Role: "docs", Required: false,
			Prompt: docsPack.Render() + "\nMap docs/conventions for this query. Return JSON.",
		})
		budget--
	}
	if wantArch && budget > 0 {
		archPack, _ := o.packer.Build("architect", query, contextstore.LeanDocsForRole("architect"), nil, o.skillPackFor("architect", query))
		hint := truncate(strings.Join(inventory, "\n"), 2000)
		slots = append(slots, SpecSlot{
			Role: "architect", Required: false,
			Prompt: archPack.Render() + "\nWorkspace files:\n" + hint + "\nReturn STRICT JSON design.",
		})
	}

	if len(slots) == 1 {
		exploreOut, err = o.runRoleTracked(ctx, plan.RoleExplorer, "", explorePrompt)
		return exploreOut, "", "", err
	}

	o.emit("explore", fmt.Sprintf("speculative digs (%d paths, max_parallel=%d)", len(slots), o.cfg.MaxParallel), "")
	for _, s := range slots {
		if s.Role != plan.RoleExplorer {
			o.emitAgent(s.Role, s.Role, "", "speculative "+s.Role, "", "")
		}
	}
	res := o.speculate(ctx, slots)
	for _, r := range res {
		switch r.Role {
		case plan.RoleExplorer:
			exploreOut, err = r.Output, r.Err
		case "docs":
			if !r.Skipped && strings.TrimSpace(r.Output) != "" {
				docsOut = r.Output
			}
		case "architect":
			if !r.Skipped && strings.TrimSpace(r.Output) != "" {
				archOut = r.Output
			}
		}
	}
	return exploreOut, archOut, docsOut, err
}
