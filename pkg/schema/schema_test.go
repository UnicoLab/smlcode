package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestForKnownRolesAndAliases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plan", RolePlan},
		{"planner", RolePlan},
		{"splitter", RoleTasks},
		{"reviewer", RoleReview},
		{"reviewer-strict", RoleReview},
		{"clarifier", RoleClarify},
		{"scope-judge", RoleScopeJudge},
		{"composer", RoleComposition},
		{"explorer", RoleExplore},
		{"corrector", RoleWorker},
		{"memory", RoleLessons},
		{"TESTER", RoleTester},
	}
	for _, c := range cases {
		got, ok := For(c.in)
		if !ok {
			t.Fatalf("For(%q): not found", c.in)
		}
		if got.Name != c.want {
			t.Errorf("For(%q) = %q, want %q", c.in, got.Name, c.want)
		}
	}
	if _, ok := For("no-such-role"); ok {
		t.Error("unknown role resolved")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		role    string
		raw     string
		wantErr string // substring; empty means must pass
	}{
		{"review ok", RoleReview, `{"approved":true,"score":80,"summary":"fine"}`, ""},
		{"review missing key", RoleReview, `{"approved":true,"score":80}`, `missing required key "summary"`},
		{"review bad bool", RoleReview, `{"approved":"yes","score":80,"summary":"x"}`, "expected boolean"},
		{"review score range", RoleReview, `{"approved":true,"score":420,"summary":"x"}`, "above maximum 100"},
		{"review issues type", RoleReview, `{"approved":true,"score":8,"summary":"x","issues":"one"}`, "expected array"},
		{"tester ok", RoleTester, `{"passed":false,"commands":["go vet ./..."],"summary":"vet failed"}`, ""},
		{"escalate enum", RoleEscalate, `{"action":"explode","reason":"x"}`, "is not one of"},
		{"escalate ok", RoleEscalate, `{"action":"re_scope","reason":"vague"}`, ""},
		{"tasks nested", RoleTasks, `{"tasks":[{"id":"T1","title":"a","description":"b","role":"worker","files":[],"acceptance":"c"}]}`, ""},
		{"tasks nested bad role", RoleTasks, `{"tasks":[{"id":"T1","title":"a","description":"b","role":"wizard","files":[],"acceptance":"c"}]}`, "tasks[0].role"},
		{"plan steps cap", RolePlan, `{"summary":"s","steps":["1","2","3","4","5","6","7"]}`, "at most 6 items"},
		{"not json", RoleReview, `nope`, "not valid JSON"},
		{"clarify ok", RoleClarify, `{"needs_user":false,"assumptions":["a"],"acceptance":["b"]}`, ""},
		{"clarify options min", RoleClarify,
			`{"needs_user":true,"assumptions":[],"acceptance":[],"questions":[{"id":"q1","header":"h","question":"q","options":[{"label":"a","description":"d"}]}]}`,
			"needs at least 2 items"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.role, []byte(c.raw))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error %q does not contain %q", err, c.wantErr)
			}
		})
	}
}

func TestCoerce(t *testing.T) {
	cases := []struct {
		name string
		role string
		raw  string
		want map[string]any
	}{
		{
			name: "string bool to bool",
			role: RoleReview,
			raw:  `{"approved":"true","score":"85","summary":"ok"}`,
			want: map[string]any{"approved": true, "score": float64(85), "summary": "ok"},
		},
		{
			name: "passed no to false",
			role: RoleTester,
			raw:  `{"passed":"no","commands":"go build ./...","summary":"x"}`,
			want: map[string]any{"passed": false, "commands": []any{"go build ./..."}, "summary": "x"},
		},
		{
			name: "scalar to array",
			role: RolePlan,
			raw:  `{"summary":"s","steps":"only one step"}`,
			want: map[string]any{"summary": "s", "steps": []any{"only one step"}},
		},
		{
			name: "comma list to array",
			role: RolePlan,
			raw:  `{"summary":"s","steps":"a, b, c"}`,
			want: map[string]any{"summary": "s", "steps": []any{"a", "b", "c"}},
		},
		{
			name: "number to string",
			role: RoleReview,
			raw:  `{"approved":1,"score":80,"summary":42}`,
			want: map[string]any{"approved": true, "score": float64(80), "summary": "42"},
		},
		{
			name: "percent score",
			role: RoleReview,
			raw:  `{"approved":true,"score":"75%","summary":"ok"}`,
			want: map[string]any{"approved": true, "score": float64(75), "summary": "ok"},
		},
		{
			name: "null array becomes empty",
			role: RoleWorker,
			raw:  `{"status":"done","summary":"s","files_changed":null}`,
			want: map[string]any{"status": "done", "summary": "s", "files_changed": []any{}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, ok := For(c.role)
			if !ok {
				t.Fatalf("no schema registered for role %q", c.role)
			}
			out, err := CoerceSpec(spec, []byte(c.raw))
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("coerced output is not JSON: %v (%s)", err, out)
			}
			for k, want := range c.want {
				gotV, ok := got[k]
				if !ok {
					t.Fatalf("key %q missing from %s", k, out)
				}
				if !jsonEqual(gotV, want) {
					t.Errorf("key %q = %#v, want %#v", k, gotV, want)
				}
			}
			// Coercion must produce something the validator accepts.
			if err := Validate(c.role, out); err != nil {
				t.Errorf("coerced output still invalid: %v (%s)", err, out)
			}
		})
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func TestStrictSchemaMakesEverythingRequired(t *testing.T) {
	spec, _ := For(RoleReview)
	strict := StrictSchema(spec)
	req := RequiredNames(strict)
	if len(req) != 4 {
		t.Fatalf("strict required = %v, want all 4 properties", req)
	}
	if strict["additionalProperties"] != false {
		t.Error("strict schema must set additionalProperties=false")
	}
	// The semantic schema must be untouched.
	if got := len(RequiredKeys(spec)); got != 3 {
		t.Errorf("StrictSchema mutated the source spec: required=%d", got)
	}
	// Nested objects too.
	tasks, _ := For(RoleTasks)
	st := StrictSchema(tasks)
	props := st["properties"].(map[string]any)
	arr := props["tasks"].(map[string]any)
	item := arr["items"].(map[string]any)
	if item["additionalProperties"] != false {
		t.Error("nested object not made strict")
	}
	if len(RequiredNames(item)) != 7 {
		t.Errorf("nested required = %v", RequiredNames(item))
	}
}

func TestDetectRoleFromPromptContract(t *testing.T) {
	planContract := `STRICT JSON only:
{"summary":"…","goals":[],"assumptions":[],"risks":[],"steps":[]}`
	clarifyContract := `{"needs_user":false,"questions":[],"assumptions":[],"acceptance":[],"non_goals":[],"language":"","entrypoint":"","prd":{}}`
	judgeContract := `{"ok":true,"issues":["T1: missing acceptance"],"hints":[],"weak_task_ids":[]}`

	// The planner agent is bound to "plan" but also runs the clarify interview.
	if got, _ := DetectRole(planContract, RolePlan); got.Name != RolePlan {
		t.Errorf("plan prompt detected as %q", got.Name)
	}
	if got, _ := DetectRole(clarifyContract, RolePlan); got.Name != RoleClarify {
		t.Errorf("clarify prompt under planner hint detected as %q, want clarify", got.Name)
	}
	// The reviewer agent is bound to "review" but also runs the scope judge.
	if got, _ := DetectRole(judgeContract, RoleReview); got.Name != RoleScopeJudge {
		t.Errorf("scope-judge prompt under reviewer hint detected as %q", got.Name)
	}
	// No contract in the prompt at all → fall back to the bound hint.
	if got, ok := DetectRole("just do the thing", RoleReview); !ok || got.Name != RoleReview {
		t.Errorf("fallback failed: %q %v", got.Name, ok)
	}
	if _, ok := DetectRole("nothing", ""); ok {
		t.Error("empty hint with no contract should not resolve")
	}
}

func TestEveryRegisteredRoleHasUsableArtifacts(t *testing.T) {
	for _, role := range Roles() {
		spec, _ := For(role)
		if len(RequiredKeys(spec)) == 0 {
			t.Errorf("%s: no required keys — DetectRole cannot score it", role)
		}
		if _, err := json.Marshal(spec.Schema); err != nil {
			t.Errorf("%s: schema not JSON-serializable: %v", role, err)
		}
		if _, err := json.Marshal(StrictSchema(spec)); err != nil {
			t.Errorf("%s: strict schema not JSON-serializable: %v", role, err)
		}
		if g := GBNF(spec); !strings.Contains(g, "root ::=") {
			t.Errorf("%s: grammar has no root", role)
		}
	}
}
