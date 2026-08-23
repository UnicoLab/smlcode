package evolve

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckNormalizeInfersKind(t *testing.T) {
	tests := []struct {
		name string
		in   Check
		want CheckKind
	}{
		{"command", Check{Description: "d", Command: "go test ./..."}, CheckCommand},
		{"file contains", Check{Description: "d", Path: "a.go", Substring: "func A"}, CheckFileContains},
		{"file exists", Check{Description: "d", Path: "a.go"}, CheckFileExists},
		{"nothing", Check{Description: "d"}, CheckNone},
		{"explicit wins", Check{Description: "d", Path: "a.go", Substring: "x", Kind: CheckFileAbsent}, CheckFileAbsent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.in
			c.Normalize(nowFixed())
			if c.Kind != tc.want {
				t.Errorf("kind = %q, want %q", c.Kind, tc.want)
			}
			if c.ID == "" {
				t.Error("no id assigned")
			}
			if tc.want == CheckNone && c.Runnable() {
				t.Error("a check with no verification should not be runnable")
			}
		})
	}
}

func TestRegressionsAddIsIdempotentAndPersists(t *testing.T) {
	dir := t.TempDir()
	r, err := OpenRegressions(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := Check{Description: "old_str fix stays", Path: "a.go", Substring: "func A", Fingerprint: "fp_x"}
	first := r.Add(c)
	second := r.Add(c)
	if first.ID != second.ID || r.Count() != 1 {
		t.Fatalf("Add was not idempotent: %d checks", r.Count())
	}
	if got := r.Add(Check{}); got.ID != "" {
		t.Error("a description-less check should be rejected")
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".slmcode", "evolve", "regressions.json")); err != nil {
		t.Fatalf("regressions.json missing: %v", err)
	}
	r2, _ := OpenRegressions(dir)
	if r2.Count() != 1 {
		t.Fatalf("reloaded %d checks", r2.Count())
	}
	if r2.Checks()[0].Description != c.Description {
		t.Errorf("reload lost the description: %+v", r2.Checks()[0])
	}
}

func TestRegressionsRunOffline(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := OpenRegressions(t.TempDir())
	r.Add(Check{Description: "fix present", Path: "a.go", Substring: "func A"})
	r.Add(Check{Description: "bug gone", Path: "a.go", Substring: "panic(", Kind: CheckFileAbsent})
	r.Add(Check{Description: "regressed", Path: "a.go", Substring: "func Missing"})
	r.Add(Check{Description: "file gone", Path: "gone.go", Kind: CheckFileExists})
	r.Add(Check{Description: "needs a shell", Command: "go test ./..."})

	results := r.RunOffline(root)
	if len(results) != 4 {
		t.Fatalf("evaluated %d checks, want the 4 offline ones (command checks are never run here)", len(results))
	}
	byDesc := map[string]Result{}
	for _, res := range results {
		byDesc[res.Check.Description] = res
	}
	for desc, wantOK := range map[string]bool{
		"fix present": true, "bug gone": true, "regressed": false, "file gone": false,
	} {
		got, ok := byDesc[desc]
		if !ok {
			t.Fatalf("check %q was not evaluated", desc)
		}
		if got.OK != wantOK {
			t.Errorf("check %q: OK = %v, want %v (%s)", desc, got.OK, wantOK, got.Detail)
		}
	}
	// Results were recorded.
	for _, c := range r.Checks() {
		if c.Offline() && c.Runs == 0 {
			t.Errorf("check %q was evaluated but not recorded", c.Description)
		}
	}
	if len(r.Runnable()) != 5 {
		t.Errorf("runnable checks = %d, want 5", len(r.Runnable()))
	}
}

func TestRegressionsRecordAndPrune(t *testing.T) {
	r, _ := OpenRegressions(t.TempDir())
	c := r.Add(Check{Description: "d", Command: "go build ./..."})
	if !r.Record(c.ID, false) {
		t.Fatal("Record on a known check returned false")
	}
	if r.Record("nope", true) {
		t.Error("Record on an unknown id should return false")
	}
	got := r.Checks()[0]
	if got.Runs != 1 || got.Fails != 1 || got.LastOK {
		t.Fatalf("record not applied: %+v", got)
	}
	for i := 0; i < 300; i++ {
		r.Add(Check{Description: "check " + itoa(i), Command: "cmd " + itoa(i)})
	}
	r.Prune(20)
	if r.Count() > 20 {
		t.Errorf("store has %d checks after pruning to 20", r.Count())
	}
}

func TestRegressionsSurviveCorruptFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".slmcode", "evolve")
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "regressions.json"), []byte("]["), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRegressions(dir)
	if err != nil {
		t.Fatalf("must tolerate corruption: %v", err)
	}
	if r.Count() != 0 || len(r.Warnings()) == 0 {
		t.Errorf("count=%d warnings=%v", r.Count(), r.Warnings())
	}
	if c := r.Add(Check{Description: "still works", Command: "x"}); c.ID == "" {
		t.Error("store unusable after corruption")
	}
}

func TestRegressionsForget(t *testing.T) {
	dir := t.TempDir()
	r, _ := OpenRegressions(dir)
	r.Add(Check{Description: "d", Command: "x"})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := r.Forget(); err != nil {
		t.Fatal(err)
	}
	if r.Count() != 0 {
		t.Error("Forget left checks behind")
	}
	if _, err := os.Stat(filepath.Join(dir, ".slmcode", "evolve", "regressions.json")); !os.IsNotExist(err) {
		t.Errorf("regressions.json survived Forget: %v", err)
	}
}

func TestRegressionsInMemory(t *testing.T) {
	r, err := OpenRegressions("")
	if err != nil {
		t.Fatal(err)
	}
	r.Add(Check{Description: "d", Command: "x"})
	if r.Count() != 1 {
		t.Error("in-memory store does not work")
	}
	if err := r.Save(); err != nil {
		t.Errorf("Save on an in-memory store should be a no-op: %v", err)
	}
}

func nowFixed() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}
