package autoresearch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Budget bounds a run on three axes at once.
//
// One axis is never enough. Experiment count alone lets a slow evaluator run
// all night; wall clock alone lets a cheap-looking run burn a month's tokens;
// tokens alone cannot stop a loop whose evaluator is hanging. All three are
// checked before every experiment, and whichever binds first is the one the
// Result names.
type Budget struct {
	MaxExperiments int           `json:"max_experiments"`
	MaxWallClock   time.Duration `json:"max_wall_clock"`
	MaxTokens      int           `json:"max_tokens"`
}

// DefaultBudget is a deliberately small run: a dozen experiments, half an hour,
// two million tokens. A ratchet you have to stop by hand is one people run once.
func DefaultBudget() Budget {
	return Budget{MaxExperiments: 12, MaxWallClock: 30 * time.Minute, MaxTokens: 2_000_000}
}

func (b Budget) withDefaults() Budget {
	d := DefaultBudget()
	if b.MaxExperiments == 0 {
		b.MaxExperiments = d.MaxExperiments
	}
	if b.MaxWallClock == 0 {
		b.MaxWallClock = d.MaxWallClock
	}
	if b.MaxTokens == 0 {
		b.MaxTokens = d.MaxTokens
	}
	return b
}

// StopReason says why a run ended.
type StopReason string

// Stop reasons.
const (
	StopExperiments StopReason = "max-experiments"
	StopWallClock   StopReason = "max-wall-clock"
	StopTokens      StopReason = "max-tokens"
	StopExhausted   StopReason = "surface-exhausted"
	StopCanceled    StopReason = "canceled"
	StopEvalFailed  StopReason = "evaluation-failed"
	StopDryRun      StopReason = "dry-run"
	StopNoSurface   StopReason = "empty-surface"
)

// Sentence renders the stop reason as something a human can act on.
func (r StopReason) Sentence() string {
	switch r {
	case StopExperiments:
		return "experiment budget spent — the surface was NOT exhausted, so more remains untried"
	case StopWallClock:
		return "wall-clock budget spent — the surface was NOT exhausted, so more remains untried"
	case StopTokens:
		return "token budget spent — the surface was NOT exhausted, so more remains untried"
	case StopExhausted:
		// "every knob" was not true and this is the one message in the package
		// built to be trusted — every sibling above goes out of its way to say
		// when the surface was NOT exhausted.
		//
		// StopExhausted is reached on ErrNoProposal from the DETERMINISTIC
		// proposer, which enumerates values and therefore cannot touch a text
		// knob at all (see surface.go: a text domain has no enumeration and
		// returns nil). A text knob such as system_prompt is reachable only
		// through the LLM proposer, which is asked on a fixed cadence and gives
		// up its slot on a model error, an empty reply, or an unchanged
		// rewrite — so a small local model can burn every chance and leave the
		// knob untried while this line claimed it had been swept.
		return "enumerable surface exhausted — every value of every enumerable knob " +
			"was tried; text knobs, if the surface has any, are reachable only " +
			"through the LLM proposer and may remain untried"
	case StopCanceled:
		return "canceled — the surface was restored to its pre-run state"
	case StopEvalFailed:
		return "evaluation failed — the trial was reverted and the run stopped"
	case StopDryRun:
		return "dry run — proposals were printed, nothing was applied"
	case StopNoSurface:
		return "nothing to tune — the surface has no mutable knobs"
	default:
		return string(r)
	}
}

// RatchetOptions configures a run.
type RatchetOptions struct {
	// Surface is what may be mutated. Required.
	Surface *Surface
	// Evaluator scores the harness. Required.
	Evaluator Evaluator
	// Proposer picks each change. Defaults to a seeded deterministic proposer.
	Proposer Proposer
	// Budget bounds the run. Zero fields take DefaultBudget's values.
	Budget Budget
	// Guards veto a change that pays for its improvement somewhere else.
	// Nil means DefaultGuards() — guarding is ON by default, and turning it off
	// takes an explicit empty slice.
	Guards []Guard
	// Seed fixes the experiment sequence.
	Seed int64
	// MinImprovement is how much the primary must move to count. Zero means
	// "strictly better".
	MinImprovement float64
	// DryRun proposes and records without applying anything.
	DryRun bool
	// Journal records trials. Nil disables journaling (used by tests).
	Journal *Journal
	// SnapshotDir holds the durable pre-run snapshot. Empty means no durable
	// snapshot — in-process reverts still work.
	SnapshotDir string
	// Now is injectable for tests.
	Now func() time.Time
	// OnTrial is called after each trial, for live CLI output.
	OnTrial func(Trial)
}

// Result is everything a finished run knows about itself.
type Result struct {
	Seed        int64      `json:"seed"`
	Baseline    Score      `json:"baseline"`
	Best        Score      `json:"best"`
	Kept        []Change   `json:"kept"`
	Trials      []Trial    `json:"trials"`
	StopReason  StopReason `json:"stop_reason"`
	StopDetail  string     `json:"stop_detail"`
	Experiments int        `json:"experiments"`
	TokensUsed  int        `json:"tokens_used"`
	DurationMS  int64      `json:"duration_ms"`
	DryRun      bool       `json:"dry_run"`
	SnapshotDir string     `json:"snapshot_dir,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
}

// Improved reports whether anything was retained.
func (r Result) Improved() bool { return len(r.Kept) > 0 }

// Reverted counts the trials that were tried and thrown away.
func (r Result) Reverted() int {
	n := 0
	for _, t := range r.Trials {
		if !t.Kept && !t.DryRun {
			n++
		}
	}
	return n
}

// GuardVetoes counts the trials that improved the primary metric and were
// reverted anyway because a guarded metric regressed. It is the number that
// says whether the anti-gaming guard is doing anything.
func (r Result) GuardVetoes() int {
	n := 0
	for _, t := range r.Trials {
		if t.Guard != "" {
			n++
		}
	}
	return n
}

// Ratchet is the loop.
type Ratchet struct {
	surface   *Surface
	evaluator Evaluator
	proposer  Proposer
	budget    Budget
	guards    []Guard
	seed      int64
	minImp    float64
	dryRun    bool
	journal   *Journal
	snapDir   string
	now       func() time.Time
	onTrial   func(Trial)

	baseline Score
	best     Score
}

// New builds a ratchet.
func New(opts RatchetOptions) (*Ratchet, error) {
	if opts.Surface == nil {
		return nil, errors.New("autoresearch: a ratchet needs a surface")
	}
	if opts.Evaluator == nil {
		return nil, errors.New("autoresearch: a ratchet needs an evaluator")
	}
	r := &Ratchet{
		surface:   opts.Surface,
		evaluator: opts.Evaluator,
		proposer:  opts.Proposer,
		budget:    opts.Budget.withDefaults(),
		guards:    opts.Guards,
		seed:      opts.Seed,
		minImp:    opts.MinImprovement,
		dryRun:    opts.DryRun,
		journal:   opts.Journal,
		snapDir:   opts.SnapshotDir,
		now:       opts.Now,
		onTrial:   opts.OnTrial,
	}
	if r.proposer == nil {
		r.proposer = NewDeterministicProposer(opts.Seed)
	}
	if r.guards == nil {
		r.guards = DefaultGuards()
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r, nil
}

// Run executes the loop:
//
//	snapshot → propose → apply → evaluate → keep or restore → record → repeat
//
// It returns the best artifact it found together with the reason it stopped,
// even when it stopped badly. An error is returned only when the run could not
// establish a baseline at all — everything after that point is reportable
// progress, and hiding a spent budget behind a fluent summary is exactly the
// failure this design exists to avoid.
func (r *Ratchet) Run(ctx context.Context) (Result, error) {
	start := r.now()
	res := Result{Seed: r.seed, DryRun: r.dryRun, Warnings: r.surface.Warnings()}
	defer func() { res.DurationMS = r.now().Sub(start).Milliseconds() }()

	files := r.surface.Files()
	if r.surface.Len() == 0 {
		res.StopReason = StopNoSurface
		res.StopDetail = StopNoSurface.Sentence()
		return res, nil
	}

	// The durable snapshot goes down BEFORE the baseline evaluation, not after:
	// an evaluator is allowed to be slow, and a run killed during the baseline
	// must still be undoable.
	if !r.dryRun && r.snapDir != "" {
		snap, err := Capture(files)
		if err != nil {
			return res, fmt.Errorf("snapshot: %w", err)
		}
		if err := snap.Persist(r.snapDir); err != nil {
			return res, fmt.Errorf("persist snapshot: %w", err)
		}
		res.SnapshotDir = r.snapDir
	}

	// A dry run measures nothing, so it evaluates nothing. That is not an
	// optimization: `slmcode autoresearch` with no flags is a dry run, and a
	// default invocation that quietly spun up a model and spent three minutes on
	// the eval suite would be a nasty surprise for a command whose whole promise
	// is that it does nothing until asked.
	base := UnknownScore()
	if !r.dryRun {
		var err error
		if base, err = r.evaluate(ctx, "baseline"); err != nil {
			return res, fmt.Errorf("baseline evaluation: %w", err)
		}
		res.TokensUsed = base.Tokens
	}
	r.baseline, r.best = base, base
	res.Baseline, res.Best = base, base

	for {
		if stop, detail := r.budgetStop(start, len(res.Trials), res.TokensUsed); stop != "" {
			res.StopReason, res.StopDetail = stop, detail
			break
		}
		if err := ctx.Err(); err != nil {
			res.StopReason, res.StopDetail = StopCanceled, StopCanceled.Sentence()
			break
		}

		change, err := r.proposer.Propose(ctx, r.surface, History{Trials: res.Trials})
		switch {
		case errors.Is(err, ErrNoProposal):
			res.StopReason, res.StopDetail = StopExhausted, StopExhausted.Sentence()
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			res.StopReason, res.StopDetail = StopCanceled, StopCanceled.Sentence()
		case err != nil:
			res.StopReason = StopEvalFailed
			res.StopDetail = "proposer failed: " + err.Error()
		}
		if res.StopReason != "" {
			break
		}

		trial := r.runTrial(ctx, len(res.Trials)+1, change)
		res.Trials = append(res.Trials, trial)
		res.Experiments++
		res.TokensUsed += trial.Score.Tokens
		if trial.Kept {
			r.best = trial.Score
			res.Best = trial.Score
			res.Kept = append(res.Kept, change)
		}
		if r.journal != nil {
			if err := r.journal.Append(trial); err != nil {
				res.Warnings = append(res.Warnings, "journal: "+err.Error())
			}
		}
		if r.onTrial != nil {
			r.onTrial(trial)
		}

		if trial.Error != "" {
			if ctx.Err() != nil {
				res.StopReason, res.StopDetail = StopCanceled, StopCanceled.Sentence()
			} else {
				res.StopReason = StopEvalFailed
				res.StopDetail = StopEvalFailed.Sentence() + ": " + trial.Error
			}
			break
		}
	}

	if r.dryRun {
		res.StopReason, res.StopDetail = StopDryRun, StopDryRun.Sentence()
	}
	if res.StopReason == "" {
		res.StopReason, res.StopDetail = StopExhausted, StopExhausted.Sentence()
	}
	if r.journal != nil {
		if _, err := r.journal.Prune(MaxTrials); err != nil {
			res.Warnings = append(res.Warnings, "journal prune: "+err.Error())
		}
		if err := r.journal.WriteBest(res); err != nil {
			res.Warnings = append(res.Warnings, "BEST.md: "+err.Error())
		}
		res.Warnings = append(res.Warnings, r.journal.Warnings()...)
	}
	return res, nil
}

// budgetStop reports the first cap that binds, checked before each experiment.
func (r *Ratchet) budgetStop(start time.Time, done, tokens int) (StopReason, string) {
	if done >= r.budget.MaxExperiments {
		return StopExperiments, fmt.Sprintf("%s (%d/%d experiments)",
			StopExperiments.Sentence(), done, r.budget.MaxExperiments)
	}
	if spent := r.now().Sub(start); spent >= r.budget.MaxWallClock {
		return StopWallClock, fmt.Sprintf("%s (%s of %s)",
			StopWallClock.Sentence(), spent.Round(time.Second), r.budget.MaxWallClock)
	}
	if tokens >= r.budget.MaxTokens {
		return StopTokens, fmt.Sprintf("%s (%d/%d tokens)",
			StopTokens.Sentence(), tokens, r.budget.MaxTokens)
	}
	return "", ""
}

// runTrial is one experiment, from apply to verdict.
//
// The contract this function exists to keep: whatever happens between the apply
// and the verdict — an error, a canceled context, a panic inside somebody
// else's evaluator — the file goes back. That is why the restore is in a defer
// alongside the recover, why `applied` is tracked rather than assumed, and why
// the named return is mutated from the defer.
func (r *Ratchet) runTrial(ctx context.Context, seq int, change Change) (t Trial) {
	started := r.now()
	t = Trial{
		Seq: seq, At: started.UTC(), Seed: r.seed,
		KnobID: change.KnobID, Before: change.Before, After: change.After,
		Origin: change.Origin, Reason: change.Reason,
		Baseline: r.best, DryRun: r.dryRun,
	}
	defer func() { t.DurationMS = r.now().Sub(started).Milliseconds() }()

	if r.dryRun {
		t.Reason = "dry run — proposed only, nothing applied"
		return t
	}

	knob, ok := r.surface.Knob(change.KnobID)
	if !ok {
		t.Error = ErrNotMutable.Error() + ": " + change.KnobID
		t.Reason = "refused: not on the mutable surface"
		return t
	}
	before, err := Capture([]string{knob.File})
	if err != nil {
		t.Error = err.Error()
		t.Reason = "refused: could not snapshot " + knob.File
		return t
	}

	applied := false
	defer func() {
		// A panicking evaluator is not a reason to leave somebody's agent
		// prompt half-rewritten. Recover, convert to an error the caller can
		// see, then restore on the way out.
		if rec := recover(); rec != nil {
			t.Kept = false
			t.Error = fmt.Sprintf("evaluation panicked: %v", rec)
			t.Reason = "reverted: the evaluator panicked"
		}
		if !applied || t.Kept {
			return
		}
		if rerr := before.Restore(); rerr != nil {
			t.Error = strings.TrimSpace(t.Error + " | restore failed: " + rerr.Error())
			return
		}
		r.surface.SetValue(change.KnobID, change.Before)
	}()

	if err := r.surface.Apply(change); err != nil {
		t.Error = err.Error()
		t.Reason = "refused: " + err.Error()
		return t
	}
	applied = true

	score, err := r.evaluate(ctx, fmt.Sprintf("trial %d", seq))
	if err != nil {
		t.Error = err.Error()
		t.Reason = "reverted: evaluation failed"
		return t
	}
	t.Score = score

	// The keep rule, in the order it must be applied.
	//
	// 1. The primary must actually move. "No worse" is not an improvement, and
	//    retaining it would let the surface drift on noise.
	if score.Primary-r.best.Primary <= r.minImp {
		t.Reason = fmt.Sprintf("reverted: primary did not improve (%s → %s)",
			pct(r.best.Primary), pct(score.Primary))
		return t
	}
	// 2. No guarded metric may regress against the current champion…
	if breach, ok := CheckGuards(r.best, score, r.guards, "champion"); !ok {
		t.Guard = breach.Name
		t.Reason = "reverted: " + breach.String()
		return t
	}
	// 3. …nor against the run's own baseline. Checking only the champion lets a
	//    sequence of individually-tolerable regressions add up to a large one:
	//    five steps each 4.9% worse on tokens pass every pairwise check and land
	//    27% worse than where the run started. This is the check that closes it.
	if breach, ok := CheckGuards(r.baseline, score, r.guards, "baseline"); !ok {
		t.Guard = breach.Name
		t.Reason = "reverted: " + breach.String()
		return t
	}

	t.Kept = true
	t.Reason = fmt.Sprintf("kept: primary %s → %s with no guarded regression",
		pct(r.best.Primary), pct(score.Primary))
	return t
}

// evaluate runs the evaluator and labels the score.
func (r *Ratchet) evaluate(ctx context.Context, label string) (Score, error) {
	score, err := r.evaluator.Evaluate(ctx)
	if err != nil {
		return score, err
	}
	score.Label = label
	return score, nil
}

// RestoreLast puts a project back to its state before the last run.
//
// This is the reversal path for the failures a defer cannot cover — a SIGKILL,
// a crashed machine — and for simple regret about a run that kept something you
// did not want.
func RestoreLast(root string) ([]string, error) {
	snap, err := LoadSnapshot(SnapshotDir(root))
	if err != nil {
		return nil, err
	}
	if err := snap.Restore(); err != nil {
		return nil, err
	}
	return snap.Paths(), nil
}

// RenderBest is BEST.md: what a run retained, what it rejected and why, and the
// stated reason it stopped.
func (r Result) RenderBest() string {
	var b strings.Builder
	b.WriteString("# Autoresearch: retained changes\n\n")
	fmt.Fprintf(&b, "- seed: `%d`\n", r.Seed)
	fmt.Fprintf(&b, "- experiments: %d (kept %d, reverted %d, guard vetoes %d)\n",
		r.Experiments, len(r.Kept), r.Reverted(), r.GuardVetoes())
	fmt.Fprintf(&b, "- tokens: %d\n", r.TokensUsed)
	fmt.Fprintf(&b, "- duration: %s\n", (time.Duration(r.DurationMS) * time.Millisecond).Round(time.Second))
	fmt.Fprintf(&b, "- **stopped because:** %s\n\n", r.StopDetail)

	b.WriteString("## Score\n\n")
	b.WriteString("| | baseline | best |\n|---|---:|---:|\n")
	fmt.Fprintf(&b, "| primary (task pass rate) | %s | %s |\n", pct(r.Baseline.Primary), pct(r.Best.Primary))
	fmt.Fprintf(&b, "| tokens per task | %s | %s |\n", num(r.Baseline.TokensPerTask), num(r.Best.TokensPerTask))
	fmt.Fprintf(&b, "| wall seconds per task | %s | %s |\n", num(r.Baseline.SecondsPerTask), num(r.Best.SecondsPerTask))
	fmt.Fprintf(&b, "| tool error rate | %s | %s |\n", pct(r.Baseline.ToolErrorRate), pct(r.Best.ToolErrorRate))
	fmt.Fprintf(&b, "| edit-format apply rate | %s | %s |\n\n", pct(r.Baseline.EditApplyRate), pct(r.Best.EditApplyRate))

	b.WriteString("## Retained\n\n")
	if len(r.Kept) == 0 {
		b.WriteString("_Nothing was retained — the harness is unchanged._\n\n")
	} else {
		for _, c := range r.Kept {
			fmt.Fprintf(&b, "- `%s`: `%s` → `%s`\n", c.KnobID, clip(c.Before, 60), clip(c.After, 60))
		}
		b.WriteString("\n")
	}

	if vetoes := r.guardVetoLines(); len(vetoes) > 0 {
		b.WriteString("## Rejected by a guard\n\n")
		b.WriteString("These improved the primary metric and were reverted anyway, because they paid\n")
		b.WriteString("for it in a metric the guard set watches.\n\n")
		for _, line := range vetoes {
			b.WriteString("- " + line + "\n")
		}
		b.WriteString("\n")
	}

	if len(r.Warnings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, w := range sortedUnique(r.Warnings) {
			b.WriteString("- " + w + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("_Undo everything this run kept: `slmcode autoresearch --restore`._\n")
	return b.String()
}

func (r Result) guardVetoLines() []string {
	var out []string
	for _, t := range r.Trials {
		if t.Guard == "" {
			continue
		}
		out = append(out, fmt.Sprintf("`%s` → `%s` — %s", t.KnobID, clip(t.After, 40), t.Reason))
	}
	return out
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
