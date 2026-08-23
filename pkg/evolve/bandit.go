package evolve

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// Decision names a discrete choice the harness makes repeatedly and whose
// right answer depends on the model and the project.
type Decision string

const (
	DecEditFormat       Decision = "edit_format"
	DecRoleModel        Decision = "role_model"
	DecThinkPasses      Decision = "think_passes"
	DecExplorePhase     Decision = "explore_phase"
	DecRetryLadder      Decision = "retry_ladder"
	DecReviewStrictness Decision = "review_strictness"
)

// Bandit tuning.
const (
	// DefaultExploreRate is the initial ε. It decays with experience so a
	// mature install is nearly greedy but never fully closed to new evidence.
	DefaultExploreRate = 0.20
	// ExploreHalfLife is how many pulls halve ε.
	ExploreHalfLife = 40
	// MinExploreRate is the floor ε decays to: the world can change (a model
	// upgrade, a refactor), so a little exploration is permanent.
	MinExploreRate = 0.02
	// MinPulls is how many times each arm is tried before Thompson sampling
	// takes over. Without it a single lucky first sample can lock in an arm.
	MinPulls = 2
	// DecayAfter is the total pull count at which a key's posterior is scaled
	// down, keeping it responsive to change and bounding the numbers on disk.
	DecayAfter = 200
	// DecayFactor is how much of the posterior survives a decay.
	DecayFactor = 0.5
	// MaxBanditKeys bounds the store.
	MaxBanditKeys = 300
)

// Key identifies one decision in one context. Model family and language are
// part of the key because the right answer genuinely differs across them:
// what a 7B Qwen can do with a unified diff is not what GPT-4o can.
type Key struct {
	Decision    Decision `json:"decision"`
	ModelFamily string   `json:"model_family,omitempty"`
	Language    string   `json:"language,omitempty"`
}

// Normalize lowercases and folds aliases.
func (k Key) Normalize() Key {
	k.Decision = Decision(strings.ToLower(strings.TrimSpace(string(k.Decision))))
	k.ModelFamily = strings.ToLower(strings.TrimSpace(k.ModelFamily))
	k.Language = memory.NormalizeLanguage(k.Language)
	return k
}

// String is the on-disk map key and the human label.
func (k Key) String() string {
	k = k.Normalize()
	return string(k.Decision) + "|" + orStar(k.ModelFamily) + "|" + orStar(k.Language)
}

func orStar(s string) string {
	if s == "" {
		return "*"
	}
	return s
}

// Arm is one option under a key, with its Beta posterior.
type Arm struct {
	Name     string    `json:"name"`
	Alpha    float64   `json:"alpha"`
	Beta     float64   `json:"beta"`
	Pulls    int       `json:"pulls"`
	Reward   float64   `json:"reward_total"`
	LastUsed time.Time `json:"last_used,omitempty"`
}

// Mean is the posterior mean reward.
func (a Arm) Mean() float64 {
	if a.Alpha+a.Beta <= 0 {
		return 0.5
	}
	return a.Alpha / (a.Alpha + a.Beta)
}

// StdDev is the Beta posterior standard deviation — how unsure we still are.
func (a Arm) StdDev() float64 {
	n := a.Alpha + a.Beta
	if n <= 0 {
		return 0.5
	}
	return math.Sqrt(a.Alpha * a.Beta / (n * n * (n + 1)))
}

type banditKeyState struct {
	Key   Key             `json:"key"`
	Arms  map[string]*Arm `json:"arms"`
	Pulls int             `json:"pulls"`
}

type banditFile struct {
	Version int                        `json:"version"`
	Updated time.Time                  `json:"updated"`
	Keys    map[string]*banditKeyState `json:"keys"`
}

// Prior is a warm start: what we believe about an arm before any evidence.
type Prior struct {
	Alpha float64
	Beta  float64
}

// BanditOptions configures a Bandit.
type BanditOptions struct {
	// Deterministic replaces Thompson sampling with a greedy argmax over the
	// posterior means and disables ε-exploration. Use it in CI and for
	// reproducible runs (`--no-explore`).
	Deterministic bool
	// ExploreRate overrides DefaultExploreRate.
	ExploreRate float64
	// Seed makes the sampler reproducible while still exploring.
	Seed int64
	// Now is injectable for tests.
	Now func() time.Time
	// Priors warm-starts specific (key, arm) pairs. DefaultPriors() is applied
	// first and these override it.
	Priors map[string]map[string]Prior
}

// Bandit is a contextual multi-armed bandit over harness choices.
//
// Algorithm: Thompson sampling over Beta posteriors, with an explicit decaying
// ε and a forced minimum number of pulls per arm.
//
// Why Thompson sampling rather than UCB1:
//
//   - The reward is naturally a bounded [0,1] score, which makes Beta the
//     conjugate prior — an O(1) update and two floats of state per arm, both
//     of which are legible in the JSON a user is invited to read.
//   - The sample counts here are tiny. One developer on one project produces
//     tens of observations per arm, not thousands. UCB1's confidence bound is
//     only meaningful once every arm has been pulled, and in the low-n regime
//     it over-explores badly — which for this harness means deliberately using
//     an edit format you already know applies 60% of the time.
//   - Warm starting is exactly expressible: a prior IS "pretend we already saw
//     α successes and β failures", so shipped defaults and learned evidence
//     live on the same scale and compose without special cases.
//   - Determinism is a one-line change (argmax of the posterior mean), which
//     matters because CI must be reproducible.
//
// Guardrails against locking in a bad arm from an unlucky start:
//   - prior pseudo-counts are never removed, so no arm's posterior can be
//     driven to certainty by a handful of samples;
//   - every arm must be pulled MinPulls times before sampling takes over;
//   - ε never decays below MinExploreRate;
//   - a key's posterior is decayed once it passes DecayAfter pulls, so a
//     changed world can be relearned instead of being outvoted by history.
type Bandit struct {
	mu       sync.Mutex
	path     string
	keys     map[string]*banditKeyState
	rng      *rand.Rand
	opt      BanditOptions
	now      func() time.Time
	dirty    bool
	warnings []string
}

// OpenBanditWith loads the policy store from dir (normally the USER dir:
// what works for a model transfers between projects — pass a project directory
// instead to keep a project-local policy, e.g. for CI).
func OpenBanditWith(dir string, opt BanditOptions) (*Bandit, error) {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	if opt.ExploreRate <= 0 {
		opt.ExploreRate = DefaultExploreRate
	}
	seed := opt.Seed
	if seed == 0 {
		seed = now().UnixNano()
	}
	b := &Bandit{
		keys: map[string]*banditKeyState{},
		rng:  rand.New(rand.NewSource(seed)), //nolint:gosec // policy exploration, not cryptography
		opt:  opt,
		now:  now,
	}
	if dir != "" {
		b.path = filepath.Join(dir, memory.SlmDirName, EvolveDirName, "policy.json")
		b.load()
	}
	return b, nil
}

func (b *Bandit) load() {
	data, err := os.ReadFile(b.path) //nolint:gosec // path derived from the caller's own state dir
	if err != nil {
		return
	}
	var bf banditFile
	if err := json.Unmarshal(data, &bf); err != nil {
		b.warnings = append(b.warnings, "policy.json unreadable; starting from priors")
		_ = os.Rename(b.path, b.path+".corrupt")
		return
	}
	for name, st := range bf.Keys {
		if st == nil || st.Arms == nil {
			continue
		}
		for armName, arm := range st.Arms {
			if arm == nil || arm.Alpha <= 0 || arm.Beta <= 0 {
				delete(st.Arms, armName)
				continue
			}
			arm.Name = armName
		}
		st.Key = st.Key.Normalize()
		b.keys[name] = st
	}
}

// DefaultPriors are the shipped warm starts. They encode what is already known
// about small models so a fresh install behaves sensibly on run one instead of
// exploring its way into a bad edit format.
//
// The numbers are deliberately modest (a few pseudo-observations): they steer
// the first handful of runs and are then swamped by real evidence.
func DefaultPriors() map[string]map[string]Prior {
	return map[string]map[string]Prior{
		string(DecEditFormat): {
			// Search/replace is by far the most reliable format for 7B–32B
			// models; unified diff is the classic failure mode; whole-file is
			// safe but expensive, so it is a fallback, not a default.
			"search_replace": {Alpha: 8, Beta: 2},
			"unified_diff":   {Alpha: 3, Beta: 5},
			"whole_file":     {Alpha: 4, Beta: 4},
		},
		string(DecThinkPasses): {
			"1": {Alpha: 5, Beta: 3},
			"2": {Alpha: 5, Beta: 4},
			"3": {Alpha: 3, Beta: 5},
		},
		string(DecExplorePhase): {
			"on":  {Alpha: 6, Beta: 3},
			"off": {Alpha: 4, Beta: 4},
		},
		string(DecReviewStrictness): {
			"normal": {Alpha: 6, Beta: 3},
			"strict": {Alpha: 4, Beta: 4},
			"lenient": {
				Alpha: 3, Beta: 5,
			},
		},
		string(DecRoleModel): {
			"fast":  {Alpha: 5, Beta: 4},
			"heavy": {Alpha: 5, Beta: 4},
		},
		string(DecRetryLadder): {
			"reread_then_shrink": {Alpha: 6, Beta: 3},
			"escalate_model":     {Alpha: 3, Beta: 5},
		},
	}
}

func (b *Bandit) priorFor(k Key, arm string) Prior {
	if p, ok := b.opt.Priors[string(k.Decision)][arm]; ok && p.Alpha > 0 && p.Beta > 0 {
		return p
	}
	if p, ok := DefaultPriors()[string(k.Decision)][arm]; ok && p.Alpha > 0 && p.Beta > 0 {
		return p
	}
	return Prior{Alpha: 1, Beta: 1} // uniform: no opinion
}

func (b *Bandit) stateLocked(k Key, arms []string) *banditKeyState {
	k = k.Normalize()
	name := k.String()
	st, ok := b.keys[name]
	if !ok {
		st = &banditKeyState{Key: k, Arms: map[string]*Arm{}}
		b.keys[name] = st
		b.dirty = true
	}
	for _, arm := range arms {
		arm = strings.TrimSpace(arm)
		if arm == "" {
			continue
		}
		if _, ok := st.Arms[arm]; !ok {
			p := b.priorFor(k, arm)
			st.Arms[arm] = &Arm{Name: arm, Alpha: p.Alpha, Beta: p.Beta}
			b.dirty = true
		}
	}
	return st
}

// Choice is the outcome of a Choose call, with the reasoning attached.
type Choice struct {
	Key    Key
	Arm    string
	Reason string
	// Explore is true when the arm was picked to gather information rather
	// than because it is currently believed best.
	Explore bool
	Mean    float64
	Pulls   int
}

// Choose picks an arm. arms must be non-empty; the first arm is the fallback
// when the list is degenerate.
func (b *Bandit) Choose(k Key, arms []string) string {
	return b.ChooseWithReason(k, arms).Arm
}

// ChooseWithReason is Choose with an explanation of why.
func (b *Bandit) ChooseWithReason(k Key, arms []string) Choice {
	arms = uniqueNonEmpty(arms)
	if len(arms) == 0 {
		return Choice{Key: k.Normalize(), Reason: "no arms offered"}
	}
	if len(arms) == 1 {
		return Choice{Key: k.Normalize(), Arm: arms[0], Reason: "only one option"}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateLocked(k, arms)

	// Guardrail 1: every arm gets MinPulls before belief takes over, so an
	// unlucky first sample cannot lock the harness onto a bad arm forever.
	if !b.opt.Deterministic {
		least, leastPulls := "", math.MaxInt32
		for _, name := range arms {
			if p := st.Arms[name].Pulls; p < MinPulls && p < leastPulls {
				least, leastPulls = name, p
			}
		}
		if least != "" {
			return Choice{
				Key: st.Key, Arm: least, Explore: true,
				Reason: fmt.Sprintf("warm-up: %s has %d/%d required samples", least, leastPulls, MinPulls),
				Mean:   st.Arms[least].Mean(), Pulls: leastPulls,
			}
		}
	}

	// Guardrail 2: an explicit, decaying exploration rate.
	if !b.opt.Deterministic {
		eps := b.exploreRate(st.Pulls)
		if b.rng.Float64() < eps {
			arm := arms[b.rng.Intn(len(arms))]
			return Choice{
				Key: st.Key, Arm: arm, Explore: true,
				Reason: fmt.Sprintf("exploring (ε=%.2f after %d pulls)", eps, st.Pulls),
				Mean:   st.Arms[arm].Mean(), Pulls: st.Arms[arm].Pulls,
			}
		}
	}

	best, bestScore := "", math.Inf(-1)
	for _, name := range arms {
		arm := st.Arms[name]
		score := arm.Mean()
		if !b.opt.Deterministic {
			score = sampleBeta(b.rng, arm.Alpha, arm.Beta)
		}
		if score > bestScore || (score == bestScore && name < best) {
			best, bestScore = name, score
		}
	}
	reason := fmt.Sprintf("Thompson sample favored %s (posterior mean %.0f%% over %d pulls)",
		best, st.Arms[best].Mean()*100, st.Arms[best].Pulls)
	if b.opt.Deterministic {
		reason = fmt.Sprintf("deterministic mode: highest posterior mean %.0f%% over %d pulls",
			st.Arms[best].Mean()*100, st.Arms[best].Pulls)
	}
	return Choice{
		Key: st.Key, Arm: best, Reason: reason,
		Mean: st.Arms[best].Mean(), Pulls: st.Arms[best].Pulls,
	}
}

func (b *Bandit) exploreRate(pulls int) float64 {
	eps := b.opt.ExploreRate / (1 + float64(pulls)/ExploreHalfLife)
	if eps < MinExploreRate {
		return MinExploreRate
	}
	return eps
}

// Outcome is the real-world result of a choice; Reward turns it into [0,1].
type Outcome struct {
	// Applied: did the edit/tool call land cleanly?
	Applied bool
	// GateRan / GatePassed: did the quality gate run, and did it pass?
	GateRan    bool
	GatePassed bool
	// Retries spent recovering.
	Retries int
	// TokensUsed against TokenBudget, WallMS against WallBudgetMS. Zero
	// budgets mean "unknown", which scores as neutral rather than punishing.
	TokensUsed   int
	TokenBudget  int
	WallMS       int64
	WallBudgetMS int64
	// Failed marks a hard failure that no amount of efficiency redeems.
	Failed bool
}

// Reward maps an outcome to [0,1].
//
//	correctness = 0.60·applied + 0.25·gate + 0.15·(1 − min(retries,3)/3)
//	cost        = 0.50·token_efficiency + 0.50·time_efficiency
//	reward      = 0.85·correctness + 0.15·cost
//	hard failure ⇒ reward is capped at 0.10
//
// Correctness outweighs cost roughly six to one on purpose. The harness must
// never learn to prefer a cheaper option that produces broken code: tokens are
// recoverable, a wrong edit is not. Cost is present at all only to break ties
// between options that work equally well.
func (o Outcome) Reward() float64 {
	b := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	retries := float64(o.Retries)
	if retries > 3 {
		retries = 3
	}
	gate := 0.5 // unknown gate is neutral
	if o.GateRan {
		gate = b(o.GatePassed)
	}
	correctness := 0.60*b(o.Applied) + 0.25*gate + 0.15*(1-retries/3)
	cost := 0.5*efficiency(float64(o.TokensUsed), float64(o.TokenBudget)) +
		0.5*efficiency(float64(o.WallMS), float64(o.WallBudgetMS))
	r := 0.85*correctness + 0.15*cost
	if o.Failed {
		r = math.Min(r, 0.10)
	}
	return clamp01(r)
}

func efficiency(used, budget float64) float64 {
	if budget <= 0 || used <= 0 {
		return 0.5 // unknown: neutral, never a bonus and never a penalty
	}
	return clamp01(1 - used/budget)
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// Update folds an outcome into the posterior for (key, arm).
func (b *Bandit) Update(k Key, arm string, o Outcome) {
	b.UpdateReward(k, arm, o.Reward())
}

// UpdateReward folds a raw [0,1] reward in directly.
func (b *Bandit) UpdateReward(k Key, arm string, reward float64) {
	arm = strings.TrimSpace(arm)
	if arm == "" {
		return
	}
	reward = clamp01(reward)
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateLocked(k, []string{arm})
	a := st.Arms[arm]
	// Bernoulli-ized Beta update: for a bounded reward this keeps the
	// posterior mean an unbiased estimate of the expected reward.
	a.Alpha += reward
	a.Beta += 1 - reward
	a.Pulls++
	a.Reward += reward
	a.LastUsed = b.now().UTC()
	st.Pulls++
	b.dirty = true

	// Guardrail 4: keep the posterior responsive and the numbers bounded.
	if st.Pulls > 0 && st.Pulls%DecayAfter == 0 {
		for _, arm := range st.Arms {
			p := b.priorFor(st.Key, arm.Name)
			arm.Alpha = p.Alpha + (arm.Alpha-p.Alpha)*DecayFactor
			arm.Beta = p.Beta + (arm.Beta-p.Beta)*DecayFactor
		}
	}
}

// KeyStats is a snapshot of one key for inspection.
type KeyStats struct {
	Key   Key   `json:"key"`
	Pulls int   `json:"pulls"`
	Arms  []Arm `json:"arms"`
}

// Snapshot returns every key's state, sorted for stable output.
func (b *Bandit) Snapshot() []KeyStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]KeyStats, 0, len(b.keys))
	for _, name := range sortedMapKeys(b.keys) {
		st := b.keys[name]
		ks := KeyStats{Key: st.Key, Pulls: st.Pulls}
		for _, armName := range sortedMapKeys(st.Arms) {
			ks.Arms = append(ks.Arms, *st.Arms[armName])
		}
		sort.SliceStable(ks.Arms, func(i, j int) bool { return ks.Arms[i].Mean() > ks.Arms[j].Mean() })
		out = append(out, ks)
	}
	return out
}

// Why explains the current state of a decision in plain language, so a user
// can see WHY the harness picked what it picked.
func (b *Bandit) Why(k Key) string {
	k = k.Normalize()
	b.mu.Lock()
	st, ok := b.keys[k.String()]
	if !ok {
		b.mu.Unlock()
		return fmt.Sprintf("%s: no evidence yet — using the shipped defaults.", k.Decision)
	}
	arms := make([]Arm, 0, len(st.Arms))
	for _, name := range sortedMapKeys(st.Arms) {
		arms = append(arms, *st.Arms[name])
	}
	pulls := st.Pulls
	eps := b.exploreRate(pulls)
	deterministic := b.opt.Deterministic
	b.mu.Unlock()

	sort.SliceStable(arms, func(i, j int) bool { return arms[i].Mean() > arms[j].Mean() })
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (model %s, language %s) — %d observations\n",
		k.Decision, orStar(k.ModelFamily), orStar(k.Language), pulls)
	for i, a := range arms {
		marker := "  "
		if i == 0 {
			marker = "→ "
		}
		fmt.Fprintf(&sb, "%s%-20s %.0f%% ±%.0f%%  (%d pulls, α=%.1f β=%.1f)\n",
			marker, a.Name, a.Mean()*100, a.StdDev()*100, a.Pulls, a.Alpha, a.Beta)
	}
	if deterministic {
		sb.WriteString("mode: deterministic (no exploration)\n")
	} else {
		fmt.Fprintf(&sb, "mode: Thompson sampling, ε=%.2f\n", eps)
	}
	return sb.String()
}

// Save persists the policy.
func (b *Bandit) Save() error {
	b.mu.Lock()
	if b.path == "" || !b.dirty {
		b.mu.Unlock()
		return nil
	}
	bf := banditFile{Version: 1, Updated: b.now().UTC(), Keys: map[string]*banditKeyState{}}
	for name, st := range b.keys {
		bf.Keys[name] = st
	}
	b.dirty = false
	b.mu.Unlock()

	data, err := json.MarshalIndent(bf, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(b.path, append(data, '\n'), 0o600)
}

// Prune bounds the store, dropping the least-used keys first.
func (b *Bandit) Prune(maxKeys int) int {
	if maxKeys <= 0 {
		maxKeys = MaxBanditKeys
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.keys) <= maxKeys {
		return 0
	}
	names := sortedMapKeys(b.keys)
	sort.SliceStable(names, func(i, j int) bool { return b.keys[names[i]].Pulls > b.keys[names[j]].Pulls })
	removed := 0
	for _, name := range names[maxKeys:] {
		delete(b.keys, name)
		removed++
	}
	b.dirty = true
	return removed
}

// Forget deletes the policy store and resets to priors.
func (b *Bandit) Forget() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keys = map[string]*banditKeyState{}
	b.dirty = false
	if b.path == "" {
		return nil
	}
	if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Warnings returns non-fatal load problems.
func (b *Bandit) Warnings() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.warnings...)
}

// sampleBeta draws from Beta(α, β) using the ratio of two Gamma draws.
func sampleBeta(rng *rand.Rand, alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return rng.Float64()
	}
	x := sampleGamma(rng, alpha)
	y := sampleGamma(rng, beta)
	if x+y <= 0 {
		return 0.5
	}
	return x / (x + y)
}

// sampleGamma draws from Gamma(shape, 1) with Marsaglia–Tsang.
func sampleGamma(rng *rand.Rand, shape float64) float64 {
	if shape < 1 {
		u := rng.Float64()
		if u <= 0 {
			u = 1e-12
		}
		return sampleGamma(rng, shape+1) * math.Pow(u, 1/shape)
	}
	d := shape - 1.0/3.0
	c := 1 / math.Sqrt(9*d)
	for i := 0; i < 1000; i++ {
		x := rng.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rng.Float64()
		if u < 1-0.0331*x*x*x*x {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
	return d // pathological RNG: fall back to the mean
}

func uniqueNonEmpty(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
