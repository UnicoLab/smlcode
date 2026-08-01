package plan

import "testing"

func TestParseTesterJSONPassed(t *testing.T) {
	// Fabricated commands[] alone is not enough — need an execution trace.
	r := ParseTesterJSON(`{"passed":true,"commands":["go test"],"summary":"ok"}`)
	if r.Passed {
		t.Fatal("commands[] without Observation/smoke must not pass")
	}
	r = ParseTesterJSON("Observation: go test ./... -short\nok\n" +
		`{"passed":true,"commands":["go test ./... -short"],"summary":"ok"}`)
	if !r.Passed {
		t.Fatalf("shell evidence should pass: %+v", r)
	}
	r = ParseTesterJSON("## Deterministic smoke\nPASSED\ncmd: python -m py_compile main.py\n" +
		`{"passed":true,"commands":["python -m py_compile main.py"],"summary":"ok"}`)
	if !r.Passed {
		t.Fatalf("smoke section should pass: %+v", r)
	}
}

func TestParseTesterJSONRejectsPassWithoutCommands(t *testing.T) {
	r := ParseTesterJSON(`{"passed":true,"summary":"looks fine","failures":[]}`)
	if r.Passed {
		t.Fatal("pass without commands must fail")
	}
	// Disk/rename acceptance remains valid without shell commands.
	r2 := ParseTesterJSON(`{"passed":true,"summary":"rename verified on disk","failures":[]}`)
	if !r2.Passed {
		t.Fatalf("disk pass should remain: %+v", r2)
	}
}

func TestParseTesterJSONFailedExplicit(t *testing.T) {
	r := ParseTesterJSON(`{"passed":false,"failures":["agent.py is placeholder"],"summary":"does not work"}`)
	if r.Passed || len(r.Failures) == 0 {
		t.Fatalf("%+v", r)
	}
}

func TestParseTesterJSONContradictoryFailures(t *testing.T) {
	r := ParseTesterJSON(`{"passed":true,"failures":["still broken"]}`)
	if r.Passed {
		t.Fatal("failures must force failed")
	}
}

func TestParseTesterJSONProseFailure(t *testing.T) {
	r := ParseTesterJSON("The implementation does not work — tests failed on import.")
	if r.Passed {
		t.Fatal("expected failed from prose")
	}
	if !TesterFailed("The implementation does not work") {
		t.Fatal("TesterFailed")
	}
}

func TestParseTesterJSONUnclearNotAccepted(t *testing.T) {
	r := ParseTesterJSON("I looked at the files.")
	if r.Passed {
		t.Fatal("unclear tester output must not auto-pass")
	}
}

func TestParseTesterJSONEmptyForcesFailure(t *testing.T) {
	r := ParseTesterJSON("")
	if r.Passed || len(r.Failures) == 0 {
		t.Fatalf("empty must fail with reason: %+v", r)
	}
	if !TesterFailed("") {
		t.Fatal("TesterFailed empty")
	}
	r2 := ParseTesterJSON(`{}`)
	if r2.Passed || len(r2.Failures) == 0 {
		t.Fatalf("{} must fail: %+v", r2)
	}
	r3 := ParseTesterJSON(`{not json`)
	if r3.Passed || len(r3.Failures) == 0 {
		t.Fatalf("malformed must fail: %+v", r3)
	}
}
