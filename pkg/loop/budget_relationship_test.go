package loop

import "testing"

// TestDefaultBudgetDoesNotCapDefaultRetries pins defect 3.
//
// max_task_calls and max_retries are one setting in two halves: the budget
// stops the ladder, so a budget below what the ladder costs silently overrules
// max_retries. On the shipped defaults one task costs
// worker + self-critique + max_retries × (review + correct) = 2 + 2×4 = 10.
// The old default of 6 bought exactly TWO correction rounds however high
// max_retries was set, and nothing in the log connected the two numbers.
func TestDefaultBudgetDoesNotCapDefaultRetries(t *testing.T) {
	const defaultMaxRetries = 4 // config.Config.MaxRetries default
	if got := MaxTaskCallsFor(defaultMaxRetries); got != DefaultMaxTaskCalls {
		t.Fatalf("MaxTaskCallsFor(%d) = %d, but DefaultMaxTaskCalls = %d — "+
			"the default budget and the default retry count must agree",
			defaultMaxRetries, got, DefaultMaxTaskCalls)
	}
	if DefaultMaxTaskCalls < 10 {
		t.Fatalf("DefaultMaxTaskCalls = %d: a hard task gets only %d correction round(s)",
			DefaultMaxTaskCalls, (DefaultMaxTaskCalls-2)/2)
	}
	// A raised max_retries must raise the budget with it, never the other way.
	if got := MaxTaskCallsFor(8); got != 18 {
		t.Fatalf("MaxTaskCallsFor(8) = %d, want 18", got)
	}
	// A lowered one never drops below the default floor.
	if got := MaxTaskCallsFor(1); got != DefaultMaxTaskCalls {
		t.Fatalf("MaxTaskCallsFor(1) = %d, want the %d floor", got, DefaultMaxTaskCalls)
	}
}

// TestSpeculativeReviewReportsItsRealRequestCount pins the other half of
// defect 3: one race is one budget unit but two real LLM round-trips at the
// default max_parallel, and a server that serializes inference makes the
// operator wait on the round-trips.
func TestSpeculativeReviewReportsItsRealRequestCount(t *testing.T) {
	b := newCallBudget(4)
	b.take("T1")    // one review attempt
	b.note("T1", 1) // …that fanned out to reviewer + reviewer-strict
	if b.spent("T1") != 1 {
		t.Fatalf("budget units = %d, want 1 — a race is one review attempt", b.spent("T1"))
	}
	if b.sentRequests("T1") != 2 {
		t.Fatalf("llm requests = %d, want 2 — the fan-out must be counted honestly",
			b.sentRequests("T1"))
	}
	b.reset("T1")
	if b.sentRequests("T1") != 0 {
		t.Fatal("reset must clear the request counter with the budget")
	}
}
