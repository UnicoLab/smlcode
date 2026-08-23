package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// The end of a run.
//
// The run summary is the last thing a user reads, and for a long time it was
// the least useful thing on screen: it printed a duration, a failed-task count,
// two file paths (one of which — errors.md — was printed even when there were
// no errors) and stopped. It never said whether anything had actually changed
// on disk. The most common real outcome of a local-SLM run is "the model
// hallucinated an edit, the evidence gate refused it, the task escalated" — and
// that outcome looked exactly like a successful one minus a checkmark: no diff,
// no statement that the tree is untouched, no next step.
//
// printRunOutcome answers the only two questions that matter at the end:
// what changed, and what do I do now.

// changeFingerprint maps a workspace-relative path onto a content hash.
type changeFingerprint map[string]string

// slmStateDir is excluded from every "what did this run change" answer: the
// harness rewrites its own board, context docs and session logs on every run,
// and counting those as changes would mean a run that touched nothing still
// reported "7 files changed".
const slmStateDir = ".slmcode/"

// fingerprintDirty hashes the files that are ALREADY modified or untracked
// before a run starts.
//
// Comparing the end-of-run diff against HEAD alone would blame this run for
// whatever the user had in their working tree when they started it. Only files
// that are dirty at the start can be misattributed, so only those need hashing
// — on a clean tree this walks nothing.
func fingerprintDirty(root string) changeFingerprint {
	fp := changeFingerprint{}
	if root == "" || !isGitRepo(root) {
		return fp
	}
	seen := map[string]bool{}
	for _, list := range [][]string{gitChangedFiles(root, nil), gitUntrackedFiles(root, nil)} {
		for _, rel := range list {
			if seen[rel] || isSlmState(rel) {
				continue
			}
			seen[rel] = true
			fp[rel] = hashFile(filepath.Join(root, rel))
		}
	}
	return fp
}

func isSlmState(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel == strings.TrimSuffix(slmStateDir, "/") || strings.HasPrefix(rel, slmStateDir)
}

func hashFile(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // path is inside the user's own project root
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// runChanges returns the diffs this run is responsible for: everything dirty at
// the end, minus the files that were already dirty with identical content at
// the start, minus the harness's own .slmcode/ state.
func runChanges(root string, before changeFingerprint) []cli.FileDiff {
	all, err := collectWorkspaceDiffs(root, nil, 3, true)
	if err != nil {
		return nil
	}
	out := make([]cli.FileDiff, 0, len(all))
	for _, fd := range all {
		if isSlmState(fd.Path) {
			continue
		}
		if prev, ok := before[fd.Path]; ok && prev != "" && prev == hashFile(filepath.Join(root, fd.Path)) {
			continue // untouched by this run; it was already dirty
		}
		out = append(out, fd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// changeTotals sums the per-file counters.
func changeTotals(diffs []cli.FileDiff) (files, added, removed int) {
	for _, fd := range diffs {
		files++
		added += fd.Added
		removed += fd.Removed
	}
	return files, added, removed
}

// maxOutcomeFiles caps the per-file list in the summary; the rest is a count.
const maxOutcomeFiles = 8

// taskTally is the board's own account of a finished run.
type taskTally struct {
	total     int
	done      int
	forced    int // done because a human overrode the evidence gate
	escalated int // attempted, then parked for a human
	blocked   int // aborted
	untouched int // never attempted (the run stopped before them)
	// stuck names the first task a human should look at, or "" when the run
	// never got far enough for any task to have a verdict worth reading.
	stuck string
}

// tallyBoard counts what the board looks like when the run stops.
//
// forced matters: `[d]one` on the escalate gate moves a task to the done column
// with the engine's own marker on it, and the old summary then reported
// "1/1 tasks done, 0 failed" with no trace that a human had waved through a
// task the evidence gate had refused. That is the one number in the summary a
// reader is entitled to distrust, so it is broken out rather than folded in.
func tallyBoard(b plan.Board) taskTally {
	var t taskTally
	for _, task := range b.Tasks {
		t.total++
		switch {
		case task.Column == plan.ColDone:
			t.done++
			if humanForcedDone(task) {
				t.forced++
			}
		case task.Column == plan.ColBlocked:
			t.blocked++
			if t.stuck == "" {
				t.stuck = task.ID
			}
		case taskWasAttempted(task):
			// Parked back in to_scope / left in review WITH a verdict on it:
			// the engine tried this one and gave up on it.
			t.escalated++
			if t.stuck == "" {
				t.stuck = task.ID
			}
		default:
			// Never reached — e.g. the run stopped at the plan gate. Saying
			// "awaiting a human" about a task no agent has looked at, and
			// pointing at its (empty) review verdict, is a lie.
			t.untouched++
		}
	}
	return t
}

// taskWasAttempted reports whether an agent has actually worked this task.
//
// A task the engine never reached carries no output, no review and no error;
// one it fought with and escalated carries at least one of the three.
func taskWasAttempted(t plan.Task) bool {
	return t.Retries > 0 ||
		strings.TrimSpace(t.Output) != "" ||
		strings.TrimSpace(t.Review) != "" ||
		strings.TrimSpace(t.Error) != ""
}

// humanForcedDoneMarker is what pkg/plan writes onto a task when a human
// answers [d]one at the escalate gate (plan.ApplyEscalateAction).
const humanForcedDoneMarker = "human mark_done after escalate"

// humanForcedDone reports whether a done task got there by human override.
func humanForcedDone(t plan.Task) bool {
	return strings.Contains(strings.ToLower(t.Review), humanForcedDoneMarker)
}

// outcomeOptions carries everything printRunOutcome needs that is not on the
// result itself.
type outcomeOptions struct {
	root    string
	slmDir  string
	before  changeFingerprint
	board   plan.Board
	success bool
	// overrides are the tasks a human force-marked done at the escalate gate
	// in THIS process, used as a floor in case the engine's own marker is not
	// on the board (a run that died before the board was written back).
	overrides []string
	// failure is set when the engine returned an error instead of a result.
	failure error
}

// printRunOutcome renders the closing block of a run: what changed on disk,
// what the board looks like, and the next command to type.
//
// It runs on BOTH the success and the failure path. A run that dies at a gate
// or blows a safety guard has still usually touched the tree, and "did my files
// change?" is a more urgent question after a failure than after a success.
func printRunOutcome(opt outcomeOptions) {
	diffs := runChanges(opt.root, opt.before)
	files, added, removed := changeTotals(diffs)
	tally := tallyBoard(opt.board)
	if n := len(opt.overrides); n > tally.forced {
		tally.forced = n
	}
	pending := pendingCount(opt.slmDir)

	fmt.Println()
	if files == 0 {
		printNoChangeOutcome(opt, tally, pending)
		return
	}

	fmt.Println(cli.Bold("Changes"))
	fmt.Printf("  %s\n", changeHeadline(files, added, removed))
	for i, fd := range diffs {
		if i >= maxOutcomeFiles {
			fmt.Println(cli.Dim(fmt.Sprintf("    … +%d more file(s)", len(diffs)-maxOutcomeFiles)))
			break
		}
		fmt.Println("  " + cli.DiffStatLine(fd))
	}
	fmt.Println()
	printNextSteps(nextStepsFor(opt, tally, pending, files))
}

// changeHeadline renders the compact "3 files · +47 −12" line.
func changeHeadline(files, added, removed int) string {
	noun := "files"
	if files == 1 {
		noun = "file"
	}
	return fmt.Sprintf("%s %s %s %s",
		cli.Bold(fmt.Sprintf("%d %s", files, noun)),
		cli.Dim("·"),
		cli.Green(fmt.Sprintf("+%d", added)),
		cli.Red(fmt.Sprintf("−%d", removed)))
}

// printNoChangeOutcome states, in words, that the tree is untouched.
//
// This is the case the CLI used to be silent about. A 40-second run that ends
// with an escalation and no diff is a legitimate outcome — the evidence gate
// refusing an edit the model never actually made is the harness working — but
// the user has to be TOLD, otherwise the only reading available is "it did
// something and won't show me".
func printNoChangeOutcome(opt outcomeOptions, tally taskTally, pending int) {
	fmt.Println(cli.Bold("Changes"))
	fmt.Println("  " + cli.Warn("no files changed — nothing was created, modified or deleted on disk"))
	switch {
	case pending > 0:
		fmt.Println(cli.Dim(fmt.Sprintf(
			"  %d proposed edit(s) are held for review and have NOT been written yet", pending)))
	case tally.escalated > 0 || tally.blocked > 0:
		fmt.Println(cli.Dim("  the model's edits were refused before they reached the tree" +
			" (usually: an edit was claimed but never made)"))
	case tally.untouched > 0:
		fmt.Println(cli.Dim(fmt.Sprintf(
			"  the run stopped before any of the %d planned task(s) was attempted", tally.untouched)))
	case !opt.success:
		fmt.Println(cli.Dim("  the run stopped before any edit was written"))
	}
	fmt.Println()
	printNextSteps(nextStepsFor(opt, tally, pending, 0))
}

// nextStep is one suggested command plus what it answers.
type nextStep struct {
	cmd  string
	what string
}

// nextStepsFor picks the two-to-four commands that are actually useful here.
func nextStepsFor(opt outcomeOptions, tally taskTally, pending, files int) []nextStep {
	var steps []nextStep
	if pending > 0 {
		steps = append(steps, nextStep{"slmcode apply",
			fmt.Sprintf("review the %d held edit(s) file by file", pending)})
	}
	if files > 0 {
		steps = append(steps, nextStep{"slmcode diff", "the full patch, file by file"})
		steps = append(steps, nextStep{"slmcode commit -m \"…\"", "keep it"})
	}
	if id := tally.stuck; id != "" {
		steps = append(steps, nextStep{"slmcode task show " + id,
			"why " + id + " stopped: verdict, gate and diff"})
	}
	if files == 0 && tally.stuck == "" && tally.total > 0 {
		steps = append(steps, nextStep{"slmcode board", "the board as the run left it"})
	}
	if files == 0 {
		steps = append(steps, nextStep{"slmcode run --vv \"…\"",
			"re-run with the full agent transcript"})
	}
	return steps
}

func printNextSteps(steps []nextStep) {
	if len(steps) == 0 {
		return
	}
	width := 0
	for _, s := range steps {
		if len(s.cmd) > width {
			width = len(s.cmd)
		}
	}
	fmt.Println(cli.Bold("Next"))
	for _, s := range steps {
		fmt.Printf("  %s  %s\n", cli.Accent(cli.PadWidth(s.cmd, width)), cli.Dim(s.what))
	}
}

// printTaskTally renders the board line, breaking out human overrides.
func printTaskTally(t taskTally) {
	if t.total == 0 {
		return
	}
	line := fmt.Sprintf("%d/%d done", t.done, t.total)
	var extra []string
	if t.forced > 0 {
		noun := "override"
		if t.forced > 1 {
			noun = "overrides"
		}
		extra = append(extra, cli.Yellow(fmt.Sprintf("%d human %s — you answered [d]one at the escalate gate",
			t.forced, noun)))
	}
	if t.escalated > 0 {
		extra = append(extra, cli.Yellow(fmt.Sprintf("%d awaiting a human", t.escalated)))
	}
	if t.blocked > 0 {
		extra = append(extra, cli.Red(fmt.Sprintf("%d blocked", t.blocked)))
	}
	if t.untouched > 0 {
		extra = append(extra, cli.Dim(fmt.Sprintf("%d never attempted", t.untouched)))
	}
	if len(extra) > 0 {
		line += "  ·  " + strings.Join(extra, "  ·  ")
	}
	cli.KeyVal("tasks", line)
}

// boardSnapshot reads board.json from disk.
//
// The failure path has no Result to read a board off, but the board itself was
// checkpointed as the run went — so the summary can still say how far it got.
func boardSnapshot(slmDir string) plan.Board {
	if slmDir == "" {
		return plan.Board{}
	}
	store := plan.NewLiveStore(slmDir)
	if err := store.Load(); err != nil {
		return plan.Board{}
	}
	return store.Snapshot()
}

// errorsLogPath returns the errors log ONLY when it holds something.
//
// The old summary printed "errors: …/errors.md" on every run, successful ones
// included, pointing at a file that in the common case does not exist. A path
// that is printed unconditionally teaches the reader to ignore it.
func errorsLogPath(slmDir string) string {
	if slmDir == "" {
		return ""
	}
	path := filepath.Join(slmDir, "errors", "errors.md")
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return ""
	}
	return path
}
