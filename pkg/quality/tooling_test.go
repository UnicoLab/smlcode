package quality

import "testing"

// The measured case: a machine with no pytest installed. The worker wrote the
// file it was asked for, the smoke exited non-zero, the reviewer rejected on
// that evidence, and the corrector rewrote correct code until the task
// escalated for human review.
func TestMissingTestRunnerIsNotTheCodesFault(t *testing.T) {
	for _, tc := range []struct{ cmd, out string }{
		{"python -m pytest -q", "/usr/bin/python3: No module named pytest"},
		{"python3 -m pytest -q", "ModuleNotFoundError: No module named 'pytest'"},
		{"pytest -q", "bash: pytest: command not found"},
		{"npx vitest run", "sh: vitest: command not found"},
		{"go test ./...", "bash: go: command not found"},
		{"golangci-lint run", "exec: \"golangci-lint\": executable file not found in $PATH"},
		{"uv run pytest", "error: No module named pytest"},
	} {
		if !ToolingMissing(tc.cmd, tc.out) {
			t.Errorf("ToolingMissing(%q, %q) = false, want true", tc.cmd, tc.out)
		}
	}
}

// The distinction that makes this safe. A missing APPLICATION module is the
// code under test failing to import — exactly the fault the check exists to
// find — and must never be excused as absent tooling.
func TestRealFailuresAreNeverExcused(t *testing.T) {
	for _, tc := range []struct{ cmd, out string }{
		{"python -m pytest -q", "ModuleNotFoundError: No module named 'myapp'"},
		{"python -m pytest -q", "E   ImportError: cannot import name 'Task' from 'store'"},
		{"python -m pytest -q", "1 failed, 3 passed in 0.4s"},
		{"go test ./...", "--- FAIL: TestMedianEven (0.00s)\nFAIL\texample/stats\t0.2s"},
		{"go test ./...", "stats.go:12:2: undefined: sortInts"},
		{"npx vitest run", "FAIL src/App.test.tsx > renders\nAssertionError: expected 1 to be 2"},
		// Mentions a tool AND a not-found, but on unrelated lines: a genuine
		// failure whose output happens to name the runner must still count.
		{"go test ./...", "--- FAIL: TestLoad\n    load.go:8: open fixtures/x.json: no such file or directory"},
		{"python -m pytest -q", ""},
		{"", "No module named pytest"},
	} {
		if ToolingMissing(tc.cmd, tc.out) {
			t.Errorf("ToolingMissing(%q, %q) = true — a real failure was excused", tc.cmd, tc.out)
		}
	}
}

func TestCommandToolsFindsTheRealEntrypoint(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want string
	}{
		{"python -m pytest -q", "pytest"},
		{"npx vitest run", "vitest"},
		{"uv run pytest", "pytest"},
		{"go test ./...", "go"},
	} {
		got := commandTools(tc.cmd)
		found := false
		for _, g := range got {
			if g == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("commandTools(%q) = %v, missing %q", tc.cmd, got, tc.want)
		}
		// Paths and flags are not entrypoints.
		for _, g := range got {
			if g == "./..." || g == "-q" || g == "-m" {
				t.Errorf("commandTools(%q) returned a non-program %q", tc.cmd, g)
			}
		}
	}
}

// `npm run test` names a package.json SCRIPT, not a program. Treating it as one
// pushes errors in the dangerous direction: a genuine failure whose output
// happens to mention "test" or "build" beside a launcher error would be excused
// as absent tooling and the broken code would pass.
func TestScriptNamesAreNotTreatedAsPrograms(t *testing.T) {
	for _, cmd := range []string{"npm run test", "npm run build", "yarn run lint", "pnpm run dev"} {
		for _, got := range commandTools(cmd) {
			switch got {
			case "test", "build", "lint", "dev":
				t.Errorf("commandTools(%q) treated the script %q as a program", cmd, got)
			}
		}
	}
	// A real failure that merely mentions the script name must stay a failure.
	if ToolingMissing("npm run test", "sh: test: command not found") {
		t.Error("a script name acquitted a genuine failure")
	}
	// The launcher forms this DOES cover still work.
	if !ToolingMissing("uv run pytest", "error: No module named pytest") {
		t.Error("uv run pytest regressed")
	}
	if !ToolingMissing("npx vitest run", "sh: vitest: command not found") {
		t.Error("npx vitest regressed")
	}
}
