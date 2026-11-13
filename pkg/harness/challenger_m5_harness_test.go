package harness

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// TestChallengerM5_HarnessNilReceiverDefensiveness asserts that all methods
// on *Harness defend against nil receivers without panicking.
func TestChallengerM5_HarnessNilReceiverDefensiveness(t *testing.T) {
	var h *Harness

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic on nil Harness receiver: %v", r)
		}
	}()

	if err := h.Init(); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("Init on nil Harness must return not initialized error, got: %v", err)
	}

	if err := h.EnsureInitialized(); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("EnsureInitialized on nil Harness must return not initialized error, got: %v", err)
	}

	if st, err := h.Status(); err == nil || st != "" {
		t.Errorf("Status on nil Harness must return error, got st=%q, err=%v", st, err)
	}

	if err := h.Close(); err != nil {
		t.Errorf("Close on nil Harness should be nil-safe idempotent no-op, got: %v", err)
	}

	if err := h.RebuildOrchestrator(); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("RebuildOrchestrator on nil Harness must return error, got: %v", err)
	}

	if res, err := h.Run(context.Background(), "test"); err == nil || res != nil {
		t.Errorf("Run on nil Harness must return error, got: %v", err)
	}

	if res, err := h.RunTeamwork(context.Background(), "test"); err == nil || res != nil {
		t.Errorf("RunTeamwork on nil Harness must return error, got: %v", err)
	}

	if res, err := h.Resume(context.Background(), "turn-123"); err == nil || res != nil {
		t.Errorf("Resume on nil Harness must return error, got: %v", err)
	}
}

// TestChallengerM5_ConcurrentRunTeamworkRaceStress exercises concurrent calls
// to RunTeamwork, Run, and Status on the same Harness under the Go race detector.
func TestChallengerM5_ConcurrentRunTeamworkRaceStress(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	h.Config.Calibrate = config.CalibrateOff
	if err := h.EnsureInitialized(); err != nil {
		t.Fatalf("EnsureInitialized failed: %v", err)
	}

	const goroutines = 20
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(3)

		// Goroutine A: RunTeamwork with pre-canceled context
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = h.RunTeamwork(ctx, "implement calculator")
		}(i)

		// Goroutine B: Status calls
		go func() {
			defer wg.Done()
			_, _ = h.Status()
		}()

		// Goroutine C: Run with pre-canceled context
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = h.Run(ctx, "implement calculator")
		}(i)
	}

	wg.Wait()
}

// TestChallengerM5_ConcurrentCloseAndRunSafety asserts that concurrently closing
// a harness while RunTeamwork is being invoked handles shutdown cleanly without panics.
func TestChallengerM5_ConcurrentCloseAndRunSafety(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	h.Config.Calibrate = config.CalibrateOff

	var wg sync.WaitGroup
	const callers = 10

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = h.RunTeamwork(ctx, "test query")
		}()
	}

	// Concurrent close
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = h.Close()
	}()

	wg.Wait()

	// Calling RunTeamwork after close must return clean error
	res, rerr := h.RunTeamwork(context.Background(), "query after close")
	if res != nil {
		t.Errorf("expected nil result on closed harness, got %v", res)
	}
	if rerr == nil || !strings.Contains(rerr.Error(), "closed") {
		t.Errorf("expected closed error, got: %v", rerr)
	}
}

// TestChallengerM5_ConfigRestorationInvariant asserts that cfg.Workflow and
// cfg.DynamicPipeline are always reverted to their pre-call values, even after
// errors or cancellations.
func TestChallengerM5_ConfigRestorationInvariant(t *testing.T) {
	tmp := t.TempDir()
	h, err := New(tmp)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	initialWorkflow := config.WorkflowFast
	initialDynamic := true
	h.Config.Workflow = initialWorkflow
	h.Config.DynamicPipeline = initialDynamic

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// 1. Pre-canceled context
	_, _ = h.RunTeamwork(ctx, "query")
	if h.Config.Workflow != initialWorkflow {
		t.Errorf("Workflow changed to %q, want %q", h.Config.Workflow, initialWorkflow)
	}
	if h.Config.DynamicPipeline != initialDynamic {
		t.Errorf("DynamicPipeline changed to %v, want %v", h.Config.DynamicPipeline, initialDynamic)
	}

	// 2. Empty query error
	_, _ = h.RunTeamwork(context.Background(), "")
	if h.Config.Workflow != initialWorkflow {
		t.Errorf("Workflow changed to %q after empty query", h.Config.Workflow)
	}
	if h.Config.DynamicPipeline != initialDynamic {
		t.Errorf("DynamicPipeline changed to %v after empty query", h.Config.DynamicPipeline)
	}
}
