package orchestrator

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

func TestEmitLoopIncrementsWaveAndPayload(t *testing.T) {
	cfg := config.Default(t.TempDir())
	o := &Orchestrator{cfg: cfg}
	var mu sync.Mutex
	var events []stream.Event
	o.OnEvent(func(e stream.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	o.emitLoop("test", LoopEvent{
		Action:   "tester_reject",
		Reason:   "missing main.py",
		Failures: []string{"missing main.py", "bad import"},
		From:     "test",
		To:       "plan",
	})
	o.emitLoop("execute", LoopEvent{
		Action: "corrective_wave",
		Reason: "running fixes",
		From:   "test",
		To:     "execute",
		Wave:   o.waveCounter,
	})

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 loop events, got %d", len(events))
	}
	if events[0].Kind != stream.KindLoop {
		t.Fatalf("kind=%q want loop", events[0].Kind)
	}
	var ev0 LoopEvent
	if err := json.Unmarshal([]byte(events[0].Output), &ev0); err != nil {
		t.Fatal(err)
	}
	if ev0.Action != "tester_reject" || ev0.Wave != 1 {
		t.Fatalf("first event: %+v", ev0)
	}
	var ev1 LoopEvent
	if err := json.Unmarshal([]byte(events[1].Output), &ev1); err != nil {
		t.Fatal(err)
	}
	if ev1.Wave != 1 {
		t.Fatalf("same-wave corrective should keep wave=1, got %d", ev1.Wave)
	}
	if o.waveCounter != 1 {
		t.Fatalf("waveCounter=%d want 1", o.waveCounter)
	}
}

func TestFirstFailureLine(t *testing.T) {
	if got := firstFailureLine([]string{" missing main entry "}, ""); got != "missing main entry" {
		t.Fatalf("got %q", got)
	}
	if got := firstFailureLine(nil, "summary only"); got != "summary only" {
		t.Fatalf("summary fallback got %q", got)
	}
}
