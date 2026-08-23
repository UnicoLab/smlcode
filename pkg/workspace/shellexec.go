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
	// -P runs a CMake script file (a script is `execute_process` with extra
	// steps), -E is cmake's command mode (`cmake -E rm`, `cmake -E copy`) i.e.
	// a file mutator, -C loads an "initial cache" script which is likewise
	// arbitrary CMake code, and --install writes to an arbitrary --prefix.
	//
	// `cmake .`, `cmake -S . -B build` and `cmake --build build` are NOT in this
	// list: they drive the project's own build, which is exactly what `go
	// build`, `mvn test` and `cargo build` do. Running the project's build is
	// the documented-inherent risk of a coding harness, not a whitelist bug.
	// Their path OPERANDS are audited separately (cmakePathEscape) so they
	// cannot point outside the workspace.
	//
	// cmakeInvocation owns the whole cmake audit and consults this entry.
	"cmake": {"-P", "-E", "-C", "--install"},
}

// cmakeRefusedFlagWhy explains each refused cmake mode in its own terms. The
// generic execFlagPrefixes message ("names another program to execute") is only
// true of -P and -C; saying it about -E or --install teaches the model the
// wrong rule and it retries with the same class of command.
var cmakeRefusedFlagWhy = map[string]string{
	"-P": "runs a CMake script, and a CMake script can `execute_process` anything",
	"-C": "loads an initial-cache CMake script, which is arbitrary CMake code",
	"-E": "is cmake's command mode (`cmake -E copy`, `cmake -E rm`), a file mutator " +
		"that bypasses ws_write/ws_edit and the checkpointer",
	"--install": "copies build output to an arbitrary --prefix, outside the workspace",
}

// cmakeInvocation is the whole cmake audit: refuse the script/mutator/installer
// modes, allow the build modes, and keep the build modes inside the workspace.
func cmakeInvocation(args []string) (string, bool) {
	for _, f := range execFlagPrefixes["cmake"] {
		if flagIndex(args, f) < 0 {
			continue
		}
		return fmt.Sprintf(
			"shell refused — `cmake %s` %s.\n"+
				"Building the project is fine: `cmake -S . -B build` to configure and "+
				"`cmake --build build` to build. Use ws_write/ws_edit/ws_mv for file changes.",
			f, cmakeRefusedFlagWhy[f]), true
	}
	return cmakePathEscape(args)
}

// cmakeOpaqueValueFlags take a separate value that is not a path the harness
// needs to police (a generator name, a target, a job count).
var cmakeOpaqueValueFlags = map[string]bool{
	"-G": true, "-A": true, "-T": true, "-D": true, "-U": true,
	"-t": true, "--target": true, "--config": true, "--parallel": true,
	"-j": true, "--preset": true, "--log-level": true, "--graphviz": true,
	"--component": true, "--prefix": true, "--toolset": true,
}

// cmakePathFlags name a directory cmake reads from or writes into.
var cmakePathFlags = map[string]bool{
	"-S": true, "-B": true, "--build": true,
}

// cmakePathEscape refuses a cmake invocation whose source/build directory
// leaves the workspace. `cmake --build build` is the canonical way to build a
// CMake project and stays allowed; `cmake --build /tmp/x`, `cmake --build
// ../out` and `cmake -S . -B /var/tmp/o` write outside the jail, which is the
// same out-of-workspace primitive `touch /etc/x` was refused for.
//
// The shell always runs with cwd == the project root, so "relative and not
// climbing out" is exactly "inside the workspace".
func cmakePathEscape(args []string) (string, bool) {
	check := func(flag, val string) (string, bool) {
		val = unquote(val)
		if val == "" || !outsideWorkspaceRel(val) {
			return "", false
		}
		what := "operand"
		if flag != "" {
			what = "`" + flag + "` operand"
		}
		return fmt.Sprintf(
			"shell refused — the cmake %s %q is outside the project root. "+
				"The harness only builds inside the workspace: use a relative directory, "+
				"e.g. `cmake -S . -B build` then `cmake --build build`.", what, val), true
	}
	for i := 0; i < len(args); i++ {
		a := unquote(args[i])
		if a == "--" {
			continue
		}
		if name, val, ok := strings.Cut(a, "="); ok && cmakePathFlags[name] {
			if reason, blocked := check(name, val); blocked {
				return reason, true
			}
			continue
		}
		if cmakePathFlags[a] {
			if i+1 < len(args) {
				if reason, blocked := check(a, args[i+1]); blocked {
					return reason, true
				}
				i++
			}
			continue
		}
		if cmakeOpaqueValueFlags[a] {
			i++ // flag with a separate, non-path value
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		// A bare operand is cmake's source (or build) directory.
		if reason, blocked := check("", a); blocked {
			return reason, true
		}
	}
	return "", false
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
	case "cmake":
		return cmakeInvocation(args)
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
