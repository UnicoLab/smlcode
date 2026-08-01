package compact

import "testing"

func TestShouldCompactNow(t *testing.T) {
	if !ShouldCompactNow(80, 80, false, false) {
		t.Fatal("at threshold")
	}
	if ShouldCompactNow(79, 80, false, false) {
		t.Fatal("below threshold")
	}
	if ShouldCompactNow(90, 80, true, false) {
		t.Fatal("compacting")
	}
	if ShouldCompactNow(90, 80, false, true) {
		t.Fatal("paused")
	}
	if ShouldCompactNow(90, 0, false, false) {
		t.Fatal("disabled")
	}
}

func TestCompactionHelpedAndHysteresis(t *testing.T) {
	if !CompactionHelped(70, 80) {
		t.Fatal("70 is 10pp under 80")
	}
	if CompactionHelped(76, 80) {
		t.Fatal("only 4pp under — not enough")
	}
	w := NewWatchdog(80)
	if !w.ShouldCompact(85) {
		t.Fatal("should fire")
	}
	w.RecordPostCompact(78) // still within 5pp → pause
	if w.ShouldCompact(90) {
		t.Fatal("paused after weak compact")
	}
	w.MaybeRearm(70)
	if !w.ShouldCompact(85) {
		t.Fatal("re-armed after usage drop")
	}
}

func TestCompactChatMessages(t *testing.T) {
	var msgs []ChatMsg
	for i := 0; i < 12; i++ {
		msgs = append(msgs, ChatMsg{Role: "user", Content: "msg"})
	}
	out, ok := CompactChatMessages(msgs, 4)
	if !ok {
		t.Fatal("expected compact")
	}
	if len(out) != 6 { // digest + 4 keep + resume
		t.Fatalf("len=%d", len(out))
	}
	if out[len(out)-1].Content != ResumeMessage {
		t.Fatal("missing resume message")
	}
}

func TestEstimateAndWindow(t *testing.T) {
	if EstimateTokens(4) != 1 {
		t.Fatal(EstimateTokens(4))
	}
	if WindowTokensFromKB(32) <= 0 {
		t.Fatal("window")
	}
	if UsagePercent(80, 100) != 80 {
		t.Fatal(UsagePercent(80, 100))
	}
}
