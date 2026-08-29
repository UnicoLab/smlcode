package quality

import (
	"regexp"
	"strings"
)

// Telling "the code is wrong" apart from "the checker isn't installed".
//
// A verification command that cannot be LAUNCHED proves nothing about the code
// it was going to check. The harness had no way to say that: RunSmoke reports
// Ran/OK, and a non-zero exit is a non-zero exit, so `python -m pytest -q` on a
// machine without pytest scored exactly like a suite that ran and found a bug.
//
// Measured, on a machine with no pytest installed: a worker wrote the file it
// was asked for, the acceptance smoke exited non-zero with "No module named
// pytest", the reviewer rejected the task on that evidence, the corrector
// rewrote correct code, the smoke failed identically, and the task escalated to
// to_scope needing human review. The run spent its whole retry budget fixing a
// missing dependency by editing source.
//
// The distinction is narrow on purpose. Only the command's OWN entrypoint
// counts: "No module named pytest" for `python -m pytest` is absent tooling,
// while "No module named myapp" from that same command is the code under test
// failing to import — the exact fault the check exists to find. Matching any
// not-found message would turn every genuine import error into a shrug.

// launcherErrors are the shell/exec layer saying it could not start a program.
// The tool's own name is checked separately by ToolingMissing.
var launcherErrors = []string{
	"command not found",
	"executable file not found",
	"no such file or directory",
	"is not recognized as an internal or external command",
	"not found",
}

// moduleMissingRe captures the module name Python reports as absent, so it can
// be compared against the module the command actually invoked.
var moduleMissingRe = regexp.MustCompile(
	`(?i)(?:ModuleNotFoundError: )?No module named ['"]?([A-Za-z0-9_.\-]+)`)

// ToolingMissing reports whether output shows command could not be launched,
// as opposed to running and finding a fault.
//
// False is the safe answer and the default: a real failure misread as absent
// tooling would silently excuse broken code, which is the failure mode this
// package exists to prevent. Every rule below therefore requires the message to
// name the command's own entrypoint.
func ToolingMissing(command, output string) bool {
	tools := commandTools(command)
	if len(tools) == 0 || strings.TrimSpace(output) == "" {
		return false
	}
	low := strings.ToLower(output)

	// Python: `python -m pytest` failing with "No module named pytest".
	for _, m := range moduleMissingRe.FindAllStringSubmatch(output, -1) {
		if len(m) < 2 {
			continue
		}
		// Compare the ROOT package: "pytest.main" is still pytest.
		missing := strings.ToLower(m[1])
		if i := strings.Index(missing, "."); i > 0 {
			missing = missing[:i]
		}
		for _, tool := range tools {
			if missing == tool {
				return true
			}
		}
	}

	// Shell/exec: the message names the tool and says it could not be started.
	for _, line := range strings.Split(low, "\n") {
		for _, tool := range tools {
			// The tool name and the complaint must be on the SAME line, or an
			// unrelated "not found" elsewhere in the output would acquit a
			// genuine failure that merely mentioned the tool by name.
			if !namesTool(line, tool) {
				continue
			}
			for _, sig := range launcherErrors {
				if strings.Contains(line, sig) {
					return true
				}
			}
		}
	}
	return false
}

// namesTool reports whether line refers to tool as a program, not as part of a
// longer identifier.
//
// Substring matching is not good enough, and the case that proves it is
// ordinary Go output: `load.go:8: open fixtures/x.json: no such file or
// directory` contains "go" and a launcher error, so a genuine missing-fixture
// failure read as an absent Go toolchain and would have been excused. A dot is
// therefore a word character here — "load.go" does not name `go`.
func namesTool(line, tool string) bool {
	isWord := func(b byte) bool {
		return b == '.' || b == '_' || b == '-' ||
			(b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	for i := 0; ; {
		j := strings.Index(line[i:], tool)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(tool)
		beforeOK := start == 0 || !isWord(line[start-1])
		afterOK := end == len(line) || !isWord(line[end])
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
		if i >= len(line) {
			return false
		}
	}
}

// commandTools returns the entrypoint names a command depends on: the program
// itself, plus the runner it delegates to (`python -m pytest` → python, pytest;
// `npx vitest run` → npx, vitest). Flags and paths are skipped.
func commandTools(command string) []string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return nil
	}
	var out []string
	add := func(s string) {
		s = strings.ToLower(strings.Trim(s, `"'`))
		// Only a bare program name — a path or an option is not one.
		if s == "" || strings.HasPrefix(s, "-") || strings.ContainsAny(s, "/\\.=") {
			return
		}
		for _, e := range out {
			if e == s {
				return
			}
		}
		out = append(out, s)
	}
	add(fields[0])
	launcher := isLauncher(fields[0])
	for i := 1; i < len(fields); i++ {
		// `python -m pytest`: the module IS the program being run.
		if fields[i] == "-m" && i+1 < len(fields) {
			add(fields[i+1])
			continue
		}
		// `uv run pytest`, `poetry run pytest` — but NOT `npm run test`, where
		// the token is a package.json SCRIPT name and no program by that name
		// need exist. Treating it as one risks the dangerous direction: a
		// genuine failure mentioning "test" or "build" next to a launcher error
		// would be excused as absent tooling.
		if launcher && (fields[i] == "run" || fields[i] == "exec") && i+1 < len(fields) {
			add(fields[i+1])
			continue
		}
		// `npx vitest`: the first bare word after the launcher.
		if launcher && i == 1 {
			add(fields[i])
		}
	}
	return out
}

// isLauncher reports whether a program's job is to run ANOTHER program, whose
// absence is the one that matters.
func isLauncher(prog string) bool {
	switch strings.ToLower(prog) {
	case "npx", "pnpx", "bunx", "uv", "uvx", "poetry", "pipenv", "hatch", "rye", "pdm", "tox":
		return true
	}
	return false
}
