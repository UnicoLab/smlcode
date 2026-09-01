package orchestrator

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/squads"
)

func libraryPlan() *squads.Plan {
	return &squads.Plan{Squads: []squads.Squad{
		{ID: "backend-go", Owns: []string{"cmd/**"}},
		{ID: "frontend-react", Owns: []string{"web/**"}},
	}}
}

// The shape a local 30B actually emits: the spec is agreed and nobody is
// recorded as having agreed to it. Measured live — one clause, provider
// resolved, consumers empty — which half-freezes the seam and costs the
// consumer its reason to stop waiting on the provider.
func TestASoleConsumerIsFilledInRatherThanLeftEmpty(t *testing.T) {
	kept, _, implied := resolveInterfaces(libraryPlan(), []squads.Interface{
		{ID: "GET /api/todos", Provider: "backend", Spec: "200 → {todos: []}"},
	})

	if len(kept) != 1 {
		t.Fatalf("kept %d clauses, want the one", len(kept))
	}
	if got := kept[0].Consumers; len(got) != 1 || got[0] != "frontend-react" {
		t.Fatalf("consumers = %v, want the only team it can be", got)
	}
	// Inferred, so it has to be said out loud.
	if len(implied) != 1 || !strings.Contains(implied[0], "frontend-react") {
		t.Errorf("the inference was not reported: %v", implied)
	}
	// And the loose reference still resolves onto the library id.
	if kept[0].Provider != "backend-go" {
		t.Errorf("provider = %q", kept[0].Provider)
	}
}

// With three teams the consumer is a real question, and answering it by
// guessing would freeze a seam between teams that never agreed to one.
func TestNoConsumerIsInventedWhenThereIsAChoice(t *testing.T) {
	p := &squads.Plan{Squads: []squads.Squad{
		{ID: "backend-go", Owns: []string{"cmd/**"}},
		{ID: "frontend-react", Owns: []string{"web/**"}},
		{ID: "data", Owns: []string{"etl/**"}},
	}}

	kept, _, implied := resolveInterfaces(p, []squads.Interface{
		{ID: "GET /api/todos", Provider: "backend-go"},
	})

	if len(kept) != 1 || len(kept[0].Consumers) != 0 {
		t.Fatalf("a consumer was invented: %+v", kept)
	}
	if len(implied) != 0 {
		t.Errorf("implied = %v", implied)
	}
}

// A named consumer is never overridden, and a clause naming nobody real is
// dropped with its id reported rather than failing the whole contract.
func TestNamedConsumersWinAndUnknownProvidersAreDropped(t *testing.T) {
	kept, dropped, implied := resolveInterfaces(libraryPlan(), []squads.Interface{
		{ID: "GET /api/todos", Provider: "backend", Consumers: []string{"frontend"}},
		{ID: "orphan", Provider: "mobile"},
	})

	if len(kept) != 1 || kept[0].ID != "GET /api/todos" {
		t.Fatalf("kept = %+v", kept)
	}
	if got := kept[0].Consumers; len(got) != 1 || got[0] != "frontend-react" {
		t.Errorf("consumers = %v", got)
	}
	if len(implied) != 0 {
		t.Errorf("a named consumer was treated as implied: %v", implied)
	}
	if len(dropped) != 1 || dropped[0] != "orphan" {
		t.Errorf("dropped = %v, want the clause naming no team", dropped)
	}
}

// The provider is never listed as its own consumer, however the model wrote it.
func TestAProviderIsNotItsOwnConsumer(t *testing.T) {
	kept, _, _ := resolveInterfaces(libraryPlan(), []squads.Interface{
		{ID: "x", Provider: "backend-go", Consumers: []string{"backend", "frontend-react"}},
	})
	for _, c := range kept[0].Consumers {
		if c == "backend-go" {
			t.Fatalf("provider consumes itself: %v", kept[0].Consumers)
		}
	}
}
