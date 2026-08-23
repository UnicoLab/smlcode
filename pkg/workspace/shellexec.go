package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Whitelisted-prefix escape hatches.
//
// The tier lists in shellsafe.go answer "which BINARY may run". That is not
// enough: several binaries on the auto-allow list take a flag whose value is
// another program to execute, or an operand that writes outside the workspace.
// `env python -c …`, `find . -exec sh -c … ;`, `go test -exec …`,
// `go build -toolexec …`, `cmake -P evil.cmake` and `touch /etc/x` all cleared
// the whitelist unchanged while running (or creating) whatever the caller
// named. DangerousInvocation is the per-binary flag audit that closes them.
//
// Everything here is a WHITELIST BUG, not an inherent property of running the
// project's own build: none of these forms is needed to compile or test a
// project, and each names its payload directly rather than inheriting it from
// the repository's own configuration.

// execFlagPrefixes are flags whose value names a program to run. Matched both
// as `--flag=value` and as `--flag value`.
var execFlagPrefixes = map[string][]string{
	// -gcflags/-asmflags/-ldflags all forward a nested -toolexec / -fuse-ld /
	// -fplugin to the compiler or the system linker, which is another program
	// of the caller's choosing. None of them is needed to build or test.
	"go": {"-exec", "-toolexec", "-vettool", "-overlay",
		"-gcflags", "-asmflags", "-ldflags", "-compiler"},
	"gofmt":  nil,
	"cargo":  {"--config"},
	"npm":    {"--node-options"},
	"pnpm":   {"--node-options"},
	"yarn":   {"--node-options"},
	"tsc":    {"--plugin"},
	"eslint": {"--rulesdir", "--resolve-plugins-relative-to"},
	"mypy":   {"--custom-typeshed-dir"},
	"pytest": {"-p", "--rootdir"},
	"ctest":  {"--build-and-test", "--test-command", "--build-generator"},
	// -P runs a CMake script file and -E is cmake's command mode (`cmake -E rm`,
	// `cmake -E copy`); --install writes to an arbitrary --prefix. `cmake .` and
	// `cmake --build <dir>` drive the project's OWN build and stay allowed —
	// that is inherent to compiling the project, not a flag escape.
	"cmake": {"-P", "-E", "--install"},
}

// findExecActions are `find` primaries that run or write something.
var findExecActions = map[string]bool{
	"-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
	"-delete": true, "-fprintf": true, "-fprint": true, "-fprint0": true,
	"-fls": true,
}

// pathCreatingBinaries are auto-allowed commands whose operands are paths they
// create. They are harmless inside the workspace and an out-of-jail write
// outside it.
var pathCreatingBinaries = map[string]bool{"mkdir": true, "touch": true}

// DangerousInvocation inspects ONE chain segment that already cleared the
// prefix whitelist and reports a refusal when its flags or operands turn it
// into an arbitrary-command or out-of-workspace-write primitive.
func DangerousInvocation(segment string) (reason string, blocked bool) {
	words := splitWords(segment)
	for len(words) > 0 && isAssignmentWord(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return "", false
	}
	bin := words[0]
	if i := strings.LastIndexByte(bin, '/'); i >= 0 {
		bin = bin[i+1:]
	}
	bin = strings.TrimSuffix(strings.ToLower(bin), ".exe")
	args := words[1:]

	switch bin {
	case "env":
		// `env`, `env -0`, `env FOO=1` only print. `env <prog>` execs.
		return envRunsProgram(args)
	case "find":
		for _, a := range args {
			if findExecActions[unquote(a)] {
				return fmt.Sprintf(
					"shell refused — `find %s` runs or deletes files for every match, which bypasses "+
						"every harness guard.\nUse plain `find` to LIST paths, then act on them with "+
						"ws_edit / ws_delete / ws_mv.", unquote(a)), true
			}
		}
		return "", false
	case "go":
		if len(args) > 0 && strings.EqualFold(unquote(args[0]), "generate") {
			return "shell refused — `go generate` executes //go:generate directives found in the " +
				"source tree, i.e. arbitrary commands chosen by the repository. " +
				"Run the generator you actually need as its own approved command.", true
		}
	}

	if flags, ok := execFlagPrefixes[bin]; ok {
		for _, f := range flags {
			if at := flagIndex(args, f); at >= 0 {
				return fmt.Sprintf(
					"shell refused — `%s %s` names another program for %s to execute, so the "+
						"whitelist would not see it.\nRun the verification command directly "+
						"(e.g. `go test ./pkg/x -short`, `python -m pytest -q`).", bin, f, bin), true
			}
		}
	}
	if pathCreatingBinaries[bin] {
		for _, a := range operands(args, "-m", "--mode", "-d", "--date", "-r", "--reference", "-t") {
			p := unquote(a)
			if p == "" || strings.HasPrefix(p, "-") {
				continue
			}
			if outsideWorkspaceRel(p) {
				return fmt.Sprintf(
					"shell refused — `%s %s` creates a path outside the project root. "+
						"The harness only operates on files inside the workspace.", bin, p), true
			}
		}
	}
	return "", false
}

// envRunsProgram reports whether an `env` invocation execs a program.
func envRunsProgram(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := unquote(args[i])
		switch {
		case a == "--":
			if i+1 < len(args) {
				return envRefusal(unquote(args[i+1])), true
			}
			return "", false
		case a == "-u" || a == "--unset" || a == "-C" || a == "--chdir" ||
			a == "-S" || a == "--split-string":
			i++ // flag with a separate value
		case strings.HasPrefix(a, "-"):
			// -i / -0 / --ignore-environment and friends: no operand.
		case isAssignmentWord(a):
			// NAME=value, still just environment.
		default:
			return envRefusal(a), true
		}
	}
	return "", false
}

func envRefusal(prog string) string {
	return fmt.Sprintf(
		"shell refused — `env %s` runs %s with the whitelist looking only at `env`. "+
			"Run %s directly so the harness can classify it (and add it to shell_allow "+
			"if it genuinely needs to run).", prog, prog, prog)
}

// flagIndex finds `-flag` or `-flag=value` in args, stopping at `--`.
func flagIndex(args []string, flag string) int {
	alt := flag
	if strings.HasPrefix(flag, "--") {
		alt = flag[1:] // Go's flag package accepts single-dash long flags too
	} else if strings.HasPrefix(flag, "-") {
		alt = "-" + flag
	}
	for i, raw := range args {
		a := unquote(raw)
		if a == "--" {
			return -1
		}
		for _, f := range []string{flag, alt} {
			if a == f || strings.HasPrefix(a, f+"=") {
				return i
			}
		}
	}
	return -1
}

// outsideWorkspaceRel reports whether a shell operand escapes the project root.
// The shell always runs with cwd == root, so an absolute path or a `..` that
// climbs out is an out-of-jail write.
func outsideWorkspaceRel(p string) bool {
	if p == "" {
		return false
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return true
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	return cleaned == ".." || strings.HasPrefix(cleaned, "../")
}
