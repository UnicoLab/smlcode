package memory

import (
	"context"
	"strings"
	"testing"
)

// simulatedProject is a toy world with one non-obvious rule: the test command
// only works after the UI has been built. An agent that has to discover this
// every run wastes a command every run; an agent with semantic memory pays for
// the discovery once.
type simulatedProject struct{ uiBuilt bool }

func (p *simulatedProject) run(cmd string) bool {
	switch cmd {
	case "make ui":
		p.uiBuilt = true
		return true
	case "make test":
		return p.uiBuilt
	default:
		return false
	}
}

// agent picks commands, consulting memory when it has any.
func attemptRun(store *Store, useMemory bool) (wasted int, ep Episode) {
	proj := &simulatedProject{}
	ep = Episode{Query: "run the project test suite", Language: "go", Model: "qwen2.5-coder:14b"}

	var plan []string
	if useMemory {
		if f, ok := store.Semantic().Get(FactGotcha, "make-test-needs-ui"); ok && f.Confidence >= 0.6 {
			plan = []string{"make ui", "make test"}
		}
	}
	if len(plan) == 0 {
		// No memory: the naive order, which fails first.
		plan = []string{"make test", "make ui", "make test"}
	}

	for _, cmd := range plan {
		ok := proj.run(cmd)
		ep.Commands = append(ep.Commands, Command{Cmd: cmd, OK: ok})
		if !ok {
			wasted++
			ep.Failures = append(ep.Failures, FailureNote{
				Fingerprint: "make-test-needs-ui",
				Message:     "`make test` failed: UI assets missing",
				Resolution:  "run `make ui` before `make test`",
				ResolvedBy:  "retry",
			})
		}
	}
	ep.Success = proj.uiBuilt
	return wasted, ep
}

// TestMemoryMakesSuccessiveRunsCheaper is the load-bearing claim of the whole
// package: the same task, run repeatedly, must cost less because of what was
// remembered — with no LLM anywhere in the loop.
func TestMemoryMakesSuccessiveRunsCheaper(t *testing.T) {
	s, _, _ := testStore(t)

	var wastedPerRun []int
	for run := 0; run < 6; run++ {
		wasted, e := attemptRun(s, true)
		wastedPerRun = append(wastedPerRun, wasted)

		if err := s.RecordEpisode(e); err != nil {
			t.Fatal(err)
		}
		// The reflection step a harness would run: distillation turns the
		// resolved failure into a durable gotcha, with no LLM involved.
		if err := s.Distill(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}

	if wastedPerRun[0] == 0 {
		t.Fatalf("run 1 should have wasted a command before it knew anything: %v", wastedPerRun)
	}
	if wastedPerRun[len(wastedPerRun)-1] != 0 {
		t.Fatalf("the harness never learned; wasted per run = %v", wastedPerRun)
	}
	for i := 1; i < len(wastedPerRun); i++ {
		if wastedPerRun[i] > wastedPerRun[i-1] {
			t.Fatalf("performance regressed at run %d: %v", i+1, wastedPerRun)
		}
	}

	// And a control: with memory disabled, nothing improves.
	control, _, _ := testStore(t)
	var controlWaste []int
	for run := 0; run < 6; run++ {
		wasted, e := attemptRun(control, false)
		controlWaste = append(controlWaste, wasted)
		_ = control.RecordEpisode(e)
	}
	if controlWaste[0] != controlWaste[len(controlWaste)-1] {
		t.Fatalf("control run improved without memory (%v) — the test is not measuring memory", controlWaste)
	}
}

// TestRecalledContextGrowsMoreUsefulNotLarger guards the other half of the
// promise: memory must get more *useful*, not merely bigger. The injected
// block stays inside its budget however many runs accumulate.
func TestRecalledContextGrowsMoreUsefulNotLarger(t *testing.T) {
	s, _, _ := testStore(t)
	s.SetRunContext(RunContext{
		Query: "run the project test suite", Language: "go",
		Model: "qwen2.5-coder:14b", Role: "tester",
	})

	const budget = 350
	var sizes []int
	var sawGotcha bool
	for run := 0; run < 30; run++ {
		_, e := attemptRun(s, run > 0)
		_ = s.RecordEpisode(e)
		if err := s.Distill(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		out := s.RenderForPrompt("tester", budget)
		if n := countTokens(nil, out); n > budget {
			t.Fatalf("run %d rendered %d tokens, budget %d", run, n, budget)
		}
		if strings.Contains(out, "make ui") {
			sawGotcha = true
		}
		sizes = append(sizes, len(out))
	}
	if !sawGotcha {
		t.Error("the learned gotcha never made it into the injected block")
	}
	// Size must plateau, not climb with run count.
	if sizes[len(sizes)-1] > budget*bytesPerToken {
		t.Errorf("injected block grew to %d bytes after 30 runs", sizes[len(sizes)-1])
	}
	if s.Episodes().Count() > DefaultMaxEpisodes {
		t.Errorf("episodes grew unbounded: %d", s.Episodes().Count())
	}
}
