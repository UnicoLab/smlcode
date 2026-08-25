package workspace

import (
	"context"
	"testing"
)

// TestWithdrawalIsReportedAsItsOwnIntervention keeps the operator's view
// truthful: "we withdrew a tool" is not the same event as "we nudged".
func TestWithdrawalIsReportedAsItsOwnIntervention(t *testing.T) {
	tr := NewCallTracker()
	var reasons []string
	tr.OnIntervention = func(reason, message string) { reasons = append(reasons, reason) }
	c := newCounted(tr, "ws_read")
	ctx := WithTaskID(context.Background(), "T1")
	same := map[string]interface{}{"path": "a.go"}

	_, _ = c.call(ctx, "ws_read", same)
	_, _ = c.call(ctx, "ws_read", same)
	_, _ = c.call(ctx, "ws_read", same)

	want := []string{ReasonRepeatedToolCall, ReasonToolWithdrawn + ":ws_read"}
	if len(reasons) != len(want) {
		t.Fatalf("intervention reasons = %v, want %v", reasons, want)
	}
	for i := range want {
		if reasons[i] != want[i] {
			t.Fatalf("intervention reasons = %v, want %v", reasons, want)
		}
	}
}
