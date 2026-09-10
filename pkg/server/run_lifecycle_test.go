package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// blockingRun is a fake engine: it blocks until its context is canceled or
// release is closed, then returns the given result.
func blockingRun(release <-chan struct{}, res *orchestrator.Result) func(context.Context, string) (*orchestrator.Result, error) {
	return func(ctx context.Context, _ string) (*orchestrator.Result, error) {
		select {
		case <-ctx.Done():
		case <-release:
		}
		return res, nil
	}
}

func latest(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/runs/latest", nil))
	if rec.Code != 200 {
		t.Fatalf("latest status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func startRun(t *testing.T, s *Server) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"query":"do a thing"}`)))
	return rec.Code
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Stop → Start used to race: handleStopRun cleared running while the old run
// goroutine was still unwinding, so a following POST /api/runs started a new
// run, and the OLD goroutine then cleared the new run's running flag,
// restored its options and published its own result over the new run's.
func TestStopThenStartDoesNotLeakOldRunState(t *testing.T) {
	t.Setenv("SLMCODE_NO_CALIBRATE", "1") // the fake engine must not wait behind a model probe
	h := newHarness(t)
	s := New(h, nil)
	defer func() { _ = s.Shutdown(context.Background()) }()

	// Run 1 blocks until stopped. The fake never observes the orchestrator's
	// own cancel, so a stop that reaches it proves the per-run context works.
	s.runFn = blockingRun(nil, &orchestrator.Result{Summary: "first"})
	if code := startRun(t, s); code != 200 {
		t.Fatalf("start #1 status=%d", code)
	}
	if l := latest(t, s); l["running"] != true || l["stopping"] != false {
		t.Fatalf("after start: %v", l)
	}

	// While run 1 is up, starting again is a conflict.
	if code := startRun(t, s); code != http.StatusConflict {
		t.Fatalf("concurrent start status=%d, want 409", code)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodPost, "/api/runs/stop", nil))
	if rec.Code != 200 {
		t.Fatalf("stop status=%d", rec.Code)
	}
	// running stays true until the goroutine exits; the fake returns as soon
	// as its context is canceled, so this resolves quickly.
	waitFor(t, "run 1 to unwind", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.running
	})
	if l := latest(t, s); l["running"] != false || l["stopping"] != false {
		t.Fatalf("after stop unwound: %v", l)
	}
	res1, _ := latest(t, s)["result"].(map[string]any)
	if res1 == nil || res1["summary"] != "first" {
		t.Fatalf("run 1 result not published: %v", res1)
	}

	// Run 2 blocks on its own release channel and must own the state.
	release2 := make(chan struct{})
	s.runFn = blockingRun(release2, &orchestrator.Result{Summary: "second"})
	if code := startRun(t, s); code != 200 {
		t.Fatalf("start #2 status=%d", code)
	}
	l := latest(t, s)
	if l["running"] != true {
		t.Fatalf("run 2 not running: %v", l)
	}
	// The snapshot is scoped to run 2: exactly one run_start, no run_end yet.
	starts, ends := 0, 0
	for _, ev := range l["events"].([]any) {
		switch ev.(map[string]any)["kind"] {
		case "run_start":
			starts++
		case "run_end", "run_stop":
			ends++
		}
	}
	if starts != 1 || ends != 0 {
		t.Fatalf("snapshot not scoped to the current run: starts=%d ends=%d", starts, ends)
	}

	// A goroutine from a STALE generation finishing late must not touch the
	// live run: running stays true, the result is not replaced, run_end is
	// not emitted.
	before := len(latest(t, s)["events"].([]any))
	s.finishRun(1, &orchestrator.Result{Summary: "stale"}, nil, savedRunOptions{}, "finished")
	l = latest(t, s)
	if l["running"] != true {
		t.Fatal("stale finishRun cleared running of the live run")
	}
	if res, _ := l["result"].(map[string]any); res != nil && res["summary"] == "stale" {
		t.Fatal("stale finishRun published its result over the live run")
	}
	if len(l["events"].([]any)) != before {
		t.Fatal("stale finishRun emitted run_end into the live run's stream")
	}

	close(release2)
	waitFor(t, "run 2 to finish", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.running
	})
	res2, _ := latest(t, s)["result"].(map[string]any)
	if res2 == nil || res2["summary"] != "second" {
		t.Fatalf("run 2 result: %v", res2)
	}
}

// A second click on Run while a run is in flight is a 409 — and must not
// sweep the in-flight run's pending asks. hitl.ClearAll used to run BEFORE
// the running check.
func TestStartConflictKeepsPendingAsks(t *testing.T) {
	t.Setenv("SLMCODE_NO_CALIBRATE", "1") // the fake engine must not wait behind a model probe
	h := newHarness(t)
	s := New(h, nil)
	defer func() { _ = s.Shutdown(context.Background()) }()
	slm := h.Config.SlmDir()

	release := make(chan struct{})
	defer close(release)
	s.runFn = blockingRun(release, nil)
	if code := startRun(t, s); code != 200 {
		t.Fatalf("start status=%d", code)
	}
	ask := workspace.ShellAsk{ID: "shell-7", Kind: "shell", Command: "go test ./...", TimeoutS: 120,
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := hitl.WriteAskID(slm, "shell", ask.ID, ask); err != nil {
		t.Fatal(err)
	}
	planAsk := plan.BuildPlanApproveAsk("ship it", &plan.Board{Tasks: []plan.Task{{ID: "T1", Title: "main"}}})
	if err := hitl.WriteAsk(slm, "plan", planAsk); err != nil {
		t.Fatal(err)
	}

	if code := startRun(t, s); code != http.StatusConflict {
		t.Fatalf("conflicting start status=%d, want 409", code)
	}
	if ids, _ := hitl.ListAskIDs(slm, "shell"); len(ids) != 1 {
		t.Fatalf("shell ask swept by the refused start: %v", ids)
	}
	if ok, _ := hitl.ReadAsk(slm, "plan", &plan.PlanApproveAsk{}); !ok {
		t.Fatal("plan ask swept by the refused start")
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/shell/pending", nil))
	var pending struct {
		Pending bool                 `json:"pending"`
		Ask     workspace.ShellAsk   `json:"ask"`
		Asks    []workspace.ShellAsk `json:"asks"`
		Count   int                  `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pending)
	if !pending.Pending || pending.Ask.ID != "shell-7" || pending.Count != 1 || len(pending.Asks) != 1 {
		t.Fatalf("shell pending shape: %s", rec.Body.String())
	}
}

// blockingWriter stalls on the first Write until released, standing in for a
// browser that opened GET /api/runs/latest and stopped reading.
type blockingWriter struct {
	hdr     http.Header
	release chan struct{}
	once    sync.Once
	started chan struct{}
}

func (w *blockingWriter) Header() http.Header { return w.hdr }
func (w *blockingWriter) WriteHeader(int)     {}
func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}

// handleLatestRun used to encode while holding s.mu; the orchestrator emits
// synchronously into Server.emit, which takes s.mu, so a stalled fetch blocked
// a worker mid tool-call.
func TestLatestRunDoesNotBlockEmit(t *testing.T) {
	t.Setenv("SLMCODE_NO_CALIBRATE", "1") // the fake engine must not wait behind a model probe
	h := newHarness(t)
	s := New(h, nil)
	defer func() { _ = s.Shutdown(context.Background()) }()
	for i := 0; i < 50; i++ {
		s.emit(orchestrator.Event{Phase: "execute", Kind: "output", Message: strings.Repeat("x", 512), Time: time.Now()})
	}
	bw := &blockingWriter{hdr: http.Header{}, release: make(chan struct{}), started: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		s.handleLatestRun(bw, newAPIRequest(http.MethodGet, "/api/runs/latest", nil))
		close(done)
	}()
	select {
	case <-bw.started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started writing")
	}
	emitted := make(chan struct{})
	go func() {
		s.emit(orchestrator.Event{Phase: "execute", Kind: "output", Message: "while stalled", Time: time.Now()})
		close(emitted)
	}()
	select {
	case <-emitted:
	case <-time.After(2 * time.Second):
		t.Fatal("emit blocked behind a stalled /api/runs/latest writer")
	}
	close(bw.release)
	<-done
}
