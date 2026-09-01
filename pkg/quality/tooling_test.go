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

// The measured case, and the shapes the other runners use to say the same
// thing. Each is the runner reporting it has no such script — not a verdict.
func TestCheckUndefinedSpotsAScriptTheProjectNeverDefined(t *testing.T) {
	for name, c := range map[string]struct{ command, output string }{
		// Live, on the frontend-react team: the model wrote components and a
		// package.json with no build script, and the team went RED for it.
		"npm, the one that was measured": {
			"npm --prefix web run build", `npm error Missing script: "build"`},
		"npm 8": {"npm run build", `npm ERR! Missing script: "build"`},
		"pnpm":  {"pnpm run lint", "ERR_PNPM_NO_SCRIPT  Missing script: lint"},
		"yarn":  {"yarn build", `error Command "build" not found.`},
		"make":  {"make test", "make: *** No rule to make target 'test'.  Stop."},
		// A valid command over a tree with nothing in it to compile or run.
		"go over an empty tree": {"go test ./...", `go: warning: "./..." matched no packages`},
	} {
		if !CheckUndefined(c.command, c.output) {
			t.Errorf("%s: %q + %q read as a real failure", name, c.command, c.output)
		}
		if got := CheckDidNotRun(c.command, c.output); got != "this project defines no such check" {
			t.Errorf("%s: reason = %q", name, got)
		}
	}
}

// The direction that matters. Excusing a real failure hides broken code, so
// every one of these must stay RED.
func TestCheckUndefinedDoesNotExcuseARealFailure(t *testing.T) {
	for name, c := range map[string]struct{ command, output string }{
		"a build that ran and found a type error": {
			"npm run build", "error TS2345: Argument of type 'string' is not assignable"},
		// The runner names a script we did not ask for, so it is not answering
		// for this command.
		"a different script is missing": {
			"npm run build", `npm error Missing script: "test"`},
		"a different make target": {
			"make test", "make: *** No rule to make target 'clean'.  Stop."},
		"a compile error": {"go build ./...", "./main.go:9:2: undefined: handler"},
		// `go test ./...` that really did test something and failed.
		"a failing go test": {"go test ./...", "--- FAIL: TestThing (0.00s)\nFAIL"},
		"a failing assertion that mentions the script name": {
			"npm run build", `AssertionError: expected "build" to equal "release"`},
		"nothing at all": {"npm run build", ""},
	} {
		if CheckUndefined(c.command, c.output) {
			t.Errorf("%s: %q + %q was excused — a real failure must stay red", name, c.command, c.output)
		}
		if got := CheckDidNotRun(c.command, c.output); got != "" {
			t.Errorf("%s: reason = %q, want none", name, got)
		}
	}
}

// The two axes stay distinct: absent tooling is a fact about the machine, an
// undefined script a fact about the project, and the user is told which.
func TestCheckDidNotRunNamesWhichKindOfNothingHappened(t *testing.T) {
	if got := CheckDidNotRun("npm run build", "npm: command not found"); got != "tooling is not installed here" {
		t.Errorf("absent tooling reported as %q", got)
	}
	if got := CheckDidNotRun("npm run build", `npm error Missing script: "build"`); got != "this project defines no such check" {
		t.Errorf("undefined script reported as %q", got)
	}
}
