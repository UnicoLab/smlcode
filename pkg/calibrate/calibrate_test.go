package calibrate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProber replays a scripted timing table. No test in this package ever
// touches the network: `make check` must never hit a model.
//
// Which level a call belongs to is derived from its INDEX, not from an
// in-flight counter. Calibrate's call sequence is fixed — one warm-up, then
// SoloSamples solo calls, then one group per concurrency level — so the index
// identifies the phase exactly, while an in-flight counter would race: the
// first goroutine of a 4-way level observes an in-flight count of 1 and would
// be handed the solo duration.
type fakeProber struct {
	mu sync.Mutex
	// perLevel maps a concurrency level to the duration each of its calls
	// takes. Missing levels fall back to solo, i.e. perfect scaling.
	perLevel map[int]time.Duration
	solo     time.Duration
	tokens   int
	meta     Metadata
	metaErr  error
	failFrom int // fail every call at or after this call number (0 = never)
	// soloSamples / levels mirror the Options the test passes; zero uses the
	// package defaults.
	soloSamples int
	levels      []int

	calls    int
	inFlight int
	peak     int
}

// levelForCall maps a 1-based call index onto the concurrency level it belongs
// to. Call 1 is the warm-up and counts as solo.
func (f *fakeProber) levelForCall(n int) int {
	solo := f.soloSamples
	if solo <= 0 {
		solo = DefaultSoloSamples
	}
	levels := f.levels
	if len(levels) == 0 {
		levels = DefaultLevels
	}
	if n <= 1+solo {
		return 1
	}
	remaining := n - (1 + solo)
	for _, l := range levels {
		if l <= 1 {
			continue // level 1 reuses the solo baseline; no calls are issued
		}
		if remaining <= l {
			return l
		}
		remaining -= l
	}
	return levels[len(levels)-1]
}

func (f *fakeProber) Complete(ctx context.Context) (Sample, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.inFlight++
	if f.inFlight > f.peak {
		f.peak = f.inFlight
	}
	d := f.solo
	if v, ok := f.perLevel[f.levelForCall(n)]; ok {
		d = v
	}
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	if f.failFrom > 0 && n >= f.failFrom {
		return Sample{}, errors.New("synthetic failure")
	}
	// Real sleeping keeps the wall-clock arithmetic honest. The durations are
	// chosen with enough margin that goroutine scheduling jitter on a loaded
	// machine cannot move a level across the efficiency floor.
	time.Sleep(d)
	return Sample{Elapsed: d, CompletionTokens: f.tokens}, nil
}

func (f *fakeProber) Metadata(ctx context.Context) (Metadata, error) {
	if f.metaErr != nil {
		return Metadata{}, f.metaErr
	}
	return f.meta, nil
}

func levels(pairs ...[2]float64) []Level {
	out := make([]Level, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, Level{Concurrency: int(p[0]), Efficiency: p[1]})
	}
	return out
}

func TestSelectKneeFromSyntheticTables(t *testing.T) {
	tests := []struct {
		name   string
		levels []Level
		floor  float64
		want   int
	}{
		{
			// The measured single-endpoint oMLX curve (9B and 27B agree).
			name:   "single local endpoint knees at 2",
			levels: levels([2]float64{1, 1.00}, [2]float64{2, 0.68}, [2]float64{4, 0.39}),
			want:   2,
		},
		{
			// The case that proves 2 is not hardcoded: a server that really
			// scales must be allowed to use every worker it was offered.
			name:   "a server that genuinely scales keeps climbing",
			levels: levels([2]float64{1, 1.00}, [2]float64{2, 0.95}, [2]float64{4, 0.92}, [2]float64{8, 0.85}),
			want:   8,
		},
		{
			name:   "a strictly serial server stays at 1",
			levels: levels([2]float64{1, 1.00}, [2]float64{2, 0.50}, [2]float64{4, 0.25}),
			want:   1,
		},
		{
			name:   "a curve that dips then recovers stops at the dip",
			levels: levels([2]float64{1, 1.00}, [2]float64{2, 0.55}, [2]float64{4, 0.90}),
			want:   1,
		},
		{
			name:   "exactly at the floor still counts",
			levels: levels([2]float64{1, 1.00}, [2]float64{2, 0.60}, [2]float64{4, 0.59}),
			want:   2,
		},
		{
			name:   "a stricter floor selects a lower knee",
			levels: levels([2]float64{1, 1.00}, [2]float64{2, 0.68}, [2]float64{4, 0.39}),
			floor:  0.80,
			want:   1,
		},
		{
			name:   "a looser floor accepts a level the default rejects",
			levels: levels([2]float64{1, 1.00}, [2]float64{2, 0.68}, [2]float64{4, 0.39}),
			floor:  0.30,
			want:   4,
		},
		{
			name:   "no evidence is one worker",
			levels: nil,
			want:   1,
		},
		{
			name:   "unsorted input is handled",
			levels: levels([2]float64{4, 0.39}, [2]float64{1, 1.00}, [2]float64{2, 0.68}),
			want:   2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelectKnee(tc.levels, tc.floor); got != tc.want {
				t.Fatalf("SelectKnee = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSelectKneeIsDeterministic(t *testing.T) {
	table := levels([2]float64{1, 1.00}, [2]float64{2, 0.68}, [2]float64{4, 0.39})
	first := SelectKnee(table, 0)
	for i := 0; i < 50; i++ {
		if got := SelectKnee(table, 0); got != first {
			t.Fatalf("run %d gave %d, want %d — selection must be pure", i, got, first)
		}
	}
}

func TestCalibrateReproducesTheMeasuredLocalKnee(t *testing.T) {
	// Replay of the real oMLX shape at a scale where scheduling jitter cannot
	// move a level across the 60% floor: 2-way needs wall <= 66ms to pass and
	// takes 50ms; 4-way needs wall > 66ms to fail and takes 110ms.
	p := &fakeProber{
		solo:     40 * time.Millisecond,
		perLevel: map[int]time.Duration{2: 50 * time.Millisecond, 4: 110 * time.Millisecond},
		tokens:   16,
		meta:     Metadata{ContextLimit: 262144, Source: "GET /v1/models max_model_len"},
	}
	prof, err := Calibrate(context.Background(), p, Key{Model: "Qwen3.8-27B-4bit", Endpoint: "http://127.0.0.1:8000/v1"}, Options{})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if prof.MaxParallel != 2 {
		t.Fatalf("knee = %d, want 2 (levels: %+v)", prof.MaxParallel, prof.Levels)
	}
	if prof.ContextLimit != 262144 {
		t.Fatalf("context limit = %d, want the server-reported 262144", prof.ContextLimit)
	}
	if prof.TokensPerSec <= 0 {
		t.Fatal("throughput must be derived from the reported completion tokens")
	}
	if prof.Version != CalibratorVersion {
		t.Fatalf("version = %d, want %d", prof.Version, CalibratorVersion)
	}
	if prof.Key.Endpoint != "http://127.0.0.1:8000" {
		t.Fatalf("endpoint identity = %q — path must be stripped", prof.Key.Endpoint)
	}
}

func TestCalibrateStopsClimbingOnceALevelFallsBelowTheFloor(t *testing.T) {
	p := &fakeProber{
		solo:     10 * time.Millisecond,
		perLevel: map[int]time.Duration{2: 200 * time.Millisecond},
		tokens:   16,
	}
	prof, err := Calibrate(context.Background(), p, Key{Model: "m", Endpoint: "http://h:1"}, Options{})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if prof.MaxParallel != 1 {
		t.Fatalf("knee = %d, want 1", prof.MaxParallel)
	}
	// 2 fell off, so 4 and 8 must never have been attempted: the whole point
	// of the escalation rule is not paying for levels that cannot win.
	if p.peak > 2 {
		t.Fatalf("peak concurrency %d — must not probe past the first failing level", p.peak)
	}
	for _, l := range prof.Levels {
		if l.Concurrency > 2 {
			t.Fatalf("measured concurrency %d after the floor was breached", l.Concurrency)
		}
	}
}

func TestCalibrateProbesEightOnlyWhenFourStillScales(t *testing.T) {
	p := &fakeProber{
		// Every level costs the same as solo: perfect scaling. 40ms leaves a
		// 26ms jitter budget before 8-way could dip under the floor.
		solo:     40 * time.Millisecond,
		tokens:   16,
		perLevel: map[int]time.Duration{},
	}
	prof, err := Calibrate(context.Background(), p, Key{Model: "m", Endpoint: "http://h:1"}, Options{})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if prof.MaxParallel != 8 {
		t.Fatalf("knee = %d, want 8 for a server that scales perfectly", prof.MaxParallel)
	}
	if p.peak != 8 {
		t.Fatalf("peak concurrency %d, want 8", p.peak)
	}
}

func TestCalibrateFailsClosedWhenTheEndpointNeverAnswers(t *testing.T) {
	p := &fakeProber{solo: time.Millisecond, failFrom: 1}
	_, err := Calibrate(context.Background(), p, Key{Model: "m", Endpoint: "http://h:1"}, Options{})
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestCalibrateKeepsPartialEvidenceWhenALevelFails(t *testing.T) {
	// Warm-up + 3 solo = 4 calls succeed; the concurrency levels then fail.
	p := &fakeProber{solo: time.Millisecond, tokens: 16, failFrom: 5}
	prof, err := Calibrate(context.Background(), p, Key{Model: "m", Endpoint: "http://h:1"}, Options{})
	if err != nil {
		t.Fatalf("a lost level must not discard the baseline: %v", err)
	}
	if !prof.Partial {
		t.Fatal("a probe that lost a level must say so")
	}
	if prof.MaxParallel != 1 || prof.P95Ms <= 0 {
		t.Fatalf("baseline lost: knee=%d p95=%dms", prof.MaxParallel, prof.P95Ms)
	}
}

func TestCalibrateRespectsItsBudget(t *testing.T) {
	p := &fakeProber{solo: 20 * time.Millisecond, tokens: 16}
	start := time.Now()
	prof, err := Calibrate(context.Background(), p, Key{Model: "m", Endpoint: "http://h:1"},
		Options{Budget: 25 * time.Millisecond})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	// Warm-up plus one solo call already exceed the budget, so the probe must
	// stop rather than march through every level.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("probe took %s despite a 25ms budget", elapsed)
	}
	if !prof.Partial {
		t.Fatal("a budget-truncated probe must be marked partial")
	}
}

func TestCalibrateLeavesThroughputUnmeasuredWithoutUsage(t *testing.T) {
	p := &fakeProber{solo: 3 * time.Millisecond, tokens: 0}
	prof, err := Calibrate(context.Background(), p, Key{Model: "m", Endpoint: "http://h:1"}, Options{})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if prof.TokensPerSec != 0 {
		t.Fatalf("tokens/sec = %v — a rate must never be invented from max_tokens", prof.TokensPerSec)
	}
	if _, ok := prof.RecommendedTaskTimeout(4096); ok {
		t.Fatal("no throughput means no task_timeout recommendation")
	}
	if _, ok := prof.RoleLatencySeed(4096); ok {
		t.Fatal("no throughput means no latency seed")
	}
}

func TestCalibrateSurvivesMetadataFailure(t *testing.T) {
	p := &fakeProber{solo: 2 * time.Millisecond, tokens: 16, metaErr: errors.New("no /v1/models")}
	prof, err := Calibrate(context.Background(), p, Key{Model: "m", Endpoint: "http://h:1"}, Options{})
	if err != nil {
		t.Fatalf("a server without /v1/models must still calibrate: %v", err)
	}
	if prof.ContextLimit != 0 {
		t.Fatalf("context limit = %d, want 0 when the server reports nothing", prof.ContextLimit)
	}
	if prof.MaxParallel <= 0 {
		t.Fatal("the concurrency measurement is independent of metadata")
	}
}

func TestEndpointIdentityIsStableAndNonSecret(t *testing.T) {
	tests := []struct{ in, want string }{
		{"http://127.0.0.1:8000/v1", "http://127.0.0.1:8000"},
		{"http://127.0.0.1:8000/v1/chat/completions", "http://127.0.0.1:8000"},
		{"127.0.0.1:1234/v1", "http://127.0.0.1:1234"},
		{"https://API.OpenAI.com/v1", "https://api.openai.com"},
		{"https://user:secret@gw.example.com:8443/v1?key=abc", "https://gw.example.com:8443"},
		{"  ", ""},
	}
	for _, tc := range tests {
		if got := EndpointIdentity(tc.in); got != tc.want {
			t.Fatalf("EndpointIdentity(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(EndpointIdentity(tc.in), "secret") {
			t.Fatalf("credentials leaked into the endpoint identity for %q", tc.in)
		}
	}
}

func TestKeyUsesTheExactModelIdNotTheFamily(t *testing.T) {
	// The correction that matters: folding "Qwen3.8-27B-4bit" and
	// "Qwen3.8-9B-4bit" to one family would hand a 27B the 9B's knee.
	a := Key{Model: "Qwen3.8-27B-4bit", Endpoint: "http://127.0.0.1:8000/v1"}.ID()
	b := Key{Model: "Qwen3.8-9B-4bit", Endpoint: "http://127.0.0.1:8000/v1"}.ID()
	if a == b {
		t.Fatal("two parameter counts of one family must not share a profile")
	}
	// Two servers on the same box differ only by port.
	c := Key{Model: "m", Endpoint: "http://127.0.0.1:8000/v1"}.ID()
	d := Key{Model: "m", Endpoint: "http://127.0.0.1:8001/v1"}.ID()
	if c == d {
		t.Fatal("two ports on one host must not share a profile")
	}
	// The same pair spelled differently is the same profile.
	e := Key{Model: "m", Endpoint: "http://127.0.0.1:8000/v1/"}.ID()
	f := Key{Model: "m", Endpoint: "http://127.0.0.1:8000"}.ID()
	if e != f {
		t.Fatal("path spelling must not fork the identity")
	}
}

func TestParseModelsMetadataCoversTheCommonSpellings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"oMLX / vLLM max_model_len", `{"data":[{"id":"m","max_model_len":262144}]}`, 262144},
		{"max_context_length", `{"data":[{"id":"m","max_context_length":32768}]}`, 32768},
		{"context_length", `{"data":[{"id":"m","context_length":8192}]}`, 8192},
		{"context_window", `{"data":[{"id":"m","context_window":128000}]}`, 128000},
		{"model absent", `{"data":[{"id":"other","max_model_len":4096}]}`, 0},
		{"nothing reported", `{"data":[{"id":"m"}]}`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			md, err := parseModelsMetadata([]byte(tc.body), "m")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if md.ContextLimit != tc.want {
				t.Fatalf("context limit = %d, want %d", md.ContextLimit, tc.want)
			}
		})
	}
}

func TestRecommendedTaskTimeoutTracksMeasuredSpeed(t *testing.T) {
	slow := Profile{TokensPerSec: 10, P95Ms: 1700, QueueInflation: 1.4}
	fast := Profile{TokensPerSec: 100, P95Ms: 600, QueueInflation: 1.4}
	slowWant, ok := slow.RecommendedTaskTimeout(4096)
	if !ok {
		t.Fatal("a measured rate must yield a recommendation")
	}
	fastWant, ok := fast.RecommendedTaskTimeout(4096)
	if !ok {
		t.Fatal("a measured rate must yield a recommendation")
	}
	if slowWant <= fastWant {
		t.Fatalf("slow model recommended %s, fast model %s — must be proportional to measured speed",
			slowWant, fastWant)
	}
	if fastWant < 2*time.Minute {
		t.Fatalf("recommendation %s is below the 2m floor", fastWant)
	}
	if slowWant%(30*time.Second) != 0 {
		t.Fatalf("recommendation %s is not a readable 30s step", slowWant)
	}
}

func TestRoleLatencySeedIsProportionalToRoleTokens(t *testing.T) {
	p := Profile{TokensPerSec: 20, P95Ms: 1000, QueueInflation: 1.5}
	small, ok1 := p.RoleLatencySeed(1024)
	big, ok2 := p.RoleLatencySeed(4096)
	if !ok1 || !ok2 {
		t.Fatal("a measured rate must yield a seed")
	}
	if big <= small {
		t.Fatalf("seed for 4096 tokens (%s) must exceed 1024 tokens (%s)", big, small)
	}
	// The relationship, not a magic number: 4× the tokens is roughly 4× the
	// decode time plus the same fixed overhead.
	if big < 2*small {
		t.Fatalf("seed is not proportional: %s vs %s", big, small)
	}
}
