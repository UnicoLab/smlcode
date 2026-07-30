package plan

import "testing"

func TestParseTesterJSONPassed(t *testing.T) {
	r := ParseTesterJSON(`{"passed":true,"commands":["go test"],"summary":"ok"}`)
	if !r.Passed {
		t.Fatal(r)
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
