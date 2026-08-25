package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// runSpeculativeRace drives one full harness run with MaxParallel=4, which is
// the DEFAULT and the only setting that takes pkg/loop's speculative review
// race (reviewer + reviewer-strict + acceptance probe raced under one group
// context that the winner cancels).
//
// Two defects lived on exactly this path:
//
//  1. a loser's "context canceled" was string-matched by the orchestrator's
//     isCancelErr and reported as a user interrupt — the run aborted with
//     "interrupted at execute" and exit 130 with nobody having interrupted;
//  2. the winning reviewer's parsed verdict was dropped, so an approved:true
//     score:92 payload was rendered and acted upon as approved=false score=0,
//     costing a correction round the model never needed.
//
// Both are races, so the callers below repeat the run.
//
// Scope, stated plainly: this is the WHOLE-PATH net, not the reducer. It drives
// the shipped default configuration through the race repeatedly and asserts the
// run finishes with the verdict the model actually gave. The deterministic
// reproducers — a loser that returns a partial stream AND a wrapped
// `context canceled`, which this fake provider's client discards before
// pkg/loop sees it — live in pkg/loop/speculate_cancel_test.go, where the
// executor is the fake and both halves can be produced on demand. Keep both:
// the unit tests fail loudly if the handling regresses, this one fails if the
// default configuration stops finishing.
func runSpeculativeRace(t *testing.T) (*orchestrator.Result, []stream.Event, map[string]int, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module demo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	model := &fakeModel{}
	// The reviewer-strict slot answers immediately and the plain reviewer is
	// held back, so the race really does cancel a loser MID-STREAM on every
	// run. Without this the fake is fast enough that both racers finish before
	// the winner's cancel lands and neither defect can appear.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		text := string(body)
		if strings.Contains(text, `{"approved"`) && !strings.Contains(text, "STRICT:") {
			// Stream the first half of the verdict, then stall. A racer the
			// winner cancels therefore comes back exactly as a real streaming
			// provider leaves it: a truncated `{"approved":true,"score":92,`
			// body AND a wrapped `context canceled` error. Both halves are
			// load-bearing — the body is what used to be read as the verdict,
			// the error is what used to be reported as a user interrupt.
			if strings.Contains(text, `"stream":true`) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", chunkJSON(`{"approved":true,"score":92,"summ`))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			select {
			case <-r.Context().Done():
				return // canceled loser: the server never finishes, like a real one
			case <-time.After(3 * time.Second):
				return
			}
		}
		model.ServeHTTP(w, r)
	}))
	defer server.Close()

	cfg := config.Default(root)
	cfg.Provider = "openai"
	cfg.Endpoint = server.URL + "/v1"
	cfg.Model = fakeModelID
	cfg.APIKey = "test-key"
	cfg.StructuredDecoding = "off"
	cfg.DynamicPipeline = false
	cfg.ClarifyMode = "off"
	cfg.PlanApprove = "auto"
	cfg.ContinueAsk = "off"
	cfg.EscalateAsk = "off"
	cfg.QAGate = false
	cfg.PostWorkerSmoke = false
	cfg.RequireSmoke = false
	cfg.ScopeJudge = false
	cfg.PlaceholderPass = false
	cfg.ThinkPasses = 1
	// The default. >=3 also enables the reviewer-strict slot.
	cfg.MaxParallel = 4
	cfg.MaxRetries = 1
	cfg.TaskTimeout = 30 * time.Second
	cfg.Normalize()

	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []stream.Event
	orch.OnEvent(func(ev stream.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		t.Fatalf("install orchestrator: %v", cerr)
	}
	defer func() { _ = h.Close() }()

	// FIVE MINUTES, AND IT IS A HANG DETECTOR, NOT A PERFORMANCE BUDGET.
	//
	// Everything this test asserts is about BEHAVIOR — that a racer the harness
	// itself canceled is never reported as a user interrupt, and that the
	// winner's verdict survives. None of it is about speed: the model here is a
	// fake, so wall-clock measures the machine, not the code.
	//
	// It was 90s, and 90s is under the wire. `make check` runs every package in
	// parallel with coverage instrumentation, and each of these tests drives six
	// full pipeline runs; on 2026-08-25 both failed the gate with "context
	// deadline exceeded" while passing in isolation (110s) and under coverage
	// alone (137s). That is a red gate caused by load, on a change that touched
	// none of this — and a gate that goes red for unrelated reasons teaches
	// everyone to re-run it until it goes green, which is how a real regression
	// gets waved through.
	//
	// Raising a bound that only exists to catch a hang does not weaken any
	// assertion here. If the pipeline ever genuinely hangs, five minutes still
	// catches it; the surrounding `go test -timeout` catches it regardless.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := h.Run(ctx, "Create "+targetFile+" with a Hello function")
	mu.Lock()
	out := append([]stream.Event(nil), events...)
	mu.Unlock()
	_, byRole := model.counts()
	return res, out, byRole, err
}

// TestSpeculativeReviewRaceCompletes is the regression net for defect 1: a
// racer that the harness itself canceled must never be reported as a user
// interrupt. It was reproducible 6/6 in one project and ~1/3 in another, so
// this repeats.
func TestSpeculativeReviewRaceCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e")
	}
	const runs = 6
	for i := 0; i < runs; i++ {
		res, events, _, err := runSpeculativeRace(t)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if res == nil {
			t.Fatalf("run %d: nil result", i)
		}
		if strings.Contains(strings.ToLower(res.Summary), "interrupt") {
			t.Fatalf("run %d: phantom interrupt — summary %q", i, res.Summary)
		}
		if !res.Success {
			t.Fatalf("run %d: run did not succeed: %s", i, res.Summary)
		}
		for _, task := range res.Board.Tasks {
			if task.Column != plan.ColDone {
				t.Fatalf("run %d: task %s ended in %q (%s)", i, task.ID, task.Column, task.Error)
			}
		}
		for _, ev := range events {
			if strings.Contains(strings.ToLower(ev.Message), "interrupted at") {
				t.Fatalf("run %d: phantom interrupt event: %s", i, ev.Message)
			}
		}
	}
}

// TestSpeculativeReviewKeepsWinnersVerdict is the regression net for defect 2:
// the fake reviewer always answers approved:true score:92, so any
// approved=false verdict on this path means the winner's parsed result was
// dropped.
func TestSpeculativeReviewKeepsWinnersVerdict(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e")
	}
	for i := 0; i < 4; i++ {
		_, events, byRole, err := runSpeculativeRace(t)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		for _, ev := range events {
			if !strings.Contains(ev.Message, "review approved=") {
				continue
			}
			if strings.Contains(ev.Message, "approved=false") {
				t.Fatalf("run %d: reviewer verdict dropped: %q (payload %q)",
					i, ev.Message, ev.Output)
			}
		}
		// The cost of reading a cut-short racer as a verdict, in order of how
		// much it hurts: the harness sees a truncated `{"approved":true,` and
		// re-asks the reviewer (one extra round-trip on a server that
		// serializes inference), and when the re-ask does not save it, a
		// correction round the model never asked for. The fake reviewer
		// approves unconditionally, so either signal means the winner's verdict
		// was dropped.
		for _, ev := range events {
			if strings.Contains(ev.Message, "cut off by max_tokens") {
				t.Fatalf("run %d: a canceled racer's partial stream was read as a verdict: %q (roles: %v)",
					i, ev.Message, byRole)
			}
			if ev.Kind == stream.KindAgentStart && strings.Contains(ev.Message, "correction pass") {
				t.Fatalf("run %d: a correction round was bought by a dropped verdict (roles: %v)",
					i, byRole)
			}
		}
	}
}

// chunkJSON renders one OpenAI streaming delta carrying content.
func chunkJSON(content string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "cmpl-partial", "object": "chat.completion.chunk", "model": fakeModelID,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{"role": "assistant", "content": content},
		}},
	})
	return string(b)
}
