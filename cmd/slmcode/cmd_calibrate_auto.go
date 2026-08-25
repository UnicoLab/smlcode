package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/calibrate"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/memory"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// Startup calibration for `slmcode run`.
//
// Everything here is best-effort by construction: a failure prints one warning
// and the run proceeds on the static defaults. Nothing below can return an
// error that stops a run, and nothing runs unless the endpoint probe has
// already confirmed the server is up — so "calibration failed" never means
// "your server is down", which the probe reports far better.

// maxParallelNoticeOnce keeps the endpoint-aware default's explanation to ONE
// line per process. It is printed by the run banner and by doctor; a wave loop
// must never repeat it.
var maxParallelNoticeOnce sync.Once

// printMaxParallelNotice explains a lowered max_parallel default, once.
// Silent when the default was not lowered — an explicit setting, a hosted
// endpoint, or a calibration that already spoke for itself.
func printMaxParallelNotice(cfg *config.Config) {
	notice := cfg.MaxParallelNotice()
	if notice == "" {
		return
	}
	maxParallelNoticeOnce.Do(func() { fmt.Println(cli.Dim("  " + notice)) })
}

// suppressMaxParallelNotice consumes the one-line budget without spending it,
// for the case where something better has already explained the number.
func suppressMaxParallelNotice() { maxParallelNoticeOnce.Do(func() {}) }

// autoCalibrate measures the configured pair when it is unseen, applies what
// it learned to h.Config, seeds the stores that would otherwise start cold,
// and rebuilds the engine if anything changed.
//
// Order matters. max_parallel and task_timeout are read when the orchestrator
// is constructed, so the rebuild has to follow the apply; the role-latency
// seed has to follow the rebuild, because the rebuild opens a fresh evolve
// store that would otherwise flush over it.
func autoCalibrate(ctx context.Context, h *harness.Harness) {
	if h == nil || h.Config == nil || !h.Config.CalibrationEnabled() {
		return
	}
	store := openCalibrationStore()
	defer func() { _ = store.Close() }()

	out := calibrate.EnsureCalibrated(ctx, h.Config, calibrate.AutoOptions{Store: store})
	for _, w := range store.Warnings() {
		fmt.Println(cli.Warn(w))
	}
	if out.Warning != "" {
		fmt.Println(cli.Warn(out.Warning))
	}
	if out.Notice != "" {
		fmt.Println(cli.Info(out.Notice))
	}
	if out.Profile.MaxParallel <= 0 {
		return
	}
	// A profile is in force, so the endpoint-aware default has been superseded
	// by a real measurement of THIS machine. Printing the static explanation
	// underneath would quote a different machine's numbers at the user and read
	// as a contradiction, so consume the guard without printing.
	suppressMaxParallelNotice()

	// Decode rate: the same evidence a real completion provides, so per-call
	// deadlines stop starting from the pessimistic 12 tok/s prior.
	calibrate.SeedThroughput(backends.GlobalThroughput, out.Profile)

	if len(out.Applied) > 0 {
		if err := quietRebuild(h); err != nil && cli.CurrentLogLevel() >= cli.LogWarn {
			fmt.Println(cli.Warn("calibration applied, but the engine could not be rebuilt: " + err.Error()))
		}
	}
	seedRoleLatency(h, out.Profile)
}

// seedRoleLatency gives the role-timeout store a measured starting point, so a
// never-before-seen model is not stuck on "0/3 samples → use the whole
// ceiling" for its first runs. It never displaces real observations.
func seedRoleLatency(h *harness.Harness, prof calibrate.Profile) {
	if h == nil || h.Orchestrator == nil {
		return
	}
	st := h.Orchestrator.LatencyStore()
	if st == nil {
		return // evolve is off; nothing to seed
	}
	family := h.Orchestrator.ModelFamilyKey()
	modelProfile := config.ResolveModelProfile(h.Config.ModelProfiles, h.Config.Model)
	roles := make([]calibrate.RoleSeed, 0, len(orchestrator.SeedableRoles()))
	for _, r := range orchestrator.SeedableRoles() {
		roles = append(roles, calibrate.RoleSeed{Role: r, MaxTokens: modelProfile.MaxTokens})
	}
	ceiling := h.Config.TaskTimeout
	seeded := calibrate.SeedRoleLatency(latencySeeder{st}, family, prof, roles, memory.MinLatencySamples, ceiling)
	if len(seeded) == 0 || cli.CurrentLogLevel() < cli.LogInfo {
		return
	}
	d, ok := prof.RoleLatencySeed(modelProfile.MaxTokens)
	if !ok {
		return
	}
	if ceiling > 0 && d > ceiling {
		d = ceiling
	}
	fmt.Println(cli.Dim(fmt.Sprintf(
		"  seeded role timeouts for %s from the measurement: %s per role (%d role(s), replaced by real observations as they arrive)",
		family, d.Round(time.Second), len(seeded))))
}

// latencySeeder adapts memory.Latencies to the narrow interface pkg/calibrate
// depends on, so that package never imports the whole memory store.
type latencySeeder struct{ st *memory.Latencies }

func (l latencySeeder) Samples(role, family string) int {
	if l.st == nil {
		// No store means no evidence, but reporting "0 samples" would invite a
		// seed nobody can record. Claim evidence so the seeder stands down.
		return 1
	}
	_, n := l.st.Quantile(role, family, 0.95)
	return n
}

func (l latencySeeder) Record(role, family string, d time.Duration) {
	if l.st == nil {
		return
	}
	l.st.Record(memory.LatencyKey{Role: role, ModelFamily: family}, d)
}
