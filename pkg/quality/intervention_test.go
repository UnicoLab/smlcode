package quality

import "testing"

func TestClassifyIntervention(t *testing.T) {
	if ClassifyIntervention("repeated_tool_call") != InterventionLoop {
		t.Fatal("loop")
	}
	if ClassifyIntervention("shell whitelist: rm") != InterventionWhitelist {
		t.Fatal("whitelist")
	}
	if ClassifyIntervention("thinking_budget_exceeded") != InterventionThinking {
		t.Fatal("thinking")
	}
	if ClassifyIntervention("malformed_args:ws_edit") != InterventionMalformed {
		t.Fatal("malformed")
	}
}
