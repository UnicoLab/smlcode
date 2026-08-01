package plan

import (
	"strings"
	"testing"
)

func TestParsePlanJSON(t *testing.T) {
	raw := "```json\n{\"summary\":\"Refactor auth\",\"goals\":[\"JWT\"],\"steps\":[\"Add claims\",\"Update middleware\"]}\n```"
	p, err := ParsePlanJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Summary != "Refactor auth" {
		t.Fatalf("summary=%q", p.Summary)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("steps=%v", p.Steps)
	}
}

func TestParsePlanJSONObjectSteps(t *testing.T) {
	raw := `{"summary":"Build CLI","goals":["hello"],"steps":[{"step":1,"task":"Create main.py","description":"Write argparse CLI"},{"id":2,"action":"Verify --help"}]}`
	p, err := ParsePlanJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Summary != "Build CLI" {
		t.Fatalf("summary=%q", p.Summary)
	}
	if len(p.Steps) < 2 {
		t.Fatalf("steps=%v", p.Steps)
	}
	if !strings.Contains(p.Steps[0], "argparse") && !strings.Contains(p.Steps[0], "main.py") {
		t.Fatalf("step0=%q", p.Steps[0])
	}
}

func TestParseTasksJSON(t *testing.T) {
	raw := `{"tasks":[{"id":"T1","title":"A","description":"do A","role":"worker"},{"title":"B","description":"do B","depends_on":["T1"]}]}`
	tasks, err := ParseTasksJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len=%d", len(tasks))
	}
	if tasks[1].ID != "T2" {
		t.Fatalf("auto id=%s", tasks[1].ID)
	}
	if tasks[1].Column != ColReadyToDev {
		t.Fatalf("column=%s", tasks[1].Column)
	}
}

func TestBoardReadyAndDone(t *testing.T) {
	b := &Board{Tasks: []Task{
		{ID: "T1", Column: ColReadyToDev, Role: RoleWorker},
		{ID: "T2", Column: ColReadyToDev, Role: RoleWorker, DependsOn: []string{"T1"}},
		{ID: "T3", Column: ColToScope, Role: RoleWorker},
	}}
	for i := range b.Tasks {
		b.Tasks[i].Normalize()
	}
	ready := b.ReadyTasks()
	if len(ready) != 1 || ready[0].ID != "T1" {
		t.Fatalf("ready=%v", ready)
	}
	t1 := ready[0]
	t1.MoveTo(ColDone)
	b.UpdateTask(t1)
	ready = b.ReadyTasks()
	if len(ready) != 1 || ready[0].ID != "T2" {
		t.Fatalf("ready after T1=%v", ready)
	}
	t2 := ready[0]
	t2.MoveTo(ColDone)
	b.UpdateTask(t2)
	if !b.AllDone() {
		t.Fatal("expected agent work done (human backlog ok)")
	}
	if !b.HumanBacklogRemaining() {
		t.Fatal("expected T3 in human backlog")
	}
}

func TestParseReviewJSON(t *testing.T) {
	r := ParseReviewJSON(`{"approved":false,"score":40,"issues":["missing test"],"summary":"needs work"}`)
	if r.Approved || r.Score != 40 || len(r.Issues) != 1 {
		t.Fatalf("%+v", r)
	}
	r2 := ParseReviewJSON(`{"approved": true, "score": 90, "issues": [], "summary": "ok"}`)
	if !r2.Approved {
		t.Fatalf("%+v", r2)
	}
	if !WorkerLooksComplete(`{"status":"done","summary":"ok","files_changed":["hello.go"]}`) {
		t.Fatal("expected worker complete")
	}
	if WorkerLooksComplete(`{"status":"blocked"}`) {
		t.Fatal("blocked should not look complete")
	}
}
