package squads

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func seamPlan() *Plan {
	return &Plan{
		Squads: []Squad{
			{ID: "backend", Name: "Backend", Charter: "BACKEND CHARTER: own the API", Owns: []string{"cmd/**", "internal/**"}},
			{ID: "frontend", Name: "Frontend", Charter: "FRONTEND CHARTER: own the SPA", Owns: []string{"web/**"}},
		},
		Contract: Contract{Interfaces: []Interface{
			{ID: "GET /api/todos", Provider: "backend", Consumers: []string{"frontend"}, Spec: "200 [{id,title,done}]"},
			{ID: "POST /api/todos", Provider: "backend", Consumers: []string{"frontend"}, Spec: "body {title} -> 201 {id}"},
			{ID: "VITE_API_BASE", Provider: "frontend", Consumers: []string{"backend"}, Spec: "env var the SPA reads"},
		}},
	}
}

// A task the router left unassigned BECAUSE it straddles the seam is the task
// that most needs the contract. It gets the whole interface list — every ID —
// and none of the squad charters.
func TestSeamTaskGetsTheWholeContract(t *testing.T) {
	p := seamPlan()
	straddling := plan.Task{ID: "T3", Files: []string{"internal/http/todos.go", "web/src/api.ts"}}

	brief := BriefFor(p, straddling)
	if brief == "" {
		t.Fatal("a task spanning two squads' territory got no brief at all")
	}
	for _, in := range p.Contract.Interfaces {
		if !strings.Contains(brief, in.ID) {
			t.Errorf("seam brief omits interface %q:\n%s", in.ID, brief)
		}
	}
	if strings.Contains(brief, "CHARTER") {
		t.Errorf("seam brief carries a squad charter — interfaces only:\n%s", brief)
	}
	if !strings.Contains(brief, "backend and frontend") {
		t.Errorf("seam brief must name the squads it spans:\n%s", brief)
	}
	if !strings.Contains(brief, "provided by `backend`") || !strings.Contains(brief, "consumed by `frontend`") {
		t.Errorf("seam brief must say which side owes each interface:\n%s", brief)
	}
}

// The other cases are unchanged: an assigned task gets its own squad's brief,
// and an unassigned task inside one territory (or none) gets nothing.
func TestBriefForKeepsTheOtherCases(t *testing.T) {
	p := seamPlan()
	if got := BriefFor(p, plan.Task{Squad: "backend", Files: []string{"cmd/main.go"}}); !strings.Contains(got, "BACKEND CHARTER") {
		t.Fatalf("an assigned task must get its squad's brief:\n%s", got)
	}
	if got := BriefFor(p, plan.Task{Files: []string{"web/src/App.tsx"}}); got != "" {
		t.Fatalf("an unassigned task inside one territory got a brief:\n%s", got)
	}
	if got := BriefFor(p, plan.Task{Files: []string{"README.md"}}); got != "" {
		t.Fatalf("an unassigned task outside every territory got a brief:\n%s", got)
	}
	if got := BriefFor(nil, plan.Task{Files: []string{"cmd/a.go", "web/b.ts"}}); got != "" {
		t.Fatal("nil plan must yield no brief")
	}
}

// The seam brief is pasted into a small model's prompt: the interface list is
// capped and each spec clipped.
func TestSeamBriefIsBounded(t *testing.T) {
	p := seamPlan()
	p.Contract.Interfaces = nil
	long := strings.Repeat("shape ", 200)
	for i := 0; i < maxSeamInterfaces+5; i++ {
		p.Contract.Interfaces = append(p.Contract.Interfaces, Interface{
			ID: "IFACE-" + string(rune('A'+i)), Provider: "backend", Consumers: []string{"frontend"}, Spec: long,
		})
	}
	brief := p.SeamBrief([]string{"backend", "frontend"})
	if !strings.Contains(brief, "and 5 more") {
		t.Fatalf("the interface list was not capped:\n%s", brief)
	}
	if strings.Contains(brief, long) {
		t.Fatal("a long spec was pasted whole")
	}
	if len(brief) > (maxSeamInterfaces+1)*(maxSeamSpecChars+120) {
		t.Fatalf("seam brief is %d bytes — not bounded", len(brief))
	}
	// A plan with no contract still names the seam and says there is none.
	p.Contract.Interfaces = nil
	if got := p.SeamBrief([]string{"backend", "frontend"}); !strings.Contains(got, "froze no interfaces") {
		t.Fatalf("a contract-less seam brief must say so:\n%s", got)
	}
}
