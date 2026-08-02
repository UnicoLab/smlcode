package plan

import "testing"

func TestParseEscalateDecide(t *testing.T) {
	d, ok := ParseEscalateDecide(`{"action":"retry","reason":"stubs","confidence":0.8}`)
	if !ok || d.Action != EscalateActionRetry {
		t.Fatalf("%+v ok=%v", d, ok)
	}
	d, ok = ParseEscalateDecide("Here is my pick:\n```json\n{\"action\": \"re_scope\", \"reason\": \"vague\"}\n```")
	if !ok || d.Action != EscalateActionReScope {
		t.Fatalf("embedded: %+v ok=%v", d, ok)
	}
	if _, ok := ParseEscalateDecide("no json here"); ok {
		t.Fatal("should fail")
	}
}

func TestHeuristicEscalateDecide(t *testing.T) {
	ans := HeuristicEscalateDecide(Task{
		Error: "static quality failed", Review: "stub Placeholder",
	}, "Placeholder implementation")
	if ans.Action != EscalateActionRetry {
		t.Fatalf("stub → retry got %s", ans.Action)
	}
	ans = HeuristicEscalateDecide(Task{
		Error: "needs human", Acceptance: "clarify vague requirements",
	}, "needs clarify / secret api key")
	if ans.Action != EscalateActionReScope {
		t.Fatalf("vague → re_scope got %s", ans.Action)
	}
}
