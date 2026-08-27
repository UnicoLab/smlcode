package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo builds a real git repository with one commit and returns its path.
//
// A real repository, not a fake: every behavior worth testing here is a
// behavior of `git worktree`, and a stub would only test that this package
// calls the functions it calls.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// The repository's PARENT is where worktrees are created, so give it one
	// that t.TempDir will clean up along with everything else.
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := git(ctx, root, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	write(t, root, "main.go", "package main\n")
	if _, err := git(ctx, root, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := git(ctx, root, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return root
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

// ── Availability ──────────────────────────────────────────────────────────

func TestAvailableRefusesNonRepositories(t *testing.T) {
	// A run that cannot be isolated must say so BEFORE it starts, not after it
	// has spent its budget.
	if err := Available(context.Background(), t.TempDir()); err == nil {
		t.Fatal("Available accepted a directory that is not a repository")
	}
	if err := Available(context.Background(), ""); err == nil {
		t.Fatal("Available accepted an empty root")
	}
}

func TestAvailableRefusesARepositoryWithNoCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := git(context.Background(), root, "init", "--initial-branch=main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	err := Available(context.Background(), root)
	if err == nil {
		t.Fatal("Available accepted a repository with no HEAD to branch from")
	}
	if !strings.Contains(err.Error(), "no commits") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestAvailableAcceptsARealRepository(t *testing.T) {
	if err := Available(context.Background(), newRepo(t)); err != nil {
		t.Fatalf("Available: %v", err)
	}
}

// ── Open ──────────────────────────────────────────────────────────────────

func TestOpenCreatesAnIsolatedCheckout(t *testing.T) {
	ctx := context.Background()
	root := newRepo(t)
	sb, err := Open(ctx, root, "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })

	if sb.Root() == root {
		t.Fatal("the sandbox root is the operator's checkout")
	}
	if sb.Branch() != "slmcode/test" {
		t.Errorf("Branch = %q", sb.Branch())
	}
	if sb.BaseBranch() != "main" {
		t.Errorf("BaseBranch = %q, want main", sb.BaseBranch())
	}
	if got := read(t, sb.Root(), "main.go"); got != "package main\n" {
		t.Errorf("worktree does not carry the repository contents: %q", got)
	}
}

func TestOpenPlacesTheWorktreeOutsideTheRepository(t *testing.T) {
	// A checkout nested under its own root would be walked by the repo map,
	// matched by glob tools and picked up by `git status` — the harness would
	// read its own isolated copy as project source.
	ctx := context.Background()
	root := newRepo(t)
	sb, err := Open(ctx, root, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })

	rel, err := filepath.Rel(root, sb.Root())
	if err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("worktree %s is inside the repository %s", sb.Root(), root)
	}
}

func TestOpenGeneratesABranchNameWhenNoneIsGiven(t *testing.T) {
	ctx := context.Background()
	sb, err := Open(ctx, newRepo(t), "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })
	if !branchSafe.MatchString(sb.Branch()) {
		t.Errorf("generated branch %q is not git-safe", sb.Branch())
	}
	if !strings.HasPrefix(sb.Branch(), "slmcode/") {
		t.Errorf("generated branch %q is not namespaced", sb.Branch())
	}
}

func TestOpenRefusesAnUnsafeBranchName(t *testing.T) {
	ctx := context.Background()
	root := newRepo(t)
	for _, b := range []string{"--force", "a b", "a;rm -rf /", "-x", "a\nb", strings.Repeat("x", 200)} {
		if sb, err := Open(ctx, root, b); err == nil {
			_ = sb.Discard(ctx)
			t.Errorf("Open accepted branch %q", b)
		}
	}
}

func TestOpenLeavesTheOperatorCheckoutOnItsBranch(t *testing.T) {
	// The whole promise: while the run happens elsewhere, the operator's
	// checkout is exactly where they left it.
	ctx := context.Background()
	root := newRepo(t)
	sb, err := Open(ctx, root, "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })

	cur, err := git(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("read branch: %v", err)
	}
	if strings.TrimSpace(cur) != "main" {
		t.Errorf("operator checkout moved to %q", strings.TrimSpace(cur))
	}
}

// ── Isolation ─────────────────────────────────────────────────────────────

func TestWritesInTheSandboxDoNotReachTheCheckout(t *testing.T) {
	ctx := context.Background()
	root := newRepo(t)
	sb, err := Open(ctx, root, "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })

	write(t, sb.Root(), "main.go", "package main // changed\n")
	write(t, sb.Root(), "new.go", "package main\n")

	if got := read(t, root, "main.go"); got != "package main\n" {
		t.Errorf("the operator's file changed: %q", got)
	}
	if read(t, root, "new.go") != "" {
		t.Error("a new file leaked into the operator's checkout")
	}
}

func TestChangedFilesSeesEditsAdditionsAndDeletions(t *testing.T) {
	ctx := context.Background()
	sb, err := Open(ctx, newRepo(t), "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })

	write(t, sb.Root(), "main.go", "package main // edited\n")
	write(t, sb.Root(), "pkg/new.go", "package pkg\n")

	files, err := sb.ChangedFiles(ctx)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	got := strings.Join(files, ",")
	for _, want := range []string{"main.go", "pkg/new.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("ChangedFiles = %v, missing %q", files, want)
		}
	}
}

func TestChangedFilesIsEmptyForAnUntouchedSandbox(t *testing.T) {
	ctx := context.Background()
	sb, err := Open(ctx, newRepo(t), "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })

	files, err := sb.ChangedFiles(ctx)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("ChangedFiles = %v for an untouched sandbox", files)
	}
}

// ── Adopt ─────────────────────────────────────────────────────────────────

func TestAdoptBringsTheWorkBack(t *testing.T) {
	ctx := context.Background()
	root := newRepo(t)
	sb, err := Open(ctx, root, "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	write(t, sb.Root(), "main.go", "package main // done\n")
	write(t, sb.Root(), "added.go", "package main\n")

	committed, err := sb.Commit(ctx, "slmcode: do the thing")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !committed {
		t.Fatal("Commit reported nothing to commit")
	}
	if err := sb.Adopt(ctx); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got := read(t, root, "main.go"); got != "package main // done\n" {
		t.Errorf("edit did not reach the checkout: %q", got)
	}
	if read(t, root, "added.go") == "" {
		t.Error("added file did not reach the checkout")
	}
	_ = sb.Discard(ctx)
}

func TestCommitReportsAnEmptySandbox(t *testing.T) {
	ctx := context.Background()
	sb, err := Open(ctx, newRepo(t), "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })

	committed, err := sb.Commit(ctx, "nothing happened")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if committed {
		t.Error("Commit claimed to commit an unchanged tree")
	}
}

func TestAdoptRefusesWhenTheCheckoutMovedOn(t *testing.T) {
	// Merging into whatever happens to be checked out now is not what the
	// operator asked for — they asked to isolate work on THIS branch.
	ctx := context.Background()
	root := newRepo(t)
	sb, err := Open(ctx, root, "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sb.Discard(ctx) })

	write(t, sb.Root(), "main.go", "package main // done\n")
	if _, err := sb.Commit(ctx, "work"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := git(ctx, root, "checkout", "-b", "other"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	err = sb.Adopt(ctx)
	if err == nil {
		t.Fatal("Adopt merged into a branch the operator switched to")
	}
	if !strings.Contains(err.Error(), "moved") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// ── Teardown ──────────────────────────────────────────────────────────────

func TestDiscardRemovesEverything(t *testing.T) {
	// The capability rewind cannot offer: abandoning is ONE operation that
	// cannot half-succeed, and it covers files nothing predicted.
	ctx := context.Background()
	root := newRepo(t)
	sb, err := Open(ctx, root, "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	dir := sb.Root()
	write(t, dir, "junk.txt", "a build artifact nothing tracked")
	write(t, dir, "main.go", "package main // abandoned\n")

	if err := sb.Discard(ctx); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("worktree survived Discard: %v", err)
	}
	branches, err := git(ctx, root, "branch", "--list")
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if strings.Contains(branches, "slmcode/test") {
		t.Errorf("branch survived Discard:\n%s", branches)
	}
	if got := read(t, root, "main.go"); got != "package main\n" {
		t.Errorf("the operator's checkout was touched: %q", got)
	}
}

func TestDiscardIsIdempotent(t *testing.T) {
	ctx := context.Background()
	sb, err := Open(ctx, newRepo(t), "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := sb.Discard(ctx); err != nil {
		t.Fatalf("first Discard: %v", err)
	}
	if err := sb.Discard(ctx); err != nil {
		t.Errorf("second Discard: %v", err)
	}
}

func TestKeepLeavesTheBranchForInspection(t *testing.T) {
	ctx := context.Background()
	root := newRepo(t)
	sb, err := Open(ctx, root, "slmcode/test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	write(t, sb.Root(), "main.go", "package main // kept\n")
	if _, err := sb.Commit(ctx, "work"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	dir := sb.Root()
	if err := sb.Keep(ctx); err != nil {
		t.Fatalf("Keep: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("worktree survived Keep")
	}
	branches, err := git(ctx, root, "branch", "--list")
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if !strings.Contains(branches, "slmcode/test") {
		t.Errorf("Keep deleted the branch it was meant to preserve:\n%s", branches)
	}
	_ = sb.Discard(ctx)
}

func TestNilSandboxMethodsAreSafe(t *testing.T) {
	var sb *Sandbox
	ctx := context.Background()
	if sb.Root() != "" || sb.Branch() != "" || sb.Origin() != "" || sb.BaseBranch() != "" {
		t.Error("a nil sandbox reported a path")
	}
	if err := sb.Discard(ctx); err != nil {
		t.Errorf("Discard on nil: %v", err)
	}
	if err := sb.Keep(ctx); err != nil {
		t.Errorf("Keep on nil: %v", err)
	}
	if files, err := sb.ChangedFiles(ctx); err != nil || files != nil {
		t.Errorf("ChangedFiles on nil = %v, %v", files, err)
	}
	if _, err := sb.Commit(ctx, "x"); err == nil {
		t.Error("Commit on nil should error")
	}
	if err := sb.Adopt(ctx); err == nil {
		t.Error("Adopt on nil should error")
	}
}
