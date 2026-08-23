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
)

// slmGitignore is written into .slmcode/ on init so secrets and scratch state
// never reach a commit. `slmcode commit` runs `git add -A`, and auth.json holds
// provider API keys — correctly 0600, but that does not stop `git add`.
const slmGitignore = `# Written by slmcode init — keeps secrets and scratch state out of git.
auth.json
pending/
sessions/
queries/
archives/
errors/
checkpoints/
*.log
`

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
	return os.WriteFile(path, []byte(slmGitignore), 0o644) //nolint:gosec // conventional .gitignore perms, not secret state
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
