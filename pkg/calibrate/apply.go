package calibrate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
)

// Consumers of a Profile.
//
// The rule is the same for every one of them: a measurement improves on a
// DEFAULT and never on a CHOICE. `config.Config.Explicit` is the authority on
// which is which, and every Apply* function below checks it first. A user who
// wrote `max_parallel: 4` on a laptop that measures a knee of 2 gets 4 — being
// wrong about their hardware is their prerogative, and silently overriding a
// written-down value is how a harness loses trust.

// Applied records one value calibration changed, for the startup line.
type Applied struct {
	Key  string
	From string
	To   string
	Why  string
}

func (a Applied) String() string {
	return fmt.Sprintf("%s %s → %s (%s)", a.Key, a.From, a.To, a.Why)
}

// Apply folds a profile into cfg, touching only what the user has not set.
// It returns what it changed, in a stable order, and never mutates cfg on a
// zero-valued or unusable profile.
func Apply(cfg *config.Config, p Profile) []Applied {
	if cfg == nil || p.MaxParallel <= 0 {
		return nil
	}
	var out []Applied

	if !cfg.MaxParallelExplicit() && cfg.MaxParallel != p.MaxParallel {
		why := fmt.Sprintf("measured knee for %s", p.Key.String())
		if l, ok := p.levelAbove(p.MaxParallel); ok {
			why = fmt.Sprintf("%d-way ran at %.0f%% efficiency", l.Concurrency, l.Efficiency*100)
		}
		out = append(out, Applied{
			Key: "max_parallel", From: itoa(cfg.MaxParallel), To: itoa(p.MaxParallel), Why: why,
		})
		cfg.SetMaxParallel(p.MaxParallel)
	}

	if a, ok := applyContext(cfg, p); ok {
		out = append(out, a)
	}
	if a, ok := applyTaskTimeout(cfg, p); ok {
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// applyContext installs the server-reported context window as this model's
// profile entry, when the user has not written model_profiles themselves.
//
// The value is a fact the server stated, not an inference, so it beats the
// shipped size-bucket heuristic ("30b" → 262144 here, where the bucket guesses
// 32768). It is applied IN MEMORY only: the calibration store is the
// persistence, and freezing a probed number into config.yaml would survive a
// server reconfiguration that changed it.
func applyContext(cfg *config.Config, p Profile) (Applied, bool) {
	if p.ContextLimit <= 0 || cfg.Explicit("model_profiles") {
		return Applied{}, false
	}
	prof := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
	if prof.ContextLimit == p.ContextLimit {
		return Applied{}, false
	}
	key := config.NormProfileKey(cfg.Model)
	if cfg.ModelProfiles == nil {
		cfg.ModelProfiles = config.DefaultModelProfiles()
	}
	// Write the FULLY RESOLVED profile back, not a bare {ContextLimit} entry.
	// An exact-model key short-circuits ResolveModelProfile's family and
	// size-bucket refinement, so a partial entry would silently drop the
	// max_tokens / max_turns the buckets contribute.
	from := prof.ContextLimit
	entry := prof
	entry.ContextLimit = p.ContextLimit
	cfg.ModelProfiles[key] = entry
	return Applied{
		Key: "context_limit", From: itoa(from), To: itoa(p.ContextLimit),
		Why: p.ContextSource,
	}, true
}

// MaxAutoTaskTimeout caps what calibration will raise task_timeout to on its
// own.
//
// The recommendation itself is uncapped and still reported — for a 3 tok/s
// model a full role response really can take most of an hour. But task_timeout
// is a spend limit as much as a correctness knob, and a probe silently
// authorizing multi-hour role calls is not a trade a harness gets to make for
// someone. Past this point the recommendation is advice, and the user sets it.
const MaxAutoTaskTimeout = 30 * time.Minute

// applyTaskTimeout widens task_timeout when the measured decode rate says the
// configured ceiling cannot fit one full role response.
//
// It only ever RAISES, and only when unset: lowering a ceiling on the strength
// of a 16-token probe would turn a slow run into a failing one, and the user's
// own number is a deliberate spend limit.
func applyTaskTimeout(cfg *config.Config, p Profile) (Applied, bool) {
	if cfg.Explicit("task_timeout") {
		return Applied{}, false
	}
	prof := config.ResolveModelProfile(cfg.ModelProfiles, cfg.Model)
	want, ok := p.RecommendedTaskTimeout(prof.MaxTokens)
	if !ok {
		return Applied{}, false
	}
	why := fmt.Sprintf("%.1f tok/s × %d role tokens × %.2f queueing at max_parallel=%d",
		p.TokensPerSec, prof.MaxTokens, p.QueueInflation, p.MaxParallel)
	if want > MaxAutoTaskTimeout {
		why += fmt.Sprintf(" — recommendation was %s, capped at the %s automatic ceiling",
			want, MaxAutoTaskTimeout)
		want = MaxAutoTaskTimeout
	}
	if want <= cfg.TaskTimeout {
		return Applied{}, false
	}
	from := cfg.TaskTimeout
	cfg.TaskTimeout = want
	return Applied{Key: "task_timeout", From: from.String(), To: want.String(), Why: why}, true
}

// LatencyRecorder is the slice of pkg/memory's role-latency store this package
// needs. Narrow on purpose: calibration seeds that store, it does not own it,
// and depending on the whole memory.Store would drag a project-scoped
// subsystem into a user-scoped probe.
type LatencyRecorder interface {
	// Samples reports how many observations back (role, family) today.
	Samples(role, modelFamily string) int
	// Record folds one observation into the series for (role, family).
	Record(role, modelFamily string, d time.Duration)
}

// RoleSeed is one role's completion budget, which is what makes the seed
// proportional to the work the role actually does.
type RoleSeed struct {
	Role      string
	MaxTokens int
}

// SeedRoleLatency gives the role-timeout store a measured starting point.
//
// Why this exists: role budgets are derived from p95 per (role, model family),
// and with fewer than memory.MinLatencySamples observations the policy
// correctly falls back to the whole task_timeout ceiling. A live run reported
// "no latency measured yet for model family qwen3.8 (1/3 samples)" and then
// hit that ceiling — the first runs on any new model are the ones with no
// evidence, and they are exactly the runs that time out.
//
// Why it is safe, where seeding from backends.Probe would not be: this seed is
// PROPORTIONAL. It is measured tokens/sec × the role's own token budget +
// measured overhead × measured queueing inflation, so it tracks the model's
// real speed instead of being a fixed prefill time reused for every role.
//
// Three guardrails keep it honest:
//   - it never overwrites real evidence — a (role, family) that already has
//     samples is left alone;
//   - it is capped at the caller's timeout ceiling, so a slow model's seed
//     degrades to exactly today's cold-start behavior (the full budget)
//     instead of making a claim the harness would never grant. The seed can
//     therefore only ever TIGHTEN a budget for a model fast enough that a role
//     provably does not need the whole ceiling;
//   - it records exactly minSamples, so the store's own retention (32 samples
//     per key) displaces the seed within a run or two of real work.
//
// Returns the roles it seeded.
func SeedRoleLatency(store LatencyRecorder, family string, p Profile, roles []RoleSeed, minSamples int, ceiling time.Duration) []string {
	if store == nil || strings.TrimSpace(family) == "" || minSamples <= 0 {
		return nil
	}
	var seeded []string
	for _, r := range roles {
		role := strings.ToLower(strings.TrimSpace(r.Role))
		if role == "" {
			continue
		}
		if store.Samples(role, family) > 0 {
			continue // real evidence exists; never displace it
		}
		d, ok := p.RoleLatencySeed(r.MaxTokens)
		if !ok {
			continue
		}
		if ceiling > 0 && d > ceiling {
			d = ceiling
		}
		for i := 0; i < minSamples; i++ {
			store.Record(role, family, d)
		}
		seeded = append(seeded, role)
	}
	sort.Strings(seeded)
	return seeded
}

// ThroughputRecorder is the slice of pkg/backends' decode-rate tracker this
// package needs.
type ThroughputRecorder interface {
	Observe(model string, completionTokens int, elapsed time.Duration)
}

// SeedThroughput hands the measured decode rate to the tracker that per-call
// deadlines are derived from (backends.EstimateTimeout).
//
// Unlike the role-latency seed this needs no justification at all: a
// calibration call IS a completion, with a server-reported token count and a
// measured duration — the same evidence a real call provides. Without it every
// fresh model starts from the pessimistic DefaultTokensPerSec prior.
func SeedThroughput(rec ThroughputRecorder, p Profile) bool {
	if rec == nil || p.CompletionTokens <= 0 || p.TokensPerSec <= 0 {
		return false
	}
	// Reconstruct the duration the rate was measured over. Observe wants
	// (tokens, elapsed) because that is what a real call reports; handing it
	// the rate directly would need a second API on a store this package does
	// not own.
	elapsed := time.Duration(float64(p.CompletionTokens) / p.TokensPerSec * float64(time.Second))
	if elapsed <= 0 {
		return false
	}
	rec.Observe(p.Model, p.CompletionTokens, elapsed)
	return true
}

// Bandit posteriors are deliberately NOT seeded.
//
// pkg/evolve's bandit learns edit_format, think_passes, explore_phase, review
// strictness, role model and retry ladder — every one of them a question about
// OUTCOMES ("did the patch apply", "was the review right"). A 16-token
// synthetic completion is evidence about none of them, and evolve.DefaultPriors
// already encodes what is genuinely known about small models up front. Seeding
// a Beta posterior from a latency probe would be inventing evidence, which is
// worse than the uniform prior it would replace.

func itoa(n int) string { return fmt.Sprintf("%d", n) }
