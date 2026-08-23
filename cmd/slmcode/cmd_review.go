package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
)

// Review UX for `permission: review` mode.
//
// Previously `slmcode apply` wrote every pending file sight-unseen: no listing,
// no diff, no per-file choice, no reject, and a hard-coded 0o644 that stripped
// +x off executable scripts. This makes review the default and keeps `--all`
// for the old behavior.

// pendingPatch is one proposed file write recorded by permissions.RecordPending.
type pendingPatch struct {
	File    string `json:"-"` // patch file name under .slmcode/pending
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

// abs resolves the target file inside the project root.
func (p pendingPatch) abs(root string) string { return filepath.Join(root, p.Path) }

// before reads the on-disk content the patch would replace.
func (p pendingPatch) before(root string) string {
	data, err := os.ReadFile(p.abs(root))
	if err != nil {
		return ""
	}
	return string(data)
}

// diff computes the unified diff for this patch.
func (p pendingPatch) diff(root string) cli.FileDiff {
	fd := cli.Diff(p.Path, p.before(root), p.Content, 3)
	if mode, ok := fileMode(p.abs(root)); ok && mode&0o111 != 0 {
		fd.ModeNote = fmt.Sprintf("mode %04o", mode)
	}
	return fd
}

func fileMode(path string) (os.FileMode, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return st.Mode().Perm(), true
}

func pendingDir(slmDir string) string { return filepath.Join(slmDir, "pending") }

// loadPending reads every pending patch, oldest first (the file names carry a
// nanosecond prefix so lexical order is chronological).
func loadPending(slmDir string) ([]pendingPatch, error) {
	dir := pendingDir(slmDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []pendingPatch
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".patch.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p pendingPatch
		if json.Unmarshal(data, &p) != nil || strings.TrimSpace(p.Path) == "" {
			continue
		}
		p.File = e.Name()
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
}

// writePatch applies one patch, preserving the existing file mode. A brand new
// file gets 0o644; an existing executable keeps its +x bits.
func writePatch(root string, p pendingPatch) error {
	abs := p.abs(root)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if existing, ok := fileMode(abs); ok {
		mode = existing
	}
	if err := os.WriteFile(abs, []byte(p.Content), mode); err != nil {
		return err
	}
	// WriteFile only applies the mode when creating; enforce it explicitly so
	// an overwrite of an executable script stays executable.
	return os.Chmod(abs, mode)
}

func dropPatch(slmDir string, p pendingPatch) error {
	if p.File == "" {
		return nil
	}
	return os.Remove(filepath.Join(pendingDir(slmDir), p.File))
}

func applyCmd() *cobra.Command {
	var (
		all      bool
		list     bool
		asJSON   bool
		noPager  bool
		contextN int
	)
	cmd := &cobra.Command{
		Use:   "apply [path…]",
		Short: "Review and apply pending agent file writes (.slmcode/pending/)",
		Long: `Review the changes agents proposed in permission=review mode.

Interactive by default: each file is shown as a colored unified diff and you
choose what happens to it. Non-interactive callers get a deterministic contract
via --all (apply everything), --list, or --json.`,
		Example: `  slmcode apply              # review each change
  slmcode apply --list       # summary of what is waiting
  slmcode apply --json       # machine-readable pending set
  slmcode apply --all        # apply everything without prompting
  slmcode apply pkg/x.go     # only files matching a path prefix
  slmcode reject pkg/x.go    # discard one proposal`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			root := ws.Config.Root
			patches, err := loadPending(ws.Config.SlmDir())
			if err != nil {
				return err
			}
			patches = filterPatches(patches, args)

			if asJSON {
				return emitPendingJSON(root, patches)
			}
			if len(patches) == 0 {
				fmt.Println(cli.Dim("nothing pending"))
				fmt.Println(cli.Dim("tip: agents record proposals here when permission=review"))
				return nil
			}
			if list {
				printPendingList(root, patches)
				return nil
			}
			if all || !cli.IsInteractive() {
				if !all {
					fmt.Println(cli.Warn("not a terminal — pass --all to apply, --list to inspect, or --json"))
					printPendingList(root, patches)
					return failf(2, "interactive review needs a TTY (use --all / --list / --json)")
				}
				return applyAll(ws.Config.SlmDir(), root, patches)
			}
			return reviewInteractive(ws.Config.SlmDir(), root, patches, contextN, noPager)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "apply every pending change without prompting")
	cmd.Flags().BoolVar(&list, "list", false, "list pending changes with ± stats and exit")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable pending set (implies no prompts)")
	cmd.Flags().BoolVar(&noPager, "no-pager", false, "never page long diffs")
	cmd.Flags().IntVar(&contextN, "context", 3, "diff context lines")
	return cmd
}

func filterPatches(patches []pendingPatch, prefixes []string) []pendingPatch {
	if len(prefixes) == 0 {
		return patches
	}
	var out []pendingPatch
	for _, p := range patches {
		for _, pre := range prefixes {
			pre = filepath.ToSlash(strings.TrimPrefix(pre, "./"))
			if strings.HasPrefix(filepath.ToSlash(p.Path), pre) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

func printPendingList(root string, patches []pendingPatch) {
	cli.Header(fmt.Sprintf("Pending changes (%d)", len(patches)))
	added, removed := 0, 0
	for _, p := range patches {
		fd := p.diff(root)
		added += fd.Added
		removed += fd.Removed
		fmt.Println(cli.DiffStatLine(fd))
	}
	fmt.Println()
	fmt.Printf("  %s  %s %s\n", cli.Dim("total"),
		cli.Green(fmt.Sprintf("+%d", added)), cli.Red(fmt.Sprintf("-%d", removed)))
	fmt.Println(cli.Dim("  slmcode apply            review each change"))
	fmt.Println(cli.Dim("  slmcode apply --all      apply everything"))
	fmt.Println(cli.Dim("  slmcode reject <path>    discard one proposal"))
}

func emitPendingJSON(root string, patches []pendingPatch) error {
	type item struct {
		Path    string `json:"path"`
		Kind    string `json:"kind"`
		Added   int    `json:"added"`
		Removed int    `json:"removed"`
		IsNew   bool   `json:"is_new"`
		Binary  bool   `json:"binary"`
		Patch   string `json:"patch"`
		Diff    string `json:"diff"`
	}
	out := struct {
		Count   int    `json:"count"`
		Added   int    `json:"added"`
		Removed int    `json:"removed"`
		Items   []item `json:"items"`
	}{Items: []item{}}
	for _, p := range patches {
		fd := p.diff(root)
		out.Items = append(out.Items, item{
			Path: p.Path, Kind: p.Kind, Added: fd.Added, Removed: fd.Removed,
			IsNew: fd.IsNew, Binary: fd.Binary, Patch: p.File, Diff: fd.UnifiedText(),
		})
		out.Added += fd.Added
		out.Removed += fd.Removed
	}
	out.Count = len(out.Items)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func applyAll(slmDir, root string, patches []pendingPatch) error {
	n := 0
	for _, p := range patches {
		if err := writePatch(root, p); err != nil {
			fmt.Println(cli.Warn(p.Path + ": " + err.Error()))
			continue
		}
		_ = dropPatch(slmDir, p)
		fmt.Println(cli.Success("applied " + p.Path))
		n++
	}
	fmt.Println(cli.Info(fmt.Sprintf("%d file(s) applied", n)))
	if n < len(patches) {
		return failf(1, "%d of %d changes failed to apply", len(patches)-n, len(patches))
	}
	return nil
}

// reviewInteractive is the per-file review loop.
func reviewInteractive(slmDir, root string, patches []pendingPatch, contextN int, noPager bool) error {
	width, height := cli.TermSize()
	in := bufio.NewReader(os.Stdin)
	applied, skipped, rejected := 0, 0, 0
	applyRest := false

	cli.Header(fmt.Sprintf("Review %d pending change(s)", len(patches)))

	for i, p := range patches {
		fd := p.diff(root)
		fmt.Println()
		fmt.Printf("%s %s\n", cli.Dim(fmt.Sprintf("[%d/%d]", i+1, len(patches))), cli.RenderDiffHeader(fd))

		if applyRest {
			if err := writePatch(root, p); err != nil {
				fmt.Println(cli.Warn(p.Path + ": " + err.Error()))
				continue
			}
			_ = dropPatch(slmDir, p)
			applied++
			fmt.Println(cli.Success("applied " + p.Path))
			continue
		}

		opt := cli.DefaultDiffRender(width)
		opt.MaxLines = maxDiffPreview(height, noPager)
		opt.NoHeader = true // the [n/m] line above already carries it
		fmt.Print(cli.RenderDiff(fd, opt))

	prompt:
		for {
			fmt.Print(cli.Bold("  [a]pply") + cli.Dim(" / ") + cli.Bold("[s]kip") +
				cli.Dim(" / ") + cli.Bold("[e]dit") + cli.Dim(" / ") + cli.Bold("[v]iew full") +
				cli.Dim(" / ") + cli.Bold("[r]eject") + cli.Dim(" / ") + cli.Bold("[A]pply all") +
				cli.Dim(" / ") + cli.Bold("[q]uit") + " " + cli.Accent("› "))
			line, err := in.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					fmt.Println()
					return summarizeReview(applied, skipped, rejected, len(patches))
				}
				return err
			}
			choice := strings.TrimSpace(line)
			switch choice {
			case "a", "y", "apply":
				if err := writePatch(root, p); err != nil {
					fmt.Println(cli.Warn(p.Path + ": " + err.Error()))
					break prompt
				}
				_ = dropPatch(slmDir, p)
				applied++
				fmt.Println(cli.Success("applied " + p.Path))
				break prompt
			case "A", "all":
				applyRest = true
				if err := writePatch(root, p); err != nil {
					fmt.Println(cli.Warn(p.Path + ": " + err.Error()))
					break prompt
				}
				_ = dropPatch(slmDir, p)
				applied++
				fmt.Println(cli.Success("applied " + p.Path + cli.Dim(" (and everything after)")))
				break prompt
			case "s", "n", "skip", "":
				skipped++
				fmt.Println(cli.Dim("skipped — still pending"))
				break prompt
			case "r", "reject":
				_ = dropPatch(slmDir, p)
				rejected++
				fmt.Println(cli.Warn("rejected " + p.Path + " — proposal discarded"))
				break prompt
			case "v", "view":
				full := cli.DefaultDiffRender(width)
				full.MaxLines = 0
				full.NoHeader = true
				fmt.Print(cli.RenderDiff(fd, full))
			case "e", "edit":
				edited, ok, err := editProposal(p)
				if err != nil {
					fmt.Println(cli.Warn(err.Error()))
					continue
				}
				if !ok {
					fmt.Println(cli.Dim("unchanged"))
					continue
				}
				p.Content = edited
				fd = p.diff(root)
				fmt.Println(cli.Info("proposal edited — re-diffed"))
				reOpt := cli.DefaultDiffRender(width)
				reOpt.NoHeader = true
				fmt.Print(cli.RenderDiff(fd, reOpt))
			case "q", "quit":
				fmt.Println()
				return summarizeReview(applied, skipped, rejected, len(patches))
			default:
				fmt.Println(cli.Dim("  pick one of a / s / e / v / r / A / q"))
			}
		}
	}
	fmt.Println()
	return summarizeReview(applied, skipped, rejected, len(patches))
}

func maxDiffPreview(height int, noPager bool) int {
	if noPager {
		return 0
	}
	n := height - 12
	if n < 20 {
		n = 20
	}
	return n
}

func summarizeReview(applied, skipped, rejected, total int) error {
	fmt.Printf("%s  %s  %s  %s\n",
		cli.Bold("review done"),
		cli.Green(fmt.Sprintf("applied=%d", applied)),
		cli.Dim(fmt.Sprintf("skipped=%d", skipped)),
		cli.Yellow(fmt.Sprintf("rejected=%d", rejected)))
	if remaining := total - applied - rejected; remaining > 0 {
		fmt.Println(cli.Dim(fmt.Sprintf("  %d still pending — slmcode apply", remaining)))
	}
	return nil
}

// editProposal opens the proposed content in $EDITOR and returns the result.
func editProposal(p pendingPatch) (string, bool, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		return "", false, fmt.Errorf("no $EDITOR set — export EDITOR=vim (or use [v]iew / [s]kip)")
	}
	tmp, err := os.CreateTemp("", "slmcode-*"+filepath.Ext(p.Path))
	if err != nil {
		return "", false, err
	}
	name := tmp.Name()
	defer func() {
		if rmErr := os.Remove(name); rmErr != nil && !os.IsNotExist(rmErr) {
			fmt.Fprintf(os.Stderr, "warning: failed to remove temp file %s: %v\n", name, rmErr)
		}
	}()
	if _, err := tmp.WriteString(p.Content); err != nil {
		_ = tmp.Close() // best effort; the WriteString error above is what matters
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}

	// editor comes from $EDITOR/$VISUAL, which the invoking user controls on
	// their own machine — same trust level as any other locally-launched tool.
	c := exec.Command(editor, name) //nolint:gosec // editor path is from the user's own env, not attacker input
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return "", false, fmt.Errorf("editor failed: %w", err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", false, err
	}
	if string(data) == p.Content {
		return "", false, nil
	}
	return string(data), true, nil
}

func rejectCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "reject [path…]",
		Short:   "Discard pending agent proposals without applying them",
		Example: "  slmcode reject pkg/x.go\n  slmcode reject --all",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !all {
				return failf(2, "name a path to reject, or pass --all")
			}
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			patches, err := loadPending(ws.Config.SlmDir())
			if err != nil {
				return err
			}
			if !all {
				patches = filterPatches(patches, args)
			}
			if len(patches) == 0 {
				fmt.Println(cli.Dim("nothing matched — slmcode apply --list"))
				return nil
			}
			for _, p := range patches {
				if err := dropPatch(ws.Config.SlmDir(), p); err != nil {
					fmt.Println(cli.Warn(p.Path + ": " + err.Error()))
					continue
				}
				fmt.Println(cli.Warn("rejected " + p.Path))
			}
			fmt.Println(cli.Info(fmt.Sprintf("%d proposal(s) discarded", len(patches))))
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "reject every pending proposal")
	return cmd
}

// ── slmcode diff ─────────────────────────────────────────────────────────────

func diffCmd() *cobra.Command {
	var (
		stat     bool
		contextN int
		noColor  bool
	)
	cmd := &cobra.Command{
		Use:   "diff [path…]",
		Short: "Show working-tree changes, including files agents just created",
		Long: `Render what changed in the workspace.

Unlike bare "git diff" this includes UNTRACKED files (rendered as all-additions)
— which is exactly what an agent produces when it creates a new file — and it
still works outside a git repository by comparing against .slmcode checkpoints.`,
		Example: "  slmcode diff\n  slmcode diff --stat\n  slmcode diff pkg/cli",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot()
			if err != nil {
				return err
			}
			if noColor {
				cli.SetColorMode(cli.ColorNever)
			}
			return showDiffPaths(root, args, stat, contextN)
		},
	}
	cmd.Flags().BoolVar(&stat, "stat", false, "summary only (path + ±N)")
	cmd.Flags().IntVar(&contextN, "context", 3, "context lines")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "disable ANSI color (same as --color=never)")
	return cmd
}

// showDiff renders the diff for one optional path (used by /diff in the REPL).
func showDiff(root, path string) error {
	var paths []string
	if path != "" {
		paths = []string{path}
	}
	return showDiffPaths(root, paths, false, 3)
}

func showDiffPaths(root string, paths []string, stat bool, contextN int) error {
	diffs, err := collectWorkspaceDiffs(root, paths, contextN)
	if err != nil {
		return err
	}
	if len(diffs) == 0 {
		fmt.Println(cli.Dim("no changes"))
		return nil
	}
	width, _ := cli.TermSize()
	added, removed := 0, 0
	for _, fd := range diffs {
		added += fd.Added
		removed += fd.Removed
	}
	if stat {
		for _, fd := range diffs {
			fmt.Println(cli.DiffStatLine(fd))
		}
	} else {
		for _, fd := range diffs {
			fmt.Println()
			fmt.Print(cli.RenderDiff(fd, cli.DefaultDiffRender(width)))
		}
		fmt.Println()
	}
	fmt.Printf("  %s %d file(s)  %s %s\n", cli.Dim("changed"), len(diffs),
		cli.Green(fmt.Sprintf("+%d", added)), cli.Red(fmt.Sprintf("-%d", removed)))
	return nil
}

// collectWorkspaceDiffs unions tracked modifications with untracked files.
func collectWorkspaceDiffs(root string, paths []string, contextN int) ([]cli.FileDiff, error) {
	if !isGitRepo(root) {
		return checkpointDiffs(root, paths, contextN)
	}
	var out []cli.FileDiff
	seen := map[string]bool{}

	for _, rel := range gitChangedFiles(root, paths) {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		before := gitFileAtHead(root, rel)
		after := readFileString(filepath.Join(root, rel))
		fd := cli.Diff(rel, before, after, contextN)
		if !fd.Empty() {
			out = append(out, fd)
		}
	}
	// Untracked files: git diff omits them entirely, so render them as
	// all-additions rather than pretending nothing happened.
	for _, rel := range gitUntrackedFiles(root, paths) {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		after := readFileString(filepath.Join(root, rel))
		fd := cli.Diff(rel, "", after, contextN)
		fd.IsNew = true
		if !fd.Empty() {
			out = append(out, fd)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func gitChangedFiles(root string, paths []string) []string {
	args := []string{"-C", root, "diff", "--name-only", "HEAD"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	// args is built from root/paths, argv elements passed straight to git
	// (no shell), not attacker-controlled — paths are the user's own CLI args.
	out, err := exec.Command("git", args...).Output() //nolint:gosec // argv-only git invocation, no shell, args are local CLI input
	if err != nil {
		// No HEAD yet (fresh repo) — fall back to the index-free listing.
		args = []string{"-C", root, "diff", "--name-only"}
		if len(paths) > 0 {
			args = append(args, "--")
			args = append(args, paths...)
		}
		out, err = exec.Command("git", args...).Output() //nolint:gosec // argv-only git invocation, no shell, args are local CLI input
		if err != nil {
			return nil
		}
	}
	return splitLinesNonEmpty(string(out))
}

func gitUntrackedFiles(root string, paths []string) []string {
	args := []string{"-C", root, "ls-files", "--others", "--exclude-standard"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := exec.Command("git", args...).Output() //nolint:gosec // argv-only git invocation, no shell, args are local CLI input
	if err != nil {
		return nil
	}
	return splitLinesNonEmpty(string(out))
}

func gitFileAtHead(root, rel string) string {
	out, err := exec.Command("git", "-C", root, "show", "HEAD:"+rel).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func splitLinesNonEmpty(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// checkpointDiffs compares the workspace against the newest .slmcode file
// checkpoints when there is no git repository at all.
func checkpointDiffs(root string, paths []string, contextN int) ([]cli.FileDiff, error) {
	base := filepath.Join(root, ".slmcode", "checkpoints")
	entries, err := os.ReadDir(base)
	if err != nil {
		fmt.Println(cli.Dim("not a git repository and no .slmcode/checkpoints — nothing to compare against"))
		fmt.Println(cli.Dim("tip: git init   ·   or   slmcode apply --list   for review-mode proposals"))
		return nil, nil
	}
	// Newest checkpoint directory wins.
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	sort.Strings(dirs)
	snap := filepath.Join(base, dirs[len(dirs)-1])

	var out []cli.FileDiff
	err = filepath.Walk(snap, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(snap, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if len(paths) > 0 && !matchesAnyPrefix(rel, paths) {
			return nil
		}
		fd := cli.Diff(rel, readFileString(p), readFileString(filepath.Join(root, rel)), contextN)
		if !fd.Empty() {
			out = append(out, fd)
		}
		return nil
	})
	return out, err
}

func matchesAnyPrefix(rel string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(rel, filepath.ToSlash(strings.TrimPrefix(p, "./"))) {
			return true
		}
	}
	return false
}
