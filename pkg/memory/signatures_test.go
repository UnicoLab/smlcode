package memory

import (
	"fmt"
	"testing"
)

// TestThrashIsStillDetectedAfterManyDistinctCalls is the regression guard for a
// detector that went blind on exactly the runs it exists to catch.
//
// THE DEFECT: the signature map FROZE at MaxSignatures — once full, a signature
// it had not already seen was never admitted, so its later repeats were never
// counted. The asymmetry is what makes this bite: the classic small-model edit
// failure is a near-miss ws_edit retried with a slightly different old_str each
// time, and every one of those is a DISTINCT signature. A thrashing run
// therefore fills the map faster than a healthy one and blinds the detector
// sooner. RunReport.RedundantCalls and the redundant-call-rate KPI silently
// under-reported, and evolve learned from the truncated signal.
func TestThrashIsStillDetectedAfterManyDistinctCalls(t *testing.T) {
	w := NewWorking(4000)

	// A long run of unique calls — the near-miss edit retries that fill the map.
	for i := 0; i < MaxSignatures*2; i++ {
		w.RecordTool(ToolEvent{Tool: "ws_edit", Path: "a.go", Args: fmt.Sprintf("old_str=v%d", i), OK: false})
	}
	_, _, baseline := w.Counters()

	// NOW the model starts genuinely repeating one call. This is the thrash the
	// detector is for, and it begins after the map is long since full.
	for i := 0; i < 5; i++ {
		w.RecordTool(ToolEvent{Tool: "ws_shell", Path: "", Command: "go build ./...", OK: true})
	}

	_, _, after := w.Counters()
	got := after - baseline
	if got != 4 {
		t.Fatalf("5 identical calls after %d distinct ones counted %d repeat(s), "+
			"want 4 — the detector stopped seeing new signatures once full",
			MaxSignatures*2, got)
	}
}

// TestSignatureMapStaysBounded is the control: eviction must not become growth.
func TestSignatureMapStaysBounded(t *testing.T) {
	w := NewWorking(4000)
	for i := 0; i < MaxSignatures*4; i++ {
		w.RecordTool(ToolEvent{Tool: "ws_read", Path: fmt.Sprintf("f%d.go", i), OK: true})
	}
	if n := len(w.signatures); n > MaxSignatures {
		t.Fatalf("signature map holds %d entries, cap is %d", n, MaxSignatures)
	}
	if n := len(w.sigOrder); n > MaxSignatures {
		t.Fatalf("signature order list holds %d entries, cap is %d", n, MaxSignatures)
	}
	if len(w.sigOrder) != len(w.signatures) {
		t.Fatalf("order list (%d) and map (%d) disagree — an eviction leaked",
			len(w.sigOrder), len(w.signatures))
	}
}
