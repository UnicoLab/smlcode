---
name: go-table-tests
description: Write Go tests the standard way — table-driven subtests, t.Helper, t.Cleanup, and what to assert instead of reflect.DeepEqual.
triggers: go test, table test, subtests, testify, testing.T, _test.go, benchmark
agents: worker, tester, deep, corrector, go-worker, go-tester
paths: "**/*_test.go, **/*.go"
user-invocable: true
---

# Table-driven tests in Go

The shape the standard library uses. Deviating from it makes a reviewer slower,
not the test better.

```go
func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Config
		wantErr string // substring; "" means no error
	}{
		{name: "empty", in: "", wantErr: "empty input"},
		{name: "ok", in: "a=1", want: Config{A: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}
```

## Rules that matter

- **Name every case.** `t.Run(tt.name, …)` makes `go test -run TestParse/ok`
  work and puts the case name in the failure line.
- **`t.Fatalf` when the test cannot continue** (setup failed, err when none was
  wanted); **`t.Errorf` when it can** — an Errorf keeps checking the rest of the
  case and reports more per run.
- **Failure messages state input, got and want**, in that order:
  `t.Errorf("Sum(%v) = %d, want %d", in, got, want)`. "test failed" tells the
  next reader nothing.
- **`t.Helper()`** as the first line of any assertion helper, or failures point
  at the helper instead of the caller.
- **`t.Cleanup(fn)`** over `defer` in helpers — it runs after subtests finish.
- **`t.TempDir()`** for files; it is removed automatically. Never write into the
  repo from a test.
- **`t.Parallel()`** goes in BOTH the parent and each subtest, and the loop
  variable must not be captured across it in Go < 1.22.
- **Compare with `==` for comparable structs**, `slices.Equal`/`maps.Equal` for
  slices and maps, `cmp.Diff` when the project already uses go-cmp.
  `reflect.DeepEqual` treats a nil slice and an empty slice as different and
  gives a useless diff.
- **Errors:** compare with `errors.Is(err, ErrClosed)` for sentinels and
  `errors.As(err, &target)` for typed errors. String matching on an error
  message is a test of the message, not of the behaviour.
- A test with no assertion is not a test. Neither is one that only checks that
  a function did not panic, unless not panicking is the documented contract.
