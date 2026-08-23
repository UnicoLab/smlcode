package backends

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Observed decode rates are process-wide state (GlobalThroughput), but the two
// consumers that most want them do not share a process with the run that
// measured them: `slmcode doctor` and `slmcode metrics` are separate
// invocations. This file gives Throughput the same treatment the capability
// cache already gets — a small JSON file under .slmcode, loaded on first read
// and rewritten as observations arrive — so "how fast is this model actually
// decoding?" has an answer outside the run that measured it.

// ThroughputTTL is how long a recorded rate stays trustworthy. Hardware,
// quantization and server flags all change the answer, so a month-old sample
// is not evidence about today's setup.
const ThroughputTTL = 30 * 24 * time.Hour

// throughputFileName is the on-disk store, next to capabilities.json.
const throughputFileName = "throughput.json"

// persistedRate is one model's stored decode rate.
type persistedRate struct {
	TokensPerSec float64   `json:"tokens_per_sec"`
	Samples      int       `json:"samples"`
	At           time.Time `json:"at"`
}

var throughputStore struct {
	mu     sync.Mutex
	dir    string
	loaded bool
	// lastSave throttles rewrites: Observe fires once per completion, and a
	// parallel wave can finish several within the same second.
	lastSave time.Time
}

// SetThroughputCacheDir points the on-disk throughput store at dir (normally
// `.slmcode`). Passing "" disables persistence. RegisterLLM calls this
// automatically, so no caller wiring is required.
func SetThroughputCacheDir(dir string) {
	throughputStore.mu.Lock()
	defer throughputStore.mu.Unlock()
	if throughputStore.dir == dir {
		return
	}
	throughputStore.dir = dir
	throughputStore.loaded = false
}

func throughputPath() string {
	if throughputStore.dir == "" {
		return ""
	}
	return filepath.Join(throughputStore.dir, throughputFileName)
}

// loadThroughput merges the on-disk store into GlobalThroughput, once per dir.
// A model already observed in THIS process always wins: a live measurement is
// better evidence than a stored one.
func loadThroughput() {
	throughputStore.mu.Lock()
	if throughputStore.loaded {
		throughputStore.mu.Unlock()
		return
	}
	throughputStore.loaded = true
	p := throughputPath()
	throughputStore.mu.Unlock()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p) // #nosec G304 -- path derived from the project root
	if err != nil {
		return
	}
	var disk map[string]persistedRate
	if err := json.Unmarshal(b, &disk); err != nil {
		return
	}
	GlobalThroughput.mu.Lock()
	defer GlobalThroughput.mu.Unlock()
	if GlobalThroughput.m == nil {
		GlobalThroughput.m = map[string]*tpEntry{}
	}
	for model, r := range disk {
		if r.TokensPerSec <= 0 || r.Samples <= 0 {
			continue
		}
		if !r.At.IsZero() && time.Since(r.At) > ThroughputTTL {
			continue
		}
		if _, live := GlobalThroughput.m[model]; live {
			continue
		}
		GlobalThroughput.m[model] = &tpEntry{tps: r.TokensPerSec, samples: r.Samples}
	}
}

// saveThroughput writes the current snapshot, at most once every few seconds.
// Best-effort throughout: losing a decode-rate sample is not worth an error
// path in the completion hot loop.
func saveThroughput() {
	throughputStore.mu.Lock()
	p := throughputPath()
	if p == "" || time.Since(throughputStore.lastSave) < 5*time.Second {
		throughputStore.mu.Unlock()
		return
	}
	throughputStore.lastSave = time.Now()
	throughputStore.mu.Unlock()

	now := time.Now()
	out := map[string]persistedRate{}
	for _, o := range GlobalThroughput.Snapshot() {
		out[o.Model] = persistedRate{TokensPerSec: o.TokensPerSec, Samples: o.Samples, At: now}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	_ = atomicfile.Write(p, b, 0o600)
}

// ResetThroughputStore clears the persistence wiring (tests).
func ResetThroughputStore() {
	throughputStore.mu.Lock()
	throughputStore.dir = ""
	throughputStore.loaded = false
	throughputStore.lastSave = time.Time{}
	throughputStore.mu.Unlock()
}
