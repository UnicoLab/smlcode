package calibrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Key identifies one calibrated pair.
//
// The model is the EXACT id, never the folded family pkg/memory uses. Folding
// is right for behavioral lessons and for per-role latency — a lesson about
// edit formats partly generalizes across Qwen3.8 builds. It is wrong here: a
// 27B and a 9B of the same family have different knees, different latencies
// and different context windows, so merging them would produce confidently
// wrong numbers for whichever one was measured second.
//
// The endpoint is reduced to its IDENTITY — scheme, host, port — so it is
// stable, non-secret, and distinguishes two servers on the same box by port.
// No API key, path or query ever reaches the store.
type Key struct {
	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
	// Provider is recorded for display only; it is not part of the identity,
	// because renaming `mlx` to `omlx` does not change what the server does.
	Provider string `json:"provider,omitempty"`
}

// Normalize trims and canonicalizes the key fields.
func (k Key) Normalize() Key {
	k.Model = strings.TrimSpace(k.Model)
	k.Endpoint = EndpointIdentity(k.Endpoint)
	k.Provider = strings.ToLower(strings.TrimSpace(k.Provider))
	return k
}

// ID is the stable identity of a calibrated pair.
func (k Key) ID() string {
	k = k.Normalize()
	sum := sha256.Sum256([]byte(strings.ToLower(k.Model) + "\x00" + k.Endpoint))
	return "c_" + hex.EncodeToString(sum[:8])
}

func (k Key) String() string {
	k = k.Normalize()
	if k.Endpoint == "" {
		return k.Model
	}
	return k.Model + " @ " + k.Endpoint
}

// EndpointIdentity reduces a base URL to scheme://host[:port].
//
// Path, query and any userinfo are dropped: the first is routing detail, and
// the other two can carry credentials that must never land in a stored profile
// or a log line.
func EndpointIdentity(raw string) string {
	ep := strings.TrimSpace(raw)
	if ep == "" {
		return ""
	}
	if !strings.Contains(ep, "://") {
		ep = "http://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(u.Host)) // host:port, no userinfo
	if host == "" {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + host
}

// Profile is what one calibration concluded about one pair.
//
// Every field records something MEASURED or something the server REPORTED.
// Version says which generation of the probe produced it, so a later slmcode
// that measures more can re-probe a profile that predates the new field
// instead of silently missing it.
type Profile struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Key     Key    `json:"key"`

	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
	Provider string `json:"provider,omitempty"`

	// ── concurrency ──
	// MaxParallel is the chosen knee.
	MaxParallel int `json:"max_parallel"`
	// Levels is the evidence behind it, so the number is inspectable rather
	// than magic (`slmcode calibrate --show`).
	Levels []Level `json:"levels,omitempty"`
	// FloorUsed is the efficiency floor the knee was selected against.
	FloorUsed float64 `json:"efficiency_floor,omitempty"`
	// QueueInflation is per-request latency at the knee ÷ solo latency: what a
	// wall-clock role budget has to absorb for running at MaxParallel.
	QueueInflation float64 `json:"queue_inflation,omitempty"`

	// ── latency + throughput baseline (solo, DefaultMaxTokens completion) ──
	P50Ms            int64   `json:"p50_ms,omitempty"`
	P95Ms            int64   `json:"p95_ms,omitempty"`
	SoloSamples      int     `json:"solo_samples,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	TokensPerSec     float64 `json:"tokens_per_sec,omitempty"`

	// ── server-reported model metadata ──
	ContextLimit  int    `json:"context_limit,omitempty"`
	ContextSource string `json:"context_source,omitempty"`

	MeasuredAt time.Time `json:"measured_at"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	// Partial is set when the probe ran out of budget or lost a level, so the
	// numbers are usable but the knee may be an underestimate.
	Partial bool   `json:"partial,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Age is how long ago this profile was measured.
func (p Profile) Age(now time.Time) time.Duration {
	if p.MeasuredAt.IsZero() {
		return 0
	}
	return now.Sub(p.MeasuredAt)
}

// Current reports whether the profile was produced by this calibrator and is
// still within ttl. A profile from an older generation is not "wrong", it is
// INCOMPLETE — re-probing is cheap and missing a field silently is not.
func (p Profile) Current(now time.Time, ttl time.Duration) bool {
	if p.ID == "" || p.MaxParallel <= 0 {
		return false
	}
	if p.Version < CalibratorVersion {
		return false
	}
	// A PARTIAL profile expires fast, and the reason is what it usually means.
	//
	// The measurement runs inside a fixed wall-clock budget whose every unit of
	// work is a model call — the thing being measured. On a cold local server
	// the warm-up and the solo baseline can eat the whole budget before any
	// concurrency level is measured, leaving only the synthetic
	// {Concurrency:1, Efficiency:1} entry, from which SelectKnee returns 1.
	//
	// That verdict was then honored for the full DefaultTTL, because this
	// function checked ID, MaxParallel, Version and age but never Partial. So
	// the SLOWEST models — the ones the measurement exists to serve — were
	// silently pinned to max_parallel=1 for a month on the strength of one cold
	// start, with nothing to notice: Apply only checks MaxParallel > 0, and the
	// "partial" marker appears solely in Summary(), which the auto path never
	// prints.
	//
	// A cold start is transient, so the retry should be too: an hour later the
	// weights are resident and the same probe measures a real knee.
	if p.Partial && p.Age(now) > PartialTTL {
		return false
	}
	if ttl > 0 && p.Age(now) > ttl {
		return false
	}
	return true
}

// StaleAgainst is the cheap validity check: a profile is stale when the server
// no longer reports the context window it was measured with. Anything else
// about the pair is already pinned by the key.
//
// A metadata call that fails or reports nothing is NOT evidence of staleness —
// an offline `/v1/models` must never trigger a re-probe storm.
func (p Profile) StaleAgainst(md Metadata) bool {
	if md.ContextLimit <= 0 || p.ContextLimit <= 0 {
		return false
	}
	return md.ContextLimit != p.ContextLimit
}

// RecommendedTaskTimeout is the task_timeout this pair actually needs.
//
//	measured decode time for a full role response
//	  × the queueing inflation of running at the chosen knee
//	  × the same 3/2 safety factor roleTimeout uses
//
// roleMaxTokens is the role's completion budget (config.ModelProfile.MaxTokens).
// Zero tokens/sec means the server reported no usage and nothing can be
// derived — the caller keeps its configured value.
//
// Rounded up to a readable 30s step, and never below 2 minutes: a
// recommendation that reads "1m10s" invites setting a budget with no headroom.
func (p Profile) RecommendedTaskTimeout(roleMaxTokens int) (time.Duration, bool) {
	if p.TokensPerSec <= 0 || roleMaxTokens <= 0 {
		return 0, false
	}
	inflation := p.QueueInflation
	if inflation < 1 {
		inflation = 1
	}
	decode := float64(roleMaxTokens) / p.TokensPerSec * inflation
	// Prefill + connect, measured: the part of a solo call that is not decode.
	overhead := float64(p.P95Ms) / 1000
	// 3/2 matches roleTimeoutSafetyNum/Den, so a recommendation and the budget
	// derived from it carry the same headroom rather than two different ones.
	want := roundUpTo(time.Duration((decode+overhead)*1.5*float64(time.Second)), 30*time.Second)
	if want < 2*time.Minute {
		want = 2 * time.Minute
	}
	return want, true
}

// SeedTurnAllowance is how many full-length model calls one role phase is
// assumed to make.
//
// A role is not one completion: the inner loop reads files, calls tools and
// re-prompts, and the duration the timeout policy measures covers all of it.
// A seed that modeled a single call would UNDER-estimate a multi-turn role,
// and an under-estimated budget is the exact failure this whole change exists
// to remove. Four is deliberately generous — over-estimating costs at most one
// slow role, under-estimating costs a failed run.
const SeedTurnAllowance = 4

// RoleLatencySeed is the duration to seed the role-latency store with for a
// role whose per-call completion budget is roleMaxTokens.
//
// This is a DERIVED estimate, and the derivation is the whole point.
// pkg/orchestrator/roletimeout.go refuses to seed from backends.Probe, and it
// is right to: that probe requests max_tokens=1, so what it measures is
// connect + prefill, "two orders of magnitude apart, and not proportional" to
// a role call. This estimate IS proportional — measured tokens/sec times the
// role's own token budget, plus measured overhead, times measured queueing
// inflation, times a turn allowance. It is biased HIGH on purpose, and it
// decays: the store keeps 32 samples per key, so real observations displace it
// within a run or two of real work.
//
// Callers must cap it at their timeout ceiling (SeedRoleLatency does), so a
// slow model's seed degrades to exactly today's cold-start behavior — the
// full budget — rather than claiming something the harness would never grant.
//
// Returns false when throughput was not measured, in which case no seed is
// better than a fabricated one.
func (p Profile) RoleLatencySeed(roleMaxTokens int) (time.Duration, bool) {
	if p.TokensPerSec <= 0 || roleMaxTokens <= 0 {
		return 0, false
	}
	inflation := p.QueueInflation
	if inflation < 1 {
		inflation = 1
	}
	perCall := float64(roleMaxTokens)/p.TokensPerSec*inflation + float64(p.P95Ms)/1000
	d := time.Duration(perCall * SeedTurnAllowance * float64(time.Second))
	if d <= 0 {
		return 0, false
	}
	return d, true
}

// Summary is the single line the harness prints after calibrating.
func (p Profile) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "concurrency knee %d", p.MaxParallel)
	if l, ok := p.levelAbove(p.MaxParallel); ok {
		fmt.Fprintf(&b, " (%d-way runs at %.0f%% efficiency)", l.Concurrency, l.Efficiency*100)
	}
	if p.P95Ms > 0 {
		fmt.Fprintf(&b, ", p95 %s", (time.Duration(p.P95Ms) * time.Millisecond).Round(100*time.Millisecond))
	}
	if p.TokensPerSec > 0 {
		fmt.Fprintf(&b, ", %.0f tok/s", p.TokensPerSec)
	}
	if p.ContextLimit > 0 {
		fmt.Fprintf(&b, ", ctx %d", p.ContextLimit)
	}
	if p.Partial {
		b.WriteString(", partial")
	}
	return b.String()
}

// levelAbove returns the first measured level above n — the one that was
// rejected, which is what makes the chosen knee legible.
func (p Profile) levelAbove(n int) (Level, bool) {
	best := Level{}
	found := false
	for _, l := range p.Levels {
		if l.Concurrency <= n {
			continue
		}
		if !found || l.Concurrency < best.Concurrency {
			best = l
			found = true
		}
	}
	return best, found
}

func roundUpTo(d, unit time.Duration) time.Duration {
	if unit <= 0 || d <= 0 {
		return d
	}
	if r := d % unit; r != 0 {
		d += unit - r
	}
	return d
}
