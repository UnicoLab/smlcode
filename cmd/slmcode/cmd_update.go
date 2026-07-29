package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/piotrlaczkowski/slmcode/pkg/cli"
	"github.com/piotrlaczkowski/slmcode/pkg/installmeta"
)

func updateCmd() *cobra.Command {
	var (
		checkOnly bool
		userMode  bool
		system    bool
		srcFlag   string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Rebuild from source and reinstall system-wide (or --user)",
		Long: `Rebuild SLMCode from your local checkout and reinstall onto PATH.

Looks for the source tree in this order:
  1. --src / SLMCODE_SRC
  2. ~/.config/slmcode/install.json (written by install.sh)
  3. Source path baked into this binary at build time
  4. Common local paths (~/Desktop/PROJECT/slmcode, …)

Examples:
  slmcode update              # same mode as last install (default: system)
  slmcode update --system     # force Homebrew /usr/local install
  slmcode update --user       # ~/.local/bin only
  slmcode update --check      # show current vs source without installing
  slmcode update --src ~/Desktop/PROJECT/slmcode
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, how, err := resolveUpdateSource(srcFlag)
			if err != nil {
				return err
			}
			meta, _ := installmeta.Load()

			srcVer := readSourceVersion(src)
			srcCommit := readSourceCommit(src)

			cli.Header("Update")
			cli.KeyVal("installed", Version+" ("+GitCommit+")")
			cli.KeyVal("binary", resolveBinaryPath())
			cli.KeyVal("source", src)
			cli.KeyVal("source_via", how)
			cli.KeyVal("source_ver", srcVer+" ("+srcCommit+")")
			if meta != nil && meta.InstalledAt != "" {
				cli.KeyVal("last_install", meta.InstalledAt)
			}

			if checkOnly {
				if srcVer != Version || (srcCommit != "unknown" && srcCommit != GitCommit) {
					fmt.Println(cli.Warn("source differs from installed binary — run: slmcode update"))
				} else {
					fmt.Println(cli.Success("installed binary matches source checkout"))
				}
				return nil
			}

			mode := "system"
			if meta != nil && meta.Mode != "" {
				mode = meta.Mode
			}
			if userMode {
				mode = "user"
			}
			if system {
				mode = "system"
			}

			script := filepath.Join(src, "scripts", "install.sh")
			if _, err := os.Stat(script); err != nil {
				return fmt.Errorf("install script missing: %s (is --src a slmcode checkout?)", script)
			}

			fmt.Println(cli.Info("rebuilding + installing (" + mode + ")…"))
			argsInstall := []string{script, "--" + mode}
			c := exec.Command("bash", argsInstall...)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			c.Stdin = os.Stdin
			c.Dir = src
			if gg := os.Getenv("GOLANGGRAPH"); gg != "" {
				c.Env = append(os.Environ(), "GOLANGGRAPH="+gg)
			}
			if err := c.Run(); err != nil {
				return fmt.Errorf("install failed: %w", err)
			}
			fmt.Println(cli.Success("update complete — open a new shell only if the binary path changed"))
			fmt.Println(cli.Dim("verify: slmcode version && slmcode doctor"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "compare installed vs source without installing")
	cmd.Flags().BoolVar(&userMode, "user", false, "install to ~/.local/bin")
	cmd.Flags().BoolVar(&system, "system", false, "install system-wide (Homebrew /usr/local)")
	cmd.Flags().StringVar(&srcFlag, "src", "", "path to slmcode source checkout")
	return cmd
}

func resolveBinaryPath() string {
	p, err := os.Executable()
	if err != nil {
		return "(unknown)"
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

func resolveUpdateSource(flag string) (src, via string, err error) {
	if flag == "" {
		flag = strings.TrimSpace(os.Getenv("SLMCODE_SRC"))
	}
	if flag != "" {
		abs, e := filepath.Abs(flag)
		if e != nil {
			return "", "", e
		}
		if !looksLikeCheckout(abs) {
			return "", "", fmt.Errorf("not a slmcode checkout: %s", abs)
		}
		return abs, "flag/env", nil
	}
	if m, e := installmeta.Load(); e == nil && m.Source != "" && looksLikeCheckout(m.Source) {
		return m.Source, "install.json", nil
	}
	if SourceRoot != "" && looksLikeCheckout(SourceRoot) {
		return SourceRoot, "binary", nil
	}
	candidates := []string{
		filepath.Join(os.Getenv("HOME"), "Desktop/PROJECT/slmcode"),
		filepath.Join(os.Getenv("HOME"), "Projects/slmcode"),
		filepath.Join(os.Getenv("HOME"), "src/slmcode"),
		filepath.Join(os.Getenv("HOME"), "code/slmcode"),
	}
	for _, c := range candidates {
		if looksLikeCheckout(c) {
			return c, "default-path", nil
		}
	}
	return "", "", fmt.Errorf("cannot find slmcode source — set SLMCODE_SRC or run: slmcode update --src /path/to/slmcode")
}

func looksLikeCheckout(root string) bool {
	_, err1 := os.Stat(filepath.Join(root, "go.mod"))
	_, err2 := os.Stat(filepath.Join(root, "cmd", "slmcode"))
	_, err3 := os.Stat(filepath.Join(root, "scripts", "install.sh"))
	return err1 == nil && err2 == nil && err3 == nil
}

func readSourceVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "cmd", "slmcode", "version.go"))
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Version") && strings.Contains(line, "\"") {
			i := strings.Index(line, "\"")
			j := strings.LastIndex(line, "\"")
			if i >= 0 && j > i {
				return line[i+1 : j]
			}
		}
	}
	return "unknown"
}

func readSourceCommit(root string) string {
	c := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD")
	out, err := c.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
