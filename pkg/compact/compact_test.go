package compact

import "testing"

func TestHeuristicSummarize(t *testing.T) {
	var body string
	for i := 0; i < 40; i++ {
		body += "## Wave update (" + string(rune('a'+i%26)) + ")\n\nnoise " + string(rune('0'+i%10)) + "\n\n"
	}
	body = "## Locked PRD\n\nKeep me\n\n" + body
	res := HeuristicSummarize(body, 800)
	if !res.Compacted {
		t.Fatal("expected compaction")
	}
	if res.AfterBytes >= res.BeforeBytes {
		t.Fatalf("after=%d before=%d", res.AfterBytes, res.BeforeBytes)
	}
	if !contains(res.Summary, "Locked PRD") {
		t.Fatalf("lost PRD: %s", res.Summary[:min(200, len(res.Summary))])
	}
}

func TestCompactMessages(t *testing.T) {
	msgs := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	out, ok := CompactMessages(msgs, 3)
	if !ok || len(out) != 4 {
		t.Fatalf("%v ok=%v", out, ok)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
