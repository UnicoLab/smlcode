package evolve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// CheckKind is how a regression check verifies that an old failure is still
// fixed.
type CheckKind string

const (
	// CheckCommand runs a shell command and expects exit 0. evolve NEVER runs
	// it: the harness does, under the permission system.
	CheckCommand CheckKind = "command"
	// CheckFileContains asserts a file contains a substring.
	CheckFileContains CheckKind = "file_contains"
	// CheckFileAbsent asserts a substring is NOT present in a file.
	CheckFileAbsent CheckKind = "file_absent"
	// CheckFileExists asserts a path exists.
	CheckFileExists CheckKind = "file_exists"
	// CheckNone records the failure with no cheap re-check available.
	CheckNone CheckKind = "none"
)

// Regression store bounds.
const (
	MaxChecks       = 200
	MaxCheckTextLen = 300
)

// Check is one fixed failure plus, where one exists, a cheap way to prove it
// has not come back. This is what makes "improves over time" measurable rather
// than aspirational: without a re-check, a fixed bug is just a story.
type Check struct {
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Class       Class     `json:"class,omitempty"`
	Description string    `json:"description"`
	Kind        CheckKind `json:"kind"`
	Command     string    `json:"command,omitempty"`
	Path        string    `json:"path,omitempty"`
	Substring   string    `json:"substring,omitempty"`
	RuleID      string    `json:"rule_id,omitempty"`
	AddedAt     time.Time `json:"added_at"`
	LastRun     time.Time `json:"last_run,omitempty"`
	LastOK      bool      `json:"last_ok"`
	Runs        int       `json:"runs,omitempty"`
	Fails       int       `json:"fails,omitempty"`
}

// Runnable reports whether the check can actually be evaluated.
func (c Check) Runnable() bool { return c.Kind != "" && c.Kind != CheckNone }

// Offline reports whether the check can be evaluated without running anything
// — these are safe for evolve to evaluate itself.
func (c Check) Offline() bool {
	switch c.Kind {
	case CheckFileContains, CheckFileAbsent, CheckFileExists:
		return c.Path != ""
	default:
		return false
	}
}

// Normalize fills defaults and bounds the fields.
func (c *Check) Normalize(now time.Time) {
	c.Description = clipStr(c.Description, MaxCheckTextLen)
	c.Command = clipStr(c.Command, MaxCheckTextLen)
	c.Path = strings.TrimSpace(c.Path)
	c.Substring = clipStr(c.Substring, MaxCheckTextLen)
	if c.Kind == "" {
		switch {
		case c.Command != "":
			c.Kind = CheckCommand
		case c.Path != "" && c.Substring != "":
			c.Kind = CheckFileContains
		case c.Path != "":
			c.Kind = CheckFileExists
		default:
			c.Kind = CheckNone
		}
	}
	if c.AddedAt.IsZero() {
		c.AddedAt = now.UTC()
	}
	if c.ID == "" {
		c.ID = "chk_" + shortHash(string(c.Kind), c.Command, c.Path, c.Substring, c.Fingerprint, c.Description)
	}
}

type checksFile struct {
	Version int       `json:"version"`
	Updated time.Time `json:"updated"`
	Checks  []Check   `json:"checks"`
}

// Regressions is the store of fixed failures and their re-checks.
type Regressions struct {
	mu       sync.RWMutex
	path     string
	byID     map[string]*Check
	order    []string
	dirty    bool
	warnings []string
	now      func() time.Time
}

// OpenRegressions loads the regression store from a project directory.
// An empty dir yields an in-memory store.
func OpenRegressions(projectDir string) (*Regressions, error) {
	return OpenRegressionsWith(projectDir, nil)
}

// OpenRegressionsWith is OpenRegressions with an injectable clock.
func OpenRegressionsWith(projectDir string, now func() time.Time) (*Regressions, error) {
	if now == nil {
		now = time.Now
	}
	r := &Regressions{byID: map[string]*Check{}, now: now}
	if projectDir != "" {
		r.path = filepath.Join(projectDir, memory.SlmDirName, EvolveDirName, "regressions.json")
		r.load()
	}
	return r, nil
}

func (r *Regressions) load() {
	data, err := os.ReadFile(r.path) //nolint:gosec // path derived from the caller's own state dir
	if err != nil {
		return
	}
	var cf checksFile
	if err := json.Unmarshal(data, &cf); err != nil {
		r.warnings = append(r.warnings, "regressions.json unreadable; starting empty")
		_ = os.Rename(r.path, r.path+".corrupt")
		return
	}
	for i := range cf.Checks {
		c := cf.Checks[i]
		c.Normalize(r.now())
		if _, dup := r.byID[c.ID]; dup {
			continue
		}
		cp := c
		r.byID[cp.ID] = &cp
		r.order = append(r.order, cp.ID)
	}
}

// Add records a fixed failure. Re-adding an equivalent check is idempotent.
func (r *Regressions) Add(c Check) Check {
	c.Normalize(r.now())
	if c.Description == "" {
		return Check{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dirty = true
	if existing, ok := r.byID[c.ID]; ok {
		return *existing
	}
	cp := c
	r.byID[cp.ID] = &cp
	r.order = append(r.order, cp.ID)
	if len(r.order) > MaxChecks*2 {
		r.pruneLocked(MaxChecks)
	}
	return cp
}

// Checks returns every stored check, most recently added first, so the harness
// can verify that old failures have not returned.
func (r *Regressions) Checks() []Check {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Check, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		if c, ok := r.byID[r.order[i]]; ok {
			out = append(out, *c)
		}
	}
	return out
}

// Runnable returns only the checks that can actually be evaluated.
func (r *Regressions) Runnable() []Check {
	var out []Check
	for _, c := range r.Checks() {
		if c.Runnable() {
			out = append(out, c)
		}
	}
	return out
}

// Record stores the outcome of running a check.
func (r *Regressions) Record(id string, ok bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, found := r.byID[id]
	if !found {
		return false
	}
	c.Runs++
	if !ok {
		c.Fails++
	}
	c.LastOK = ok
	c.LastRun = r.now().UTC()
	r.dirty = true
	return true
}

// Result is the outcome of evaluating one check.
type Result struct {
	Check  Check
	OK     bool
	Detail string
}

// RunOffline evaluates the file-based checks against root. Command checks are
// skipped: evolve never executes anything, the harness does that through the
// permission system. Results are recorded.
func (r *Regressions) RunOffline(root string) []Result {
	var out []Result
	for _, c := range r.Checks() {
		if !c.Offline() {
			continue
		}
		ok, detail := evalOffline(root, c)
		r.Record(c.ID, ok)
		out = append(out, Result{Check: c, OK: ok, Detail: detail})
	}
	return out
}

func evalOffline(root string, c Check) (bool, string) {
	path := c.Path
	if root != "" && !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(path))
	}
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the project's own regression store
	switch c.Kind {
	case CheckFileExists:
		if err != nil {
			return false, "missing: " + c.Path
		}
		return true, ""
	case CheckFileContains:
		if err != nil {
			return false, "missing: " + c.Path
		}
		if !strings.Contains(string(data), c.Substring) {
			return false, "regressed: " + c.Path + " no longer contains the fix"
		}
		return true, ""
	case CheckFileAbsent:
		if err != nil {
			return true, "" // absent file trivially lacks the bad substring
		}
		if strings.Contains(string(data), c.Substring) {
			return false, "regressed: " + c.Path + " contains the bad pattern again"
		}
		return true, ""
	default:
		return true, "not evaluated offline"
	}
}

// Count returns how many checks are stored.
func (r *Regressions) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

// Prune bounds the store, keeping the most recently added checks.
func (r *Regressions) Prune(maxChecks int) int {
	if maxChecks <= 0 {
		maxChecks = MaxChecks
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pruneLocked(maxChecks)
}

func (r *Regressions) pruneLocked(maxChecks int) int {
	if len(r.order) <= maxChecks {
		return 0
	}
	drop := r.order[:len(r.order)-maxChecks]
	for _, id := range drop {
		delete(r.byID, id)
	}
	r.order = r.order[len(drop):]
	r.dirty = true
	return len(drop)
}

// Save persists the store.
func (r *Regressions) Save() error {
	r.mu.Lock()
	if r.path == "" || !r.dirty {
		r.mu.Unlock()
		return nil
	}
	cf := checksFile{Version: 1, Updated: r.now().UTC()}
	for _, id := range r.order {
		if c, ok := r.byID[id]; ok {
			cf.Checks = append(cf.Checks, *c)
		}
	}
	sort.SliceStable(cf.Checks, func(i, j int) bool { return cf.Checks[i].AddedAt.Before(cf.Checks[j].AddedAt) })
	r.dirty = false
	r.mu.Unlock()

	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(r.path, append(data, '\n'), 0o600)
}

// Forget deletes the store.
func (r *Regressions) Forget() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID = map[string]*Check{}
	r.order = nil
	r.dirty = false
	if r.path == "" {
		return nil
	}
	if err := os.Remove(r.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Warnings returns non-fatal load problems.
func (r *Regressions) Warnings() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.warnings...)
}

func clipStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n])
}
