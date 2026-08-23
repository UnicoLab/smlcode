package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
)

// noteUninitialized prints a one-line "there is no workspace here" banner.
//
// The read-only commands answer from built-in defaults when .slmcode/ does not
// exist, which is right — but they said nothing about it, so `slmcode plan` in
// the wrong directory printed a header and a blank line, and `slmcode status`
// described a configuration that has never been saved anywhere. It returns
// true when it printed, so callers can skip an empty body.
func noteUninitialized(root string) bool {
	if harness.Initialized(root) {
		return false
	}
	fmt.Println(cli.Warn("no .slmcode/ workspace in " + root + " — showing built-in defaults"))
	fmt.Println(cli.Dim("  slmcode init      scaffold memory, board and config here"))
	return true
}

// The `.slmcode/.gitignore` body is NOT defined here. pkg/config owns the
// authoritative list (config.SlmIgnoreEntries) because every path on it is
// created by a package under pkg/ — a copy maintained next to `init` drifted
// the moment a package started writing somewhere new, and that is exactly what
// happened: this file used to carry six patterns while the workspace had
// grown to twenty-six. `slmcode commit` runs `git add -A`, so the gap was
// memory/, evolve/, metrics/, summaries/ and provider metadata landing in the
// user's history.

// ensureSlmGitignore writes .slmcode/.gitignore when it is missing.
func ensureSlmGitignore(slmDir string) error {
	if slmDir == "" {
		return nil
	}
	path := filepath.Join(slmDir, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(slmDir, 0o750); err != nil { // project state dir, owner-only
		return err
	}
	return os.WriteFile(path, []byte(config.RenderSlmGitignore()), 0o644) //nolint:gosec // conventional .gitignore perms, not secret state
}

// gitIgnores reports whether git would ignore the given repo-relative path.
func gitIgnores(root, rel string) bool {
	if !isGitRepo(root) {
		return true // not a repo: nothing can be staged
	}
	c := exec.Command("git", "-C", root, "check-ignore", "-q", rel)
	return c.Run() == nil
}

// confirm asks a yes/no question on stdin. Returns def when input is empty or
// unavailable.
func confirm(question string, def bool) bool {
	suffix := " [y/N] "
	if def {
		suffix = " [Y/n] "
	}
	if !cli.IsInteractive() {
		return def
	}
	fmt.Print(cli.Bold(question) + cli.Dim(suffix))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Println()
		return def
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

// emitJSON writes v as indented JSON to stdout. Every --json path goes through
// here so machine-readable output is byte-consistent and never colored.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// jsonMode disables color for a --json invocation, since escapes would corrupt
// the payload for anything downstream.
func jsonMode(on bool) {
	if on {
		cli.SetColorMode(cli.ColorNever)
	}
}
