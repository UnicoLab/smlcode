// Package sandbox runs a harness turn against an isolated copy of the
// workspace instead of the operator's checkout.
//
// ── What isolation buys that pkg/rewind does not ─────────────────────────
//
// pkg/rewind already snapshots every file a wave touches and can put them
// back, which is most of the value at a fraction of the cost. Two things it
// cannot do:
//
//   - ABANDON. Restoring N files one by one is a best-effort walk over a list
//     the harness had to predict correctly. Deleting a worktree is one
//     operation that cannot half-succeed, and it covers files nothing
//     predicted — a stray build artifact, a file a shell command created.
//   - CONTAIN. In-place, an out-of-scope write lands in the operator's
//     checkout and is reported afterwards. In a worktree it lands somewhere
//     the operator was never using.
//
// ── The seam ─────────────────────────────────────────────────────────────
//
// A Sandbox is deliberately NOT a new abstraction threaded through the tool
// layer. Every ws_* tool already resolves paths against one root, so isolation
// is achieved by changing what that root IS for the duration of a run, and
// nothing below this package needs to know it happened.
//
// The one thing that must NOT follow the root is harness state. Memory, the
// board, the repair rules and the bandit's policy live in .slmcode and are the
// reason a second run on a repo goes better than the first; derived from a
// throwaway worktree they would be thrown away too. config.StateDir pins them
// to the origin checkout — see config.SlmDir.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// gitTimeout bounds any single git call.
const gitTimeout = 60 * time.Second

// branchSafe matches a branch name this package is willing to create.
var branchSafe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,80}$`)

// Sandbox is an isolated checkout of a repository.
type Sandbox struct {
	// origin is the operator's checkout.
	origin string
	// dir is the isolated worktree.
	dir string
	// branch is the branch the worktree is on.
	branch string
	// baseBranch is what origin was on when the sandbox opened.
	baseBranch string
	// removed guards Discard against running twice.
	removed bool
}

// Root is the directory a run should treat as the workspace.
func (s *Sandbox) Root() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Branch is the branch the isolated work is on.
func (s *Sandbox) Branch() string {
	if s == nil {
		return ""
	}
	return s.branch
}

// Origin is the operator's checkout.
func (s *Sandbox) Origin() string {
	if s == nil {
		return ""
	}
	return s.origin
}

// BaseBranch is the branch origin was on when the sandbox opened.
func (s *Sandbox) BaseBranch() string {
	if s == nil {
		return ""
	}
	return s.baseBranch
}

// Available reports whether isolation can be used for root, and why not.
//
// Checked up front and reported as a refusal rather than discovered halfway
// through: a run that cannot be isolated should say so before it starts, not
// after it has spent its budget.
func Available(ctx context.Context, root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("no workspace root")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is not on PATH — worktree isolation needs it")
	}
	if _, err := git(ctx, root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("%s is not a git repository — worktree isolation needs one", root)
	}
	// A repository with no commits has no HEAD to branch from.
	if _, err := git(ctx, root, "rev-parse", "--verify", "HEAD"); err != nil {
		return fmt.Errorf("repository has no commits yet — commit once before isolating a run")
	}
	return nil
}

// Open creates a worktree for branch, based on the repository's current HEAD.
//
// The worktree lives beside the repository rather than inside it: a checkout
// nested under its own root would be walked by the repo map, matched by glob
// tools and picked up by `git status` — the harness would be reading its own
// isolated copy as if it were project source.
func Open(ctx context.Context, root, branch string) (*Sandbox, error) {
	if err := Available(ctx, root); err != nil {
		return nil, err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = fmt.Sprintf("slmcode/run-%d", time.Now().UnixNano())
	}
	if !branchSafe.MatchString(branch) {
		return nil, fmt.Errorf("refusing to create branch %q — use letters, digits, dot, dash, slash", branch)
	}

	base, err := git(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("read current branch: %w", err)
	}
	base = strings.TrimSpace(base)

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	dir, err := os.MkdirTemp(filepath.Dir(abs), ".slmcode-worktree-")
	if err != nil {
		return nil, fmt.Errorf("create worktree directory: %w", err)
	}
	// git worktree add refuses an existing directory, and MkdirTemp just made
	// one. Removing it keeps the atomic-name guarantee (nothing else can claim
	// the path in between on any filesystem this runs on) without fighting git.
	if err := os.Remove(dir); err != nil {
		return nil, fmt.Errorf("prepare worktree directory: %w", err)
	}

	if _, err := git(ctx, root, "worktree", "add", "-b", branch, dir, "HEAD"); err != nil {
		return nil, fmt.Errorf("git worktree add: %w", err)
	}
	return &Sandbox{origin: abs, dir: dir, branch: branch, baseBranch: base}, nil
}

// ChangedFiles lists the repo-relative paths the run modified, added or deleted.
func (s *Sandbox) ChangedFiles(ctx context.Context) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	out, err := git(ctx, s.dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("read worktree status: %w", err)
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		// Porcelain v1: XY<space>path, with renames as "old -> new".
		path := strings.TrimSpace(line[3:])
		if i := strings.LastIndex(path, " -> "); i >= 0 {
			path = strings.TrimSpace(path[i+4:])
		}
		path = strings.Trim(path, `"`)
		if path != "" {
			files = append(files, path)
		}
	}
	return files, nil
}

// Commit stages everything the run changed and commits it inside the worktree.
//
// `git add -A` is safe HERE and only here, and the difference is the whole
// point of the sandbox: this tree contains nothing but what this run did, so
// there is no unrelated work in flight to sweep up. In the operator's checkout
// the same command would be reckless — see pkg/vcs, which stages an explicit
// file list for exactly that reason.
func (s *Sandbox) Commit(ctx context.Context, message string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("no sandbox")
	}
	if _, err := git(ctx, s.dir, "add", "-A"); err != nil {
		return false, fmt.Errorf("stage worktree changes: %w", err)
	}
	staged, err := git(ctx, s.dir, "diff", "--cached", "--name-only")
	if err != nil {
		return false, fmt.Errorf("inspect staged changes: %w", err)
	}
	if strings.TrimSpace(staged) == "" {
		return false, nil
	}
	if strings.TrimSpace(message) == "" {
		message = "slmcode run"
	}
	if _, err := git(ctx, s.dir, "commit", "-m", message); err != nil {
		return false, fmt.Errorf("commit worktree: %w", err)
	}
	return true, nil
}

// Adopt merges the sandbox branch back into the branch origin is on.
//
// It refuses when origin has moved to a different branch than it was on at
// Open, because merging into whatever happens to be checked out now is not
// what the operator asked for — they asked to isolate work on THIS branch.
func (s *Sandbox) Adopt(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("no sandbox")
	}
	current, err := git(ctx, s.origin, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("read origin branch: %w", err)
	}
	if got := strings.TrimSpace(current); got != s.baseBranch {
		return fmt.Errorf("checkout moved from %q to %q while the run was isolated — "+
			"merge %s by hand", s.baseBranch, got, s.branch)
	}
	if _, err := git(ctx, s.origin, "merge", "--no-edit", s.branch); err != nil {
		return fmt.Errorf("merge %s into %s: %w", s.branch, s.baseBranch, err)
	}
	return nil
}

// Discard removes the worktree and its branch. Safe to call twice.
//
// The branch is deleted with -D rather than -d: an abandoned run's branch has
// by definition not been merged, and -d would refuse to remove exactly the
// branches this is for.
func (s *Sandbox) Discard(ctx context.Context) error {
	if s == nil || s.removed {
		return nil
	}
	s.removed = true
	var firstErr error
	if _, err := git(ctx, s.origin, "worktree", "remove", "--force", s.dir); err != nil {
		firstErr = fmt.Errorf("remove worktree: %w", err)
		// git failed; take the directory out of the way ourselves so a failed
		// cleanup does not leave a checkout sitting next to the repository.
		_ = os.RemoveAll(s.dir)
	}
	if _, err := git(ctx, s.origin, "branch", "-D", s.branch); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("delete branch %s: %w", s.branch, err)
	}
	return firstErr
}

// Keep removes the worktree but leaves the branch in place, so the operator
// can inspect, cherry-pick or open a pull request from it later.
func (s *Sandbox) Keep(ctx context.Context) error {
	if s == nil || s.removed {
		return nil
	}
	s.removed = true
	if _, err := git(ctx, s.origin, "worktree", "remove", "--force", s.dir); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}

// git runs an argv-only git command in dir and returns stdout.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) // #nosec G204 -- argv-only, validated arguments
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", err, firstLine(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError) //nolint:errorlint // direct type check is the intent
	if ok {
		*target = ee
	}
	return ok
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
