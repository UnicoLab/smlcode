package workspace

import (
	"fmt"
	"os"
	"strings"
)

// The shell allowlist is split into three tiers:
//
//	ReadOnlyPrefixes    — inspect the tree, cannot mutate it → auto-allow
//	BuildTestPrefixes   — compile/test runners → auto-allow (they are the point)
//	ExecutorPrefixes    — interpreters and command runners that can do ANYTHING
//	                      (python -c, node -e, perl -e, make, npx, go run …)
//	MutatorPrefixes     — file movers/rewriters (sed -i, cp, mv, install …)
//
// Only the first two are auto-allowed. Executors and mutators need an explicit
// entry in SLMCODE_BASH_ALLOW / the config allow list, because "python" on the
// allowlist is functionally identical to "no allowlist at all".

// ReadOnlyPrefixes never mutate the workspace.
//
// NOTE on `env`: it is matched WITHOUT a trailing space on purpose (bare `env`
// prints the environment) but `env <cmd>` execs an arbitrary program, so
// DangerousInvocation refuses any `env` that carries a command operand. Adding
// a prefix here that can name another program to run re-opens that hole; check
// DangerousInvocation before extending this list.
var ReadOnlyPrefixes = []string{
	"ls", "cat", "head", "tail", "wc", "pwd", "echo", "printf", "date",
	"which", "type", "env", "printenv", "uname", "whoami", "id",
	"git log", "git status", "git diff", "git show", "git branch",
	"git remote", "git stash list", "git tag", "git ls-files", "git rev-parse",
	"find ", "grep ", "rg ", "ag ", "fd ", "tree ",
	"pip show", "pip list", "npm list", "npm ls",
	"cargo metadata",
	"df ", "du ", "free ", "top -bn", "ps ",
	"curl -I", "curl --head",
	"mkdir ", "touch ",
	"true", "false", "test ", "[",
	"sort", "uniq", "cut", "diff ", "stat ", "file ", "basename ", "dirname ",
}

// BuildTestPrefixes are compilers/test runners the worker is expected to use.
var BuildTestPrefixes = []string{
	"go test", "go build", "go vet", "go fmt", "gofmt ", "go mod", "go list",
	"pytest", "python -m pytest", "python3 -m pytest",
	"python -m py_compile", "python3 -m py_compile",
	"python -m compileall", "python3 -m compileall",
	"python -m unittest", "python3 -m unittest",
	"node --check",
	"npm test", "npm run", "npm ci", "npm install",
	"cargo test", "cargo build", "cargo clippy", "cargo fmt", "cargo check",
	"mvn ", "./mvnw ", "gradle ", "./gradlew ",
	"ctest ", "cmake ",
	"bash -n", "shellcheck ",
	"tsc ", "eslint ", "ruff ", "mypy ", "black ", "flake8 ",
	// The npx forms of the SAME verification tools. `tsc` and `eslint` are
	// already builtin above, but a JS/TS project almost never has them on PATH
	// — they live in node_modules, and `npx tsc --noEmit` is how every one of
	// these packs actually spells it (it is the react and shadcn packs' own
	// typecheck and qa_gate). Leaving the npx form out made the pack's own
	// documented command reachable only through a pack-supplied allow entry,
	// which is one more thing that has to be wired correctly before a
	// typechecker can run. Measured: a live assembler run was refused for
	// `npx tsc --noEmit` — its own smoke command.
	//
	// These name a FIXED tool, unlike bare `npx`: the package is part of the
	// prefix, so none of them can be redirected at another program.
	// Trailing space is load-bearing, and it is why `tsc `/`eslint ` above
	// carry one too: matching is a plain HasPrefix, so `npx tsc` without it
	// also admits `npx tsc-evil`, an arbitrary package whose name merely
	// starts the same way.
	"npx tsc ", "npx vitest ", "npx jest ", "npx eslint ", "npx prettier ",
	"gcc ", "g++ ", "clang ", "clang++ ",
	"uv run pytest", "uv sync", "uv pip",

	// Component-library assembly is NOT a prefix — see IsAssemblerCommand in
	// assemblersafe.go. `npx shadcn add`, `npx shadcn@latest add` and
	// `npx --yes shadcn@4.19.0 add` are the same command, and a literal prefix
	// admits only one of them: measured, a live 30B lost a whole turn to a
	// refusal for writing the first.
}

// ExecutorPrefixes can run arbitrary code and therefore require explicit opt-in.
var ExecutorPrefixes = []string{
	"python", "python3", "node", "deno", "bun", "perl", "ruby", "php",
	"npx", "yarn", "pnpm", "make", "go run", "cargo run",
	"sh", "bash", "zsh", "ksh", "eval", "exec", "source", ".",
	"xargs", "sudo", "su", "ssh", "nc", "telnet", "gdb", "lldb",
	"awk", "gawk",
}

// MutatorPrefixes rewrite or relocate files behind the tool layer's back.
var MutatorPrefixes = []string{
	"sed", "cp", "mv", "rm", "rmdir", "install", "truncate", "rsync",
	"ln", "chmod", "chown", "shred", "dd", "tee", "patch", "git checkout",
	"git reset", "git clean", "git apply", "git stash",
}

// BuiltinSafePrefixes is the auto-allow set (read-only + build/test).
// Kept as a package-level var for existing callers and tests.
var BuiltinSafePrefixes = append(append([]string{}, ReadOnlyPrefixes...), BuildTestPrefixes...)

// ParseExtraPrefixes splits a comma-separated allow list (trailing spaces kept
// for word-boundary matching, like little-coder).
func ParseExtraPrefixes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimLeft(part, " \t")
		if strings.TrimSpace(p) == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// SafePrefixes merges builtins with extras (env SLMCODE_BASH_ALLOW + config).
func SafePrefixes(extra []string) []string {
	out := append([]string{}, BuiltinSafePrefixes...)
	out = append(out, ParseExtraPrefixes(os.Getenv("SLMCODE_BASH_ALLOW"))...)
	out = append(out, extra...)
	return out
}

// UnsafeShellSyntax reports shell constructs that make static analysis of a
// command meaningless, with a model-facing explanation.
//
// Command substitution hides an arbitrary nested command from every guard in
// this package: `echo $(rm -rf x)`, "[[ $(sed -i ...) ]]", process substitution
// `<(...)`/`>(...)`, and a bare `&` which starts a SECOND command the chain
// splitter never saw. There is no safe way to allow these.
func UnsafeShellSyntax(command string) (reason string, unsafe bool) {
	stripped := StripHeredocBodies(command)
	if at, tok := findUnquotedAny(stripped, []string{"$(", "`", "<(", ">("}); at >= 0 {
		return fmt.Sprintf(
			"shell refused — command substitution %q is not allowed. It hides a nested command "+
				"from every safety check.\nWrite the literal value instead, or run the inner command "+
				"first as its own ws_shell call and use the output.", tok), true
	}
	if at := findBareAmpersand(stripped); at >= 0 {
		return "shell refused — a bare `&` backgrounds the command and starts a second one that " +
			"escapes the safety checks. Run one command per ws_shell call and wait for its output.", true
	}
	return "", false
}

// findUnquotedAny returns the first index of any token outside quotes.
func findUnquotedAny(cmd string, tokens []string) (int, string) {
	quoted := byte(0)
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if quoted == 0 && ch == '\\' {
			i++
			continue
		}
		if quoted == 0 && (ch == '"' || ch == '\'') {
			quoted = ch
			continue
		}
		if quoted != 0 && ch == quoted {
			quoted = 0
			continue
		}
		// Single quotes suppress substitution; double quotes do NOT.
		if quoted == '\'' {
			continue
		}
		for _, t := range tokens {
			if strings.HasPrefix(cmd[i:], t) {
				return i, t
			}
		}
	}
	return -1, ""
}

// findBareAmpersand finds `&` that is neither `&&` nor part of a `2>&1` style
// fd duplication.
func findBareAmpersand(cmd string) int {
	quoted := byte(0)
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if quoted == 0 && ch == '\\' {
			i++
			continue
		}
		if quoted == 0 && (ch == '"' || ch == '\'') {
			quoted = ch
			continue
		}
		if quoted != 0 && ch == quoted {
			quoted = 0
			continue
		}
		if quoted != 0 || ch != '&' {
			continue
		}
		if i+1 < len(cmd) && cmd[i+1] == '&' {
			i++
			continue
		}
		if i > 0 && cmd[i-1] == '&' {
			continue
		}
		// fd duplication: `>&2`, `2>&1`, `&>file`
		if i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<') {
			continue
		}
		if i+1 < len(cmd) && cmd[i+1] == '>' {
			continue
		}
		return i
	}
	return -1
}

// IsSafeBash reports whether every chain segment is whitelisted, none writes
// via redirect/tee/dd/sed -i/cp/mv, and no substitution syntax is present.
func IsSafeBash(command string, prefixes []string) bool {
	if _, unsafe := UnsafeShellSyntax(command); unsafe {
		return false
	}
	if HasWriteRedirection(command) {
		return false
	}
	segs := splitCommandChain(command)
	if len(segs) == 0 {
		return false
	}
	if len(prefixes) == 0 {
		prefixes = BuiltinSafePrefixes
	}
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if !segmentHasSafePrefix(seg, prefixes) {
			return false
		}
		// A whitelisted BINARY can still name another program to run
		// (`env python -c`, `find -exec`, `go test -exec`, `cmake -P`) or
		// create a path outside the jail (`touch /etc/x`).
		if _, blocked := DangerousInvocation(seg); blocked {
			return false
		}
	}
	return true
}

func segmentHasSafePrefix(segment string, prefixes []string) bool {
	// The component-library installers are matched structurally rather than by
	// prefix, because the same command has several equally natural spellings.
	// IsAssemblerCommand returns false for the ones that name a remote
	// registry, so this cannot become a way past AssemblerFetchesRemoteCode.
	if IsAssemblerCommand(segment) {
		return true
	}
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(segment, p) {
			return true
		}
	}
	return false
}

// segmentBinary returns the leading command word of a chain segment.
func segmentBinary(segment string) string {
	fields := strings.Fields(segment)
	if len(fields) == 0 {
		return ""
	}
	// Skip leading VAR=value assignments.
	for _, f := range fields {
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") &&
			strings.IndexByte(f, '=') > 0 && !strings.ContainsAny(f[:strings.IndexByte(f, '=')], "/. ") {
			continue
		}
		return f
	}
	return fields[0]
}

// ClassifySegment names the tier a chain segment belongs to.
func ClassifySegment(segment string) string {
	bin := segmentBinary(segment)
	if bin == "" {
		return "unknown"
	}
	base := bin
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	for _, p := range ExecutorPrefixes {
		if base == p || segment == p || strings.HasPrefix(segment, p+" ") {
			return "executor"
		}
	}
	for _, p := range MutatorPrefixes {
		if base == p || segment == p || strings.HasPrefix(segment, p+" ") {
			return "mutator"
		}
	}
	return "unknown"
}

// GuardShellWhitelist refuses non-whitelisted shell in auto mode.
// Returns a model-facing refusal string when blocked.
func GuardShellWhitelist(command string, extra []string) (refuse string, blocked bool) {
	if reason, unsafe := UnsafeShellSyntax(command); unsafe {
		return reason, true
	}
	// Before the whitelist, not after: the install prefix IS whitelisted, so a
	// check that ran later would never see the command at all. This is the one
	// refusal that applies to a command the allow-list already admits.
	for _, seg := range splitCommandChain(command) {
		if reason, blocked := AssemblerFetchesRemoteCode(strings.TrimSpace(seg)); blocked {
			return reason, true
		}
	}
	prefixes := SafePrefixes(extra)
	if IsSafeBash(command, prefixes) {
		return "", false
	}
	// Report an exec-flag / out-of-jail escape with its own explanation before
	// falling through to the generic "not an allowed command" text, which would
	// name the wrong culprit (`env`, `find`, `go`).
	for _, seg := range splitCommandChain(command) {
		if reason, blocked := DangerousInvocation(strings.TrimSpace(seg)); blocked {
			return reason, true
		}
	}
	writes := DetectWriteTargets(command)
	if len(writes) > 0 {
		var paths []string
		for _, w := range writes {
			paths = append(paths, fmt.Sprintf("%q (%s)", w.Path, w.Kind))
		}
		return fmt.Sprintf(
			"shell whitelist: this command writes to %s. "+
				"Use ws_write for a new file, ws_edit/ws_patch for an existing one, and ws_mv to rename — "+
				"do not mutate files from the shell, the harness cannot checkpoint or review those writes.",
			strings.Join(paths, ", "),
		), true
	}
	offender := command
	for _, seg := range splitCommandChain(command) {
		seg = strings.TrimSpace(seg)
		if seg != "" && !segmentHasSafePrefix(seg, prefixes) {
			offender = seg
			break
		}
	}
	name := segmentBinary(offender)
	if name == "" {
		name = offender
	}
	switch ClassifySegment(offender) {
	case "executor":
		// The refused COMMAND, not only the binary. A message that says `"npx"
		// can execute arbitrary code` is unanswerable: npx runs a different
		// program per invocation, so the model cannot tell which spelling was
		// rejected and neither can an operator reading the log. Diagnosing a
		// real refusal here took a full extra run purely because the text never
		// said what had been refused.
		return fmt.Sprintf(
			"shell refused — %q can execute arbitrary code, so it needs explicit operator approval "+
				"(add it to shell_allow / SLMCODE_BASH_ALLOW).\nRefused command: %s\n"+
				"For verification use an allowed runner instead: `go test ./pkg/x -short`, "+
				"`python -m pytest -q`, `python -m py_compile <file>`, `node --check <file>`.",
			name, clipCommand(offender)), true
	case "mutator":
		return fmt.Sprintf(
			"shell refused — %q modifies files outside the tool layer, so edits cannot be "+
				"checkpointed, reviewed or reverted.\nRefused command: "+clipCommand(offender)+"\n"+
				"Use ws_edit / ws_patch to change a file, ws_write to create one, "+
				"ws_mv to rename, ws_delete to remove.", name), true
	}
	return fmt.Sprintf(
		"shell whitelist: %q is not an allowed command. Use an allowed verification command "+
			"(go test / go vet / pytest / python -m py_compile / node --check / npm test), "+
			"or make the change with ws_edit / ws_write / ws_patch.\nRefused command: %s",
		name, clipCommand(offender),
	), true
}

// clipCommand bounds a refused command quoted back into a prompt or a log.
func clipCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	const max = 200
	if len(cmd) <= max {
		return cmd
	}
	return cmd[:max] + "…"
}
