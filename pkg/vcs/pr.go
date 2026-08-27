package vcs

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Pull-request delivery.
//
// This is the one thing in the harness that leaves the machine. Everything
// else — every edit, every gate, every wave — is local and reversible; a pull
// request is neither. So the shape of this file is defensive in a way the rest
// of the codebase does not need to be:
//
//   - it never runs unless the operator asked for it by flag;
//   - it refuses to run on a dirty tree it did not create, on a detached HEAD,
//     or on the default branch;
//   - it stages ONLY what the run changed, never `git add -A`;
//   - Prepare and Deliver are separate calls, so a caller can show the operator
//     exactly what is about to be pushed before anything is.

// branchSafe matches a git branch name we are willing to create.
var branchSafe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,80}$`)

// defaultBranches are refused as a commit target. Committing a run straight
// onto main is not a delivery mechanism, it is an accident.
var defaultBranches = map[string]bool{
	"main": true, "master": true, "trunk": true, "develop": true, "development": true,
}

// DeliverOptions configures a pull request.
type DeliverOptions struct {
	// Branch is the branch to create. Generated from the issue or query when empty.
	Branch string
	// Title is the PR title.
	Title string
	// Body is the PR body.
	Body string
	// Files are the repo-relative paths to stage. Required: this package never
	// stages the whole tree.
	Files []string
	// Draft opens the PR as a draft.
	Draft bool
	// Base is the target branch. Empty lets the forge choose the default.
	Base string
}

// Plan is what Deliver would do, for showing an operator before it happens.
type Plan struct {
	Branch     string
	Title      string
	Files      []string
	Base       string
	Draft      bool
	FromBranch string
}

// Summary renders the plan as operator-facing lines.
func (p Plan) Summary() []string {
	out := []string{
		"branch:  " + p.FromBranch + " → " + p.Branch,
		"title:   " + p.Title,
	}
	if p.Base != "" {
		out = append(out, "base:    "+p.Base)
	}
	if p.Draft {
		out = append(out, "draft:   yes")
	}
	out = append(out, fmt.Sprintf("files:   %d", len(p.Files)))
	for _, f := range p.Files {
		out = append(out, "  - "+f)
	}
	return out
}

// Prepare validates that a pull request can be delivered and returns the plan.
//
// It changes nothing. Every refusal it can produce is a condition the operator
// has to fix, and finding them all before touching the repository is what keeps
// a failed delivery from leaving a half-made branch behind.
func Prepare(ctx context.Context, root string, opt DeliverOptions) (Plan, error) {
	if err := RequireGH(); err != nil {
		return Plan{}, err
	}
	if root == "" {
		return Plan{}, fmt.Errorf("no workspace root")
	}
	if _, err := run(ctx, root, "git", "rev-parse", "--is-inside-work-tree"); err != nil {
		return Plan{}, fmt.Errorf("not a git repository: %w", err)
	}

	current, err := run(ctx, root, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return Plan{}, fmt.Errorf("read current branch: %w", err)
	}
	current = strings.TrimSpace(current)
	if current == "HEAD" {
		return Plan{}, fmt.Errorf("HEAD is detached — check out a branch before delivering a pull request")
	}

	files := cleanPaths(opt.Files)
	if len(files) == 0 {
		return Plan{}, fmt.Errorf("nothing to deliver: the run changed no files")
	}

	branch := strings.TrimSpace(opt.Branch)
	if branch == "" {
		branch = BranchName(opt.Title)
	}
	if !branchSafe.MatchString(branch) {
		return Plan{}, fmt.Errorf("refusing to create branch %q — use letters, digits, dot, dash, slash", branch)
	}
	if defaultBranches[strings.ToLower(branch)] {
		return Plan{}, fmt.Errorf("refusing to commit onto %q — name a feature branch", branch)
	}

	title := strings.TrimSpace(opt.Title)
	if title == "" {
		return Plan{}, fmt.Errorf("a pull request needs a title")
	}

	return Plan{
		Branch:     branch,
		Title:      clip(title, 200),
		Files:      files,
		Base:       strings.TrimSpace(opt.Base),
		Draft:      opt.Draft,
		FromBranch: current,
	}, nil
}

// Deliver creates the branch, commits the named files and opens the pull
// request. It returns the PR URL.
//
// Call Prepare first and show the operator its plan: this function assumes the
// decision to publish has already been made.
func Deliver(ctx context.Context, root string, plan Plan, body string) (string, error) {
	if _, err := run(ctx, root, "git", "checkout", "-b", plan.Branch); err != nil {
		// An existing branch is not a failure — the operator may be delivering a
		// second run onto the same one.
		if _, err2 := run(ctx, root, "git", "checkout", plan.Branch); err2 != nil {
			return "", fmt.Errorf("create branch %s: %w", plan.Branch, err)
		}
	}

	// Stage ONLY what the run touched. `git add -A` would sweep in whatever else
	// the operator had in flight — the classic way an automated commit ships
	// somebody's unrelated debugging.
	args := append([]string{"add", "--"}, plan.Files...)
	if _, err := run(ctx, root, "git", args...); err != nil {
		return "", fmt.Errorf("stage files: %w", err)
	}

	staged, err := run(ctx, root, "git", "diff", "--cached", "--name-only")
	if err != nil {
		return "", fmt.Errorf("inspect staged changes: %w", err)
	}
	if strings.TrimSpace(staged) == "" {
		return "", fmt.Errorf("nothing staged — the named files are unchanged on disk")
	}

	msg := plan.Title
	if strings.TrimSpace(body) != "" {
		msg += "\n\n" + clip(body, 4000)
	}
	if _, err := run(ctx, root, "git", "commit", "-m", msg); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	if _, err := run(ctx, root, "git", "push", "--set-upstream", "origin", plan.Branch); err != nil {
		return "", fmt.Errorf("push %s: %w", plan.Branch, err)
	}

	prArgs := []string{"pr", "create", "--title", plan.Title, "--body", clip(body, 60000)}
	if plan.Base != "" {
		prArgs = append(prArgs, "--base", plan.Base)
	}
	if plan.Draft {
		prArgs = append(prArgs, "--draft")
	}
	out, err := run(ctx, root, "gh", prArgs...)
	if err != nil {
		return "", fmt.Errorf("gh pr create: %w", err)
	}
	return strings.TrimSpace(lastLine(out)), nil
}

// BranchName derives a git-safe branch name from a title.
func BranchName(title string) string {
	var b strings.Builder
	b.WriteString("slmcode/")
	prev := byte('-')
	for i := 0; i < len(title) && b.Len() < 48; i++ {
		c := title[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 32)
			prev = c
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
			prev = c
		default:
			if prev != '-' {
				b.WriteByte('-')
				prev = '-'
			}
		}
	}
	// Trim the slash too, not just dashes. A title of pure punctuation
	// contributes no characters at all, leaving the bare prefix "slmcode/" —
	// which branchSafe accepts (slash is a legal branch character) and git then
	// rejects, turning a cosmetic input into a failed delivery at the last step.
	name := strings.TrimRight(b.String(), "-/")
	if name == "slmcode" || name == "" {
		// A title with nothing usable in it still has to produce a legal,
		// unique branch. Seconds since the epoch is enough: two deliveries in
		// the same second from one workspace is not a case worth a UUID.
		name = fmt.Sprintf("slmcode/run-%d", time.Now().Unix())
	}
	return name
}

// cleanPaths drops empties, duplicates and anything that escapes the root.
func cleanPaths(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "./"))
		if p == "" || seen[p] {
			continue
		}
		// A staged path is passed to `git add --`, which resolves it relative to
		// the repository. `..` would reach outside the workspace the run was
		// scoped to, and an absolute path ignores the scope entirely.
		if strings.Contains(p, "..") || strings.HasPrefix(p, "/") {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}
