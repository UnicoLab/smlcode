package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/sandbox"
	"github.com/spf13/cobra"
)

// Worktree isolation for `slmcode run`.
//
// The seam is one line: point Config.Root at a throwaway git worktree and pin
// Config.StateDir to the origin's .slmcode. Every ws_* tool already resolves
// paths against Config.Root, so the entire tool layer becomes isolated without
// knowing it, while memory, the board and the bandit's policy keep accumulating
// where they belong.
//
// In-place stays the default, permanently. Plenty of workspaces are not git
// repositories at all, and a harness that only works inside one would be a
// worse harness.

// isolation is an opened sandbox plus what the caller must do at the end.
type isolation struct {
	sb       *sandbox.Sandbox
	origRoot string
	keep     bool
}

// beginIsolation opens a worktree when --isolate asks for one.
//
// Returns (nil, nil) when isolation was not requested — the caller runs
// exactly as it always did.
func beginIsolation(ctx context.Context, cmd *cobra.Command, h *harness.Harness) (*isolation, error) {
	mode := strings.TrimSpace(flagString(cmd, "isolate"))
	switch mode {
	case "", "none", "in-place":
		return nil, nil
	case "worktree":
	default:
		return nil, fmt.Errorf("unknown --isolate mode %q — use 'worktree' or 'none'", mode)
	}
	if h == nil || h.Config == nil {
		return nil, fmt.Errorf("no harness to isolate")
	}
	cfg := h.Config

	if err := sandbox.Available(ctx, cfg.Root); err != nil {
		return nil, fmt.Errorf("cannot isolate this run: %w", err)
	}
	sb, err := sandbox.Open(ctx, cfg.Root, flagString(cmd, "branch"))
	if err != nil {
		return nil, err
	}

	iso := &isolation{sb: sb, origRoot: cfg.Root, keep: flagBool(cmd, "keep-worktree")}
	// State must NOT follow the root. Derived from a throwaway worktree, the
	// four memory layers, the repair-rule store and the bandit's policy would
	// be thrown away with it — turning "it gets better at your repo" into
	// "it starts from zero every run".
	cfg.StateDir = filepath.Join(iso.origRoot, config.DirName)
	cfg.Root = sb.Root()

	// The orchestrator — and the workspace inside it — was already built from
	// the ORIGINAL root by openHarness. Mutating Config.Root afterwards changes
	// nothing they can see, so without this rebuild an "isolated" run writes
	// straight into the operator's checkout while cheerfully reporting a
	// worktree path. Measured: the first end-to-end isolated run edited
	// main.go in the origin.
	orch, err := orchestrator.New(cfg)
	if err != nil {
		_ = sb.Discard(ctx)
		cfg.Root, cfg.StateDir = iso.origRoot, ""
		return nil, fmt.Errorf("rebuild orchestrator for the worktree: %w", err)
	}
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		cli.Warn("previous orchestrator did not close cleanly: " + cerr.Error())
	}

	cli.KeyVal("isolation", "worktree")
	cli.KeyVal("worktree", sb.Root())
	cli.KeyVal("branch", sb.Branch())
	return iso, nil
}

// finish adopts or abandons the sandbox once the run is over.
//
// A successful run's work is merged back into the branch the operator was on;
// a failed one is thrown away whole. Neither is fatal to the caller: the run's
// own outcome has already been decided and reported, and a cleanup problem
// must not restate it as a different result.
func (i *isolation) finish(ctx context.Context, success, deliveredPR bool) {
	if i == nil || i.sb == nil {
		return
	}
	if !success {
		i.abandon(ctx)
		return
	}
	if deliveredPR {
		// A pull request IS the delivery. Merging the same branch locally as
		// well would put the work on the operator's branch before anyone
		// reviewed it — pre-empting the exact decision the PR exists to get.
		i.preserve(ctx)
		return
	}

	committed, err := i.sb.Commit(ctx, "slmcode: isolated run")
	if err != nil {
		cli.Warn("could not commit the isolated run: " + err.Error())
		i.preserve(ctx)
		return
	}
	if !committed {
		// A run that succeeded and wrote nothing is a legitimate outcome — an
		// inquiry, or a change already satisfied on disk. There is nothing to
		// merge and nothing to keep.
		_ = i.sb.Discard(ctx)
		return
	}
	if err := i.sb.Adopt(ctx); err != nil {
		cli.Warn("could not merge the isolated run: " + err.Error())
		i.preserve(ctx)
		return
	}
	cli.KeyVal("merged", i.sb.Branch()+" → "+i.sb.BaseBranch())
	_ = i.sb.Discard(ctx)
}

// abandon throws the sandbox away, or keeps its branch when asked.
func (i *isolation) abandon(ctx context.Context) {
	if i.keep {
		// Committing before keeping is what makes --keep-worktree mean
		// anything after a failure: an uncommitted worktree removed by Keep
		// takes its own changes with it, so the branch left behind would be
		// empty — the one state a user asking to inspect a failed run does not
		// want.
		if _, err := i.sb.Commit(ctx, "slmcode: abandoned run"); err != nil {
			cli.Warn("could not commit the abandoned run: " + err.Error())
		}
		i.preserve(ctx)
		return
	}
	if err := i.sb.Discard(ctx); err != nil {
		cli.Warn("could not remove the worktree: " + err.Error())
		return
	}
	fmt.Println(cli.Dim("  isolated run discarded — your checkout is untouched"))
}

// preserve keeps the branch and tells the operator how to reach it.
func (i *isolation) preserve(ctx context.Context) {
	if err := i.sb.Keep(ctx); err != nil {
		cli.Warn("could not remove the worktree: " + err.Error())
		return
	}
	cli.KeyVal("kept branch", i.sb.Branch())
	fmt.Println(cli.Dim("  inspect it with: git diff " + i.sb.BaseBranch() + "..." + i.sb.Branch()))
}

// registerIsolationFlags adds the isolation flags to `run`.
func registerIsolationFlags(cmd *cobra.Command) {
	cmd.Flags().String("isolate", "",
		"run against an isolated copy: 'worktree' (git) or 'none' (default, in-place)")
	cmd.Flags().Bool("keep-worktree", false,
		"keep the branch when an isolated run fails, instead of discarding it")
}
