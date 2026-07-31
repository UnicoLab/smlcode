package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// SpecSlot is one speculative specialist launch.
type SpecSlot struct {
	Role     string
	Prompt   string
	Required bool // required slots always waited for; optional losers are cancelled
	// Local, when set, runs in-process instead of an LLM role call (e.g. disk acceptance).
	Local func(ctx context.Context) (string, error)
	Phase string // observability phase label (explore/review/test); defaults to "speculate"
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
				phase := j.slot.Phase
				if phase == "" {
					phase = "speculate"
				}
				o.emit(phase, fmt.Sprintf("speculative %s", j.slot.Role), "")
				var out string
				var err error
				if j.slot.Local != nil {
					out, err = j.slot.Local(gctx)
				} else {
					out, err = o.runRoleTracked(gctx, j.slot.Role, "", j.slot.Prompt)
				}
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
		Role: plan.RoleExplorer, Prompt: explorePrompt, Required: true, Phase: "explore",
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
			Role: "docs", Required: false, Phase: "explore",
			Prompt: docsPack.Render() + "\nMap docs/conventions for this query. Return JSON.",
		})
		budget--
	}
	if wantArch && budget > 0 {
		archPack, _ := o.packer.Build("architect", query, contextstore.LeanDocsForRole("architect"), nil, o.skillPackFor("architect", query))
		hint := truncate(strings.Join(inventory, "\n"), 2000)
		slots = append(slots, SpecSlot{
			Role: "architect", Required: false, Phase: "explore",
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

// speculateTester races disk/rename acceptance (required when it can win) against
// one or more tester LLM strategies. Cancels optional tester losers on acceptance win
// or on the first decisive tester JSON when racing duplicates.
func (o *Orchestrator) speculateTester(ctx context.Context, query string, board *plan.Board, basePrompt string) (testOut string, fromDisk bool, err error) {
	diskOK := renameDiskOK(o.cfg.Root, query, board)
	if diskOK && o.cfg.MaxParallel < 2 {
		// Serial fast-path: no need to start tester at all.
		return `{"passed":true,"summary":"rename verified on disk","commands":[],"failures":[]}`, true, nil
	}

	slots := make([]SpecSlot, 0, 3)
	if diskOK || o.cfg.MaxParallel >= 2 {
		slots = append(slots, SpecSlot{
			Role: "disk-accept", Required: diskOK, Phase: "test",
			Local: func(ctx context.Context) (string, error) {
				// Poll briefly so a just-finished rename can win and cancel tester LLM.
				for i := 0; i < 6; i++ {
					if renameDiskOK(o.cfg.Root, query, board) {
						return `{"passed":true,"summary":"rename verified on disk","commands":[],"failures":[]}`, nil
					}
					select {
					case <-ctx.Done():
						return "", ctx.Err()
					case <-time.After(25 * time.Millisecond):
					}
				}
				return "", fmt.Errorf("no disk acceptance")
			},
		})
	}

	slots = append(slots, SpecSlot{
		Role: plan.RoleTester, Prompt: basePrompt, Required: !diskOK && o.cfg.MaxParallel < 2,
		Phase: "test",
	})

	// Duplicate tester strategy when think_passes and parallel capacity allow.
	budget := o.cfg.MaxParallel - len(slots)
	if budget < 0 {
		budget = 0
	}
	if o.cfg.ThinkPasses >= 2 && budget > 0 && !diskOK {
		strict := basePrompt + "\n\nSTRICT mode: cite task IDs + file paths in failures[]. Prefer passed=false when uncertain."
		slots = append(slots, SpecSlot{
			Role: "tester-strict", Prompt: strict, Required: false, Phase: "test",
		})
	}

	if len(slots) == 1 {
		out, e := o.runRoleTracked(ctx, plan.RoleTester, "", basePrompt)
		return out, false, e
	}

	o.emit("test", fmt.Sprintf("speculative tester race (%d paths, max_parallel=%d)", len(slots), o.cfg.MaxParallel), "")
	res := o.speculate(ctx, slots)

	var testerOut, strictOut, diskOut string
	var testerErr error
	for _, r := range res {
		switch r.Role {
		case "disk-accept":
			if !r.Skipped && strings.TrimSpace(r.Output) != "" && r.Err == nil {
				diskOut = r.Output
			}
		case plan.RoleTester:
			testerOut, testerErr = r.Output, r.Err
			if r.Skipped {
				testerErr = r.Err
			}
		case "tester-strict":
			if !r.Skipped && strings.TrimSpace(r.Output) != "" {
				strictOut = r.Output
			}
		}
	}
	if diskOut != "" {
		return diskOut, true, nil
	}
	// Prefer first decisive JSON among tester strategies.
	for _, cand := range []string{testerOut, strictOut} {
		if strings.TrimSpace(cand) == "" {
			continue
		}
		tr := plan.ParseTesterJSON(cand)
		if tr.Passed || len(tr.Failures) > 0 || strings.TrimSpace(tr.Summary) != "" {
			return cand, false, nil
		}
	}
	if strings.TrimSpace(testerOut) != "" {
		return testerOut, false, testerErr
	}
	if strings.TrimSpace(strictOut) != "" {
		return strictOut, false, nil
	}
	return testerOut, false, testerErr
}
