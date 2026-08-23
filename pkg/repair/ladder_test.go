package repair

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/schema"
)

func TestRepairLadderRungs(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantRung string
		wantJSON string
	}{
		{
			name:     "already valid",
			raw:      `{"approved":true,"score":90,"summary":"ok"}`,
			wantRung: RungNone,
			wantJSON: `{"approved":true,"score":90,"summary":"ok"}`,
		},
		{
			name:     "markdown fence",
			raw:      "```json\n{\"approved\":true,\"score\":90,\"summary\":\"ok\"}\n```",
			wantRung: RungFence,
			wantJSON: `{"approved":true,"score":90,"summary":"ok"}`,
		},
		{
			name:     "prose before and after",
			raw:      "Here is my review:\n{\"approved\":true,\"score\":90,\"summary\":\"ok\"}\nHope that helps!",
			wantRung: RungExtract,
			wantJSON: `{"approved":true,"score":90,"summary":"ok"}`,
		},
		{
			name:     "trailing comma",
			raw:      `{"approved":true,"score":90,"summary":"ok",}`,
			wantRung: RungTrailingComa,
			wantJSON: `{"approved":true,"score":90,"summary":"ok"}`,
		},
		{
			name:     "single quotes",
			raw:      `{'approved':true,'score':90,'summary':'ok'}`,
			wantRung: RungQuotes,
			wantJSON: `{"approved":true,"score":90,"summary":"ok"}`,
		},
		{
			name:     "python literals",
			raw:      `{"approved":True,"score":90,"summary":"ok","issues":None}`,
			wantRung: RungPyLiterals,
		},
		{
			name:     "raw newline inside a string",
			raw:      "{\"approved\":true,\"score\":90,\"summary\":\"line one\nline two\"}",
			wantRung: RungControlChars,
			wantJSON: `{"approved":true,"score":90,"summary":"line one\nline two"}`,
		},
		{
			name:     "raw tab inside a string",
			raw:      "{\"approved\":true,\"score\":90,\"summary\":\"a\tb\"}",
			wantRung: RungControlChars,
		},
		{
			name:     "missing closing brace",
			raw:      `{"approved":true,"score":90,"summary":"ok"`,
			wantRung: RungCloseBraces,
			wantJSON: `{"approved":true,"score":90,"summary":"ok"}`,
		},
		{
			name:     "missing closing bracket and brace",
			raw:      `{"approved":true,"score":90,"summary":"ok","issues":["a"`,
			wantRung: RungCloseBraces,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rung, err := Repair(tc.raw, schema.Spec{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rung != tc.wantRung {
				t.Errorf("rung = %q, want %q (output %s)", rung, tc.wantRung, got)
			}
			if !json.Valid(got) {
				t.Fatalf("output is not valid JSON: %s", got)
			}
			if tc.wantJSON != "" && !sameJSON(string(got), tc.wantJSON) {
				t.Errorf("output = %s, want %s", got, tc.wantJSON)
			}
		})
	}
}

func TestRepairDetectsTruncationDistinctly(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"cut mid string", `{"approved":true,"score":90,"summary":"the worker edited calc.go and then`},
		{"cut mid string in array", `{"summary":"s","steps":["first step","second ste`},
		{"cut inside a fenced block", "```json\n{\"summary\":\"s\",\"steps\":[\"half a ste"},
		{"cut right after an escape", `{"summary":"s","steps":["a\`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rung, err := Repair(tc.raw, schema.Spec{})
			if !errors.Is(err, ErrTruncated) {
				t.Fatalf("err = %v, want ErrTruncated (rung %q, out %s)", err, rung, got)
			}
			if rung != RungTruncated {
				t.Errorf("rung = %q, want %q", rung, RungTruncated)
			}
			if got != nil {
				t.Errorf("truncated input must not yield a guessed document: %s", got)
			}
			if !strings.Contains(err.Error(), "max_tokens") {
				t.Errorf("error should name the fix: %v", err)
			}
		})
	}
}

func TestRepairDoesNotMistakeCompleteJSONForTruncation(t *testing.T) {
	for _, raw := range []string{
		`{"summary":"he said \"hi\" then left","steps":["a"]}`,
		`{"summary":"path is C:\\tmp\\x","steps":[]}`,
		"```json\n{\"summary\":\"s\",\"steps\":[]}\n```",
	} {
		if _, rung, err := Repair(raw, schema.Spec{}); err != nil {
			t.Errorf("%s → rung %q err %v", raw, rung, err)
		}
	}
}

func TestRepairCoercesAgainstSchema(t *testing.T) {
	spec, _ := schema.For(schema.RoleReview)
	got, rung, err := Repair(`{"approved":"true","score":"85","summary":"ok"}`, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(rung, "+"+RungCoerce) {
		t.Errorf("rung = %q, want a +coerce suffix", rung)
	}
	if err := schema.ValidateSpec(spec, got); err != nil {
		t.Errorf("coerced output still invalid: %v (%s)", err, got)
	}
	var m map[string]any
	_ = json.Unmarshal(got, &m)
	if m["approved"] != true {
		t.Errorf("approved = %#v", m["approved"])
	}

	// A document that already matches the schema keeps a plain rung name.
	_, rung2, err := Repair(`{"approved":true,"score":85,"summary":"ok"}`, spec)
	if err != nil {
		t.Fatal(err)
	}
	if rung2 != RungNone {
		t.Errorf("rung = %q, want %q", rung2, RungNone)
	}
}

func TestRepairCombinesRungsAndCoercion(t *testing.T) {
	spec, _ := schema.For(schema.RoleTester)
	// Fenced + trailing comma + string boolean + scalar where an array is wanted.
	raw := "```json\n{\"passed\":\"yes\",\"commands\":\"go build ./...\",\"summary\":\"ok\",}\n```"
	got, rung, err := Repair(raw, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rung, RungCoerce) {
		t.Errorf("rung = %q, expected coercion to be reported", rung)
	}
	if err := schema.ValidateSpec(spec, got); err != nil {
		t.Fatalf("output invalid: %v (%s)", err, got)
	}
	var r struct {
		Passed   bool     `json:"passed"`
		Commands []string `json:"commands"`
	}
	if err := json.Unmarshal(got, &r); err != nil {
		t.Fatal(err)
	}
	if !r.Passed || len(r.Commands) != 1 {
		t.Errorf("got %+v from %s", r, got)
	}
}

func TestRepairUnrepairable(t *testing.T) {
	for _, raw := range []string{"", "   ", "I cannot help with that request."} {
		got, rung, err := Repair(raw, schema.Spec{})
		if err == nil {
			t.Errorf("%q unexpectedly repaired to %s", raw, got)
			continue
		}
		if !errors.Is(err, ErrUnrepairable) {
			t.Errorf("%q → %v, want ErrUnrepairable", raw, err)
		}
		if rung != RungFailed {
			t.Errorf("%q → rung %q", raw, rung)
		}
	}
}

func TestRepairRoleAndInto(t *testing.T) {
	var out struct {
		Approved bool   `json:"approved"`
		Summary  string `json:"summary"`
	}
	rung, err := RepairInto("```json\n{\"approved\":\"true\",\"score\":80,\"summary\":\"fine\"}\n```", "reviewer", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Approved || out.Summary != "fine" {
		t.Errorf("out = %+v (rung %q)", out, rung)
	}
	// An unknown role degrades to schema-free repair rather than failing.
	if _, _, err := RepairRole(`{"a":1}`, "no-such-role"); err != nil {
		t.Errorf("unknown role should not fail: %v", err)
	}
}

func TestCounterTracksRungDistribution(t *testing.T) {
	c := &Counter{}
	c.Record(RungNone, nil)
	c.Record(RungExtract, nil)
	c.Record(RungExtract, nil)
	c.Record(RungCloseBraces, nil)
	c.Record(RungTruncated, ErrTruncated)
	c.Record(RungFailed, ErrUnrepairable)
	c.Record("extract+coerce", nil)

	total, failed, truncated, coerced := c.Totals()
	if total != 7 || failed != 1 || truncated != 1 || coerced != 1 {
		t.Errorf("totals = %d/%d/%d/%d", total, failed, truncated, coerced)
	}
	if snap := c.Snapshot(); snap[RungExtract] != 2 {
		t.Errorf("snapshot = %v", snap)
	}
	top, n := c.Top()
	if top != RungExtract || n != 2 {
		t.Errorf("Top = %q %d", top, n)
	}
	rep := c.Report()
	if len(rep) < 2 || !strings.HasPrefix(rep[0], "total=7") {
		t.Errorf("report = %v", rep)
	}
	c.Reset()
	if total, _, _, _ := c.Totals(); total != 0 {
		t.Error("reset failed")
	}
}

func TestRepairRecordsIntoPackageStats(t *testing.T) {
	Stats.Reset()
	_, _, _ = Repair("```json\n{\"a\":1}\n```", schema.Spec{})
	_, _, _ = Repair(`{"a":"unterminated`, schema.Spec{})
	total, _, truncated, _ := Stats.Totals()
	if total != 2 || truncated != 1 {
		t.Errorf("stats total=%d truncated=%d", total, truncated)
	}
	Stats.Reset()
}

func TestLegacyRepairJSONStillWorks(t *testing.T) {
	// The old entry points must keep behaving — plan/composer call them directly.
	got, err := RepairJSON("```json\n{\"a\":True,}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(got, `{"a":true}`) {
		t.Errorf("got %s", got)
	}
	var dest map[string]any
	if err := RepairAndUnmarshal(`prefix {"b":2} suffix`, &dest); err != nil {
		t.Fatal(err)
	}
	if dest["b"] != float64(2) {
		t.Errorf("dest = %v", dest)
	}
}

func TestEscapeControlCharsLeavesValidJSONAlone(t *testing.T) {
	in := `{"a":"already \n escaped","b":"tab\there"}`
	if got := escapeControlCharsInStrings(in); got != in {
		t.Errorf("mangled valid input:\n got %s\nwant %s", got, in)
	}
}

func TestRepairRecoversAnObjectCutAfterAComma(t *testing.T) {
	// max_tokens landing right after a complete key/value pair plus its
	// separator. Dropping the dangling comma invents nothing, so this is a
	// legitimate recovery rather than a guess.
	cases := []struct{ raw, want string }{
		{`{"path": "a.go", "old_str": "x",`, `{"path":"a.go","old_str":"x"}`},
		{`{"summary":"s","steps":["a"],`, `{"summary":"s","steps":["a"]}`},
		{`{"summary":"s","steps":["a","b",`, `{"summary":"s","steps":["a","b"]}`},
	}
	for _, c := range cases {
		got, rung, err := Repair(c.raw, schema.Spec{})
		if err != nil {
			t.Errorf("%s → %v (rung %q)", c.raw, err, rung)
			continue
		}
		if !sameJSON(string(got), c.want) {
			t.Errorf("%s → %s, want %s", c.raw, got, c.want)
		}
		if rung != RungCloseBraces {
			t.Errorf("%s → rung %q, want %q", c.raw, rung, RungCloseBraces)
		}
	}
	// The legacy entry point must recover it too — pkg/evolve repairs tool-call
	// arguments through RepairToolArgs.
	fixed, err := RepairToolArgs(`{"path": "a.go", "old_str": "x",`)
	if err != nil {
		t.Fatalf("RepairToolArgs: %v", err)
	}
	if !sameJSON(fixed, `{"path":"a.go","old_str":"x"}`) {
		t.Errorf("RepairToolArgs = %s", fixed)
	}
}

func TestDanglingCommaFixLeavesStringsAlone(t *testing.T) {
	// A comma inside a string must never be stripped.
	for _, raw := range []string{
		`{"summary":"a, b, c","steps":[]}`,
		`{"steps":["one, two"],"summary":"s"}`,
	} {
		got, _, err := Repair(raw, schema.Spec{})
		if err != nil {
			t.Fatalf("%s → %v", raw, err)
		}
		if !sameJSON(string(got), raw) {
			t.Errorf("%s was rewritten to %s", raw, got)
		}
	}
}
