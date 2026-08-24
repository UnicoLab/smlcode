package plan

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Attempt lineage.
//
// A task is rarely solved on the first pass: a worker answers, a reviewer
// rejects, a corrector answers again. The harness used to destroy that history
// as it created it — Task.Output was overwritten by every corrector pass,
// Task.AttemptLog kept six capped prose lines with no parent pointer, and the
// in-process ledger that remembered "what was already tried" died with the
// process. Nothing joined *this task → this hypothesis → this diff → this
// reviewer verdict → this failure reason*, so a small model was free to
// re-propose an approach a reviewer had already refused twice.
//
// An Attempt is that missing join, persisted. It obeys the same rules as
// pkg/memory and pkg/graph:
//
//   - Deterministic, no LLM. Hypothesis is DERIVED from text the run already
//     produced (see DeriveHypothesis); nothing here calls a model.
//   - Bounded. Every field is capped, Output hardest of all, and the store caps
//     at DefaultMaxAttempts with Prune rewriting the log so the file shrinks.
//   - Safe to be wrong. A corrupt index is moved aside and rebuilt from the
//     log, a corrupt log line is skipped, and problems surface through
//     Warnings rather than failing a run.
//   - Inspectable and reversible. Plain JSONL and JSON under .slmcode/attempts.
//     `rm -rf .slmcode/attempts` is a supported operation: the harness loses
//     lineage and keeps working.

// On-disk layout and bounds.
const (
	// AttemptsDirName is the directory this store owns under .slmcode/.
	AttemptsDirName = "attempts"
	// AttemptsLogFileName is the append-only attempt log.
	AttemptsLogFileName = "attempts.jsonl"
	// AttemptsIndexFileName is the rebuildable lineage index.
	AttemptsIndexFileName = "attempts.index.json"

	// DefaultMaxAttempts is the shipped ceiling on stored attempts.
	DefaultMaxAttempts = 2000
	// DefaultMaxAttemptAge is how long an attempt stays interesting.
	DefaultMaxAttemptAge = 180 * 24 * time.Hour

	// MaxAttemptOutputLen caps the stored model output. An attempt is a
	// *record*, not a transcript: without this the log grows without bound.
	MaxAttemptOutputLen = 4 * 1024
	// MaxAttemptHypothesisLen caps the derived "what this tried" line.
	MaxAttemptHypothesisLen = 300
	// MaxAttemptTextLen caps an issue, a gate signal or a diffstat.
	MaxAttemptTextLen = 240
	// MaxAttemptIssues / MaxAttemptFiles / MaxAttemptGates cap the lists.
	MaxAttemptIssues = 8
	MaxAttemptFiles  = 24
	MaxAttemptGates  = 12
	// MaxAttemptLineLen is the longest JSONL line that will be parsed; a longer
	// one is treated as corrupt so a damaged file cannot exhaust memory.
	MaxAttemptLineLen = 64 * 1024

	attemptsIndexVersion = 1
	slmStateDirName      = ".slmcode"
)

// UnknownRunID stands in when an attempt is recorded outside a named run. It
// keeps an attempt id well-formed (<runID>/<taskID>/<n>) so the id can always
// be turned into a graph node.
const UnknownRunID = "unknown-run"

// Attempt verdicts.
const (
	AttemptApproved  = "approved"
	AttemptRejected  = "rejected"
	AttemptEscalated = "escalated"
	AttemptError     = "error"
)

// Attempt is one completed pass at a task: what it tried, what it changed, and
// what the reviewer said about it. ParentID points at the pass it grew out of,
// which is what makes a chain of attempts readable as a chain.
type Attempt struct {
	// ID is stable and derivable: <runID>/<taskID>/<n>.
	ID    string `json:"id"`
	RunID string `json:"run_id,omitempty"`
	// TaskID is the board task this attempt was made against.
	TaskID string `json:"task_id"`
	// N is the 1-based attempt number within the task.
	N int `json:"n"`
	// ParentID is the previous attempt's ID; "" for the first attempt.
	ParentID string `json:"parent_id,omitempty"`
	Role     string `json:"role,omitempty"`
	// Hypothesis is what this attempt tried, derived deterministically from the
	// text the run already produced. Empty when nothing was derivable — an
	// empty hypothesis is a gap, a fabricated one is a lie.
	Hypothesis string `json:"hypothesis,omitempty"`
	// Output is the model output for THIS attempt, capped at
	// MaxAttemptOutputLen.
	Output string `json:"output,omitempty"`
	// FilesTouched is what the attempt claimed or was focused on.
	FilesTouched []string `json:"files_touched,omitempty"`
	// DiffStat is compact ("3 files, +42/-7"). Full diffs are never stored.
	DiffStat string `json:"diff_stat,omitempty"`
	// GateSignals names the harness gates that fired on this attempt.
	GateSignals []string `json:"gate_signals,omitempty"`
	// Verdict is approved | rejected | escalated | error.
	Verdict string   `json:"verdict,omitempty"`
	Score   float64  `json:"score,omitempty"`
	Issues  []string `json:"issues,omitempty"`
	// FailureClass is the evolve fingerprint class, when one was derivable.
	FailureClass string    `json:"failure_class,omitempty"`
	At           time.Time `json:"at"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
}

// AttemptID returns the stable id for the nth attempt at a task.
func AttemptID(runID, taskID string, n int) string {
	runID, taskID = strings.TrimSpace(runID), strings.TrimSpace(taskID)
	if runID == "" {
		runID = UnknownRunID
	}
	if taskID == "" {
		taskID = "-"
	}
	if n < 1 {
		n = 1
	}
	return runID + "/" + taskID + "/" + strconv.Itoa(n)
}

// Normalize fills defaults and clamps every unbounded field. Always called
// before an attempt is stored, so a record on disk is always within its caps.
func (a *Attempt) Normalize(now time.Time) {
	if a.At.IsZero() {
		a.At = now
	}
	a.At = a.At.UTC().Truncate(time.Second)
	a.RunID = strings.TrimSpace(a.RunID)
	if a.RunID == "" {
		a.RunID = UnknownRunID
	}
	a.TaskID = strings.TrimSpace(a.TaskID)
	a.Role = strings.TrimSpace(a.Role)
	a.ParentID = strings.TrimSpace(a.ParentID)
	if a.N < 1 {
		a.N = 1
	}
	a.Hypothesis = attemptOneLine(a.Hypothesis, MaxAttemptHypothesisLen)
	a.Output = attemptClip(strings.TrimSpace(a.Output), MaxAttemptOutputLen)
	a.DiffStat = attemptOneLine(a.DiffStat, MaxAttemptTextLen)
	a.FilesTouched = attemptDedupe(a.FilesTouched, MaxAttemptFiles, MaxAttemptTextLen)
	a.GateSignals = attemptDedupe(a.GateSignals, MaxAttemptGates, MaxAttemptTextLen)
	a.Issues = attemptDedupe(a.Issues, MaxAttemptIssues, MaxAttemptTextLen)
	a.FailureClass = strings.ToLower(strings.TrimSpace(a.FailureClass))
	a.Verdict = strings.ToLower(strings.TrimSpace(a.Verdict))
	if a.Verdict == "" && len(a.Issues) > 0 {
		// A record with issues and no stated verdict is a rejection: that is
		// the only reading that does not invent information.
		a.Verdict = AttemptRejected
	}
	if a.Score < 0 {
		a.Score = 0
	}
	if a.DurationMS < 0 {
		a.DurationMS = 0
	}
	if a.ID == "" {
		a.ID = AttemptID(a.RunID, a.TaskID, a.N)
	}
	if a.ParentID == a.ID {
		// A self-parent is a one-node cycle; drop it rather than store it.
		a.ParentID = ""
	}
}

// Validate reports why an attempt cannot be stored.
func (a Attempt) Validate() error {
	switch {
	case strings.TrimSpace(a.TaskID) == "":
		return errors.New("plan: attempt has no task id")
	case strings.TrimSpace(a.ID) == "":
		return errors.New("plan: attempt has no id")
	}
	return nil
}

// Rejected reports whether this attempt was refused — by the reviewer, by a
// gate, by escalation or by an error. This is what "already tried and did not
// work" means.
func (a Attempt) Rejected() bool {
	switch a.Verdict {
	case AttemptRejected, AttemptEscalated, AttemptError:
		return true
	default:
		return false
	}
}

// Reason returns the single best statement of why this attempt was refused, or
// "" when it was not refused or recorded no reason.
func (a Attempt) Reason() string {
	if !a.Rejected() {
		return ""
	}
	if len(a.Issues) > 0 {
		return a.Issues[0]
	}
	if a.FailureClass != "" {
		return "failure class: " + a.FailureClass
	}
	if len(a.GateSignals) > 0 {
		return "gate: " + strings.Join(a.GateSignals, ", ")
	}
	return ""
}

// ── the store ───────────────────────────────────────────────────────────────

// attemptEntry is the searchable projection of an attempt plus where its line
// starts in the log. Lineage joins run against these, so walking a chain never
// has to parse the whole JSONL.
type attemptEntry struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent_id,omitempty"`
	RunID    string    `json:"run_id,omitempty"`
	TaskID   string    `json:"task_id,omitempty"`
	N        int       `json:"n,omitempty"`
	Verdict  string    `json:"verdict,omitempty"`
	At       time.Time `json:"at"`
	Offset   int64     `json:"offset"`
}

func attemptEntryOf(a Attempt, offset int64) attemptEntry {
	return attemptEntry{
		ID: a.ID, ParentID: a.ParentID, RunID: a.RunID, TaskID: a.TaskID,
		N: a.N, Verdict: a.Verdict, At: a.At, Offset: offset,
	}
}

type attemptIndexFile struct {
	Version int            `json:"version"`
	Updated time.Time      `json:"updated"`
	Entries []attemptEntry `json:"entries"`
}

// AttemptOptions configures OpenAttemptsWith.
type AttemptOptions struct {
	// ReadOnly opens the store without ever writing: no directory creation, no
	// appends, no index flush, no corrupt-file quarantine.
	ReadOnly bool
	// Max bounds the store. Zero uses DefaultMaxAttempts.
	Max int
	// SlmDir overrides <root>/.slmcode for callers that were configured with an
	// explicit state directory.
	SlmDir string
	// Now is injectable for deterministic tests.
	Now func() time.Time
}

// Attempts is the append-only attempt store.
//
// An Attempts is always usable. OpenAttempts never fails because of a corrupt
// or unwritable store: it degrades to in-memory operation and records the
// problem in Warnings. Lineage is valuable, but losing it must never cost a run.
type Attempts struct {
	mu       sync.RWMutex
	dir      string // "" ⇒ in-memory only
	readOnly bool
	max      int
	now      func() time.Time

	entries  []attemptEntry
	byID     map[string]int
	children map[string][]int
	byTask   map[string][]int
	mem      map[string]Attempt // full records, in-memory mode only
	warnings []string
	dirty    bool
}

// OpenAttempts opens (or creates) the attempt store at <root>/.slmcode/attempts.
//
// An empty root yields a fully in-memory store, which is the correct behavior
// for slmcode invoked outside a workspace.
func OpenAttempts(root string) (*Attempts, error) { return OpenAttemptsWith(root, AttemptOptions{}) }

// OpenAttemptsWith is OpenAttempts with explicit options.
func OpenAttemptsWith(root string, opt AttemptOptions) (*Attempts, error) {
	max := opt.Max
	if max <= 0 {
		max = DefaultMaxAttempts
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	s := &Attempts{
		readOnly: opt.ReadOnly,
		max:      max,
		now:      now,
		byID:     map[string]int{},
		children: map[string][]int{},
		byTask:   map[string][]int{},
		mem:      map[string]Attempt{},
	}
	base := strings.TrimSpace(opt.SlmDir)
	if base == "" {
		if root = strings.TrimSpace(root); root != "" {
			base = filepath.Join(root, slmStateDirName)
		}
	}
	if base != "" {
		s.dir = filepath.Join(base, AttemptsDirName)
		if err := s.ensure(); err != nil {
			s.warnings = append(s.warnings, "attempts disabled: "+err.Error())
			s.dir = ""
		}
	}
	s.load()
	return s, nil
}

func (s *Attempts) ensure() error {
	if s.dir == "" || s.readOnly {
		return nil
	}
	return os.MkdirAll(s.dir, 0o750)
}

// Dir returns the store directory ("" when the store is in-process only).
func (s *Attempts) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

func (s *Attempts) logPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, AttemptsLogFileName)
}

func (s *Attempts) indexPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, AttemptsIndexFileName)
}

// load reads the index and verifies it against the log. A missing, corrupt,
// truncated or hand-edited index is rebuilt from the log — never fatal.
func (s *Attempts) load() {
	if s.dir == "" {
		return
	}
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		s.rebuild()
		return
	}
	var idx attemptIndexFile
	if err := json.Unmarshal(data, &idx); err != nil {
		// Corrupt store: preserve the bad file rather than silently destroying
		// it, and rebuild from the log, which is the source of truth anyway.
		s.warnings = append(s.warnings, AttemptsIndexFileName+" unreadable; moved aside and rebuilt from log")
		s.quarantine(s.indexPath())
		s.rebuild()
		return
	}
	if len(idx.Entries) == 0 {
		s.rebuild()
		return
	}
	s.setEntries(idx.Entries)
	if !s.indexLooksValid() {
		s.warnings = append(s.warnings, "attempt index stale; rebuilt from log")
		s.rebuild()
	}
}

// quarantine moves a file aside to <name>.corrupt. Never in read-only mode: an
// inspection must not mutate the store it is inspecting.
func (s *Attempts) quarantine(path string) {
	if path == "" || s.readOnly {
		return
	}
	_ = os.Rename(path, path+".corrupt")
}

// indexLooksValid spot-checks the newest entry's offset against the log. A full
// verification would defeat the point of having an index.
func (s *Attempts) indexLooksValid() bool {
	if len(s.entries) == 0 {
		return true
	}
	last := s.entries[len(s.entries)-1]
	got, ok := s.readAt(last.Offset)
	return ok && got.ID == last.ID
}

// rebuild reconstructs the index by scanning the log.
func (s *Attempts) rebuild() {
	path := s.logPath()
	if path == "" {
		s.setEntries(nil)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.setEntries(nil)
		return
	}
	defer func() { _ = f.Close() }()

	var (
		entries []attemptEntry
		offset  int64
		corrupt int
	)
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := attemptReadLine(r)
		if len(line) > 0 {
			start := offset
			offset += int64(len(line))
			var a Attempt
			if json.Unmarshal(attemptTrimLine(line), &a) == nil && a.Validate() == nil {
				entries = append(entries, attemptEntryOf(a, start))
			} else if strings.TrimSpace(string(line)) != "" {
				corrupt++
			}
		}
		if err != nil {
			break
		}
	}
	if corrupt > 0 {
		s.warnings = append(s.warnings,
			"skipped "+strconv.Itoa(corrupt)+" corrupt attempt record(s)")
	}
	s.setEntries(entries)
	s.dirty = true
}

// setEntries installs entries and rebuilds every join map from them.
func (s *Attempts) setEntries(entries []attemptEntry) {
	sort.SliceStable(entries, func(i, j int) bool { return attemptEntryLess(entries[i], entries[j]) })
	s.entries = make([]attemptEntry, 0, len(entries))
	s.byID = make(map[string]int, len(entries))
	s.children = make(map[string][]int, len(entries))
	s.byTask = make(map[string][]int, len(entries))
	for _, en := range entries {
		if en.ID == "" {
			continue
		}
		if _, dup := s.byID[en.ID]; dup {
			continue
		}
		s.indexLocked(en)
	}
}

// indexLocked adds one entry to the in-memory joins.
func (s *Attempts) indexLocked(en attemptEntry) {
	i := len(s.entries)
	s.entries = append(s.entries, en)
	s.byID[en.ID] = i
	if en.ParentID != "" {
		s.children[en.ParentID] = append(s.children[en.ParentID], i)
	}
	if en.TaskID != "" {
		s.byTask[en.TaskID] = append(s.byTask[en.TaskID], i)
	}
}

// attemptEntryLess is the store's total order: oldest first, then attempt
// number, then id, so two loads of the same records never disagree.
func attemptEntryLess(a, b attemptEntry) bool {
	if !a.At.Equal(b.At) {
		return a.At.Before(b.At)
	}
	if a.N != b.N {
		return a.N < b.N
	}
	return a.ID < b.ID
}

// Append persists one attempt and updates the index.
//
// Re-appending an id that is already stored is a no-op: an attempt is a record
// of something that happened once, so observing it twice must not duplicate it.
func (s *Attempts) Append(a Attempt) (Attempt, error) {
	if s == nil {
		return a, nil
	}
	a.Normalize(s.now())
	if err := a.Validate(); err != nil {
		return a, err
	}
	if s.readOnly {
		return a, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.byID[a.ID]; dup {
		return a, nil
	}

	offset := int64(-1)
	if path := s.logPath(); path != "" {
		off, err := s.appendLine(path, a)
		if err != nil {
			return a, err
		}
		offset = off
	} else {
		s.mem[a.ID] = a
	}
	s.indexLocked(attemptEntryOf(a, offset))
	s.dirty = true
	if s.max > 0 && len(s.entries) > s.max*2 {
		// Hard backstop so a run that never calls Prune still cannot blow up.
		s.pruneLocked(AttemptPrunePolicy{MaxAttempts: s.max, MaxAge: -1})
	}
	return a, nil
}

// appendLine writes one record and returns the offset it was written at.
func (s *Attempts) appendLine(path string, a Attempt) (int64, error) {
	data, err := json.Marshal(a)
	if err != nil {
		return -1, err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return -1, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return -1, err
	}
	offset := int64(-1)
	if info, statErr := f.Stat(); statErr == nil {
		offset = info.Size()
	}
	// A single Write of a sub-PIPE_BUF line is atomic on POSIX, so a crashed
	// run leaves whole records, never a spliced one.
	_, wErr := f.Write(data)
	cErr := f.Close()
	if err := errors.Join(wErr, cErr); err != nil {
		return -1, err
	}
	return offset, nil
}

// Count returns how many attempts are indexed.
func (s *Attempts) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Get hydrates one attempt by id.
func (s *Attempts) Get(id string) (Attempt, bool) {
	if s == nil {
		return Attempt{}, false
	}
	s.mu.RLock()
	i, ok := s.byID[strings.TrimSpace(id)]
	var en attemptEntry
	if ok {
		en = s.entries[i]
	}
	s.mu.RUnlock()
	if !ok {
		return Attempt{}, false
	}
	return s.hydrate(en)
}

// All returns every stored attempt, oldest first. Bounded by the store cap.
func (s *Attempts) All() []Attempt {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	entries := append([]attemptEntry(nil), s.entries...)
	s.mu.RUnlock()
	return s.hydrateAll(entries)
}

// ForTask returns every attempt at one task in a run, ordered oldest first.
// An empty runID matches every run.
func (s *Attempts) ForTask(runID, taskID string) []Attempt {
	if s == nil {
		return nil
	}
	runID, taskID = strings.TrimSpace(runID), strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	s.mu.RLock()
	var entries []attemptEntry
	for _, i := range s.byTask[taskID] {
		if runID != "" && s.entries[i].RunID != runID {
			continue
		}
		entries = append(entries, s.entries[i])
	}
	s.mu.RUnlock()
	sort.SliceStable(entries, func(i, j int) bool { return attemptEntryLess(entries[i], entries[j]) })
	return s.hydrateAll(entries)
}

// Children returns the attempts whose ParentID is attemptID, oldest first.
func (s *Attempts) Children(attemptID string) []Attempt {
	if s == nil {
		return nil
	}
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		return nil
	}
	s.mu.RLock()
	var entries []attemptEntry
	for _, i := range s.children[attemptID] {
		entries = append(entries, s.entries[i])
	}
	s.mu.RUnlock()
	sort.SliceStable(entries, func(i, j int) bool { return attemptEntryLess(entries[i], entries[j]) })
	return s.hydrateAll(entries)
}

// Leaves returns the attempts in a run that nothing points at as a parent —
// the tip of every chain. An empty runID considers every run.
func (s *Attempts) Leaves(runID string) []Attempt {
	if s == nil {
		return nil
	}
	runID = strings.TrimSpace(runID)
	s.mu.RLock()
	var entries []attemptEntry
	for _, en := range s.entries {
		if runID != "" && en.RunID != runID {
			continue
		}
		if len(s.children[en.ID]) > 0 {
			continue
		}
		entries = append(entries, en)
	}
	s.mu.RUnlock()
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].TaskID != entries[j].TaskID {
			return entries[i].TaskID < entries[j].TaskID
		}
		return attemptEntryLess(entries[i], entries[j])
	})
	return s.hydrateAll(entries)
}

// Lineage returns one chain of attempts at a task, ordered root → leaf.
//
// The chain is the newest leaf walked back up its ParentID pointers, so it is
// the history that actually produced the current state of the task rather than
// a flat list of everything ever tried. A parent that has been pruned away ends
// the walk: a truncated chain is honest, a fabricated link is not.
func (s *Attempts) Lineage(taskID string) []Attempt {
	if s == nil {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}

	s.mu.RLock()
	positions := s.byTask[taskID]
	leaf, found := -1, false
	for _, i := range positions {
		if len(s.children[s.entries[i].ID]) > 0 {
			continue
		}
		if !found || attemptEntryLess(s.entries[leaf], s.entries[i]) {
			leaf, found = i, true
		}
	}
	if !found {
		// Every node has a child (a cycle, or the leaf was pruned): fall back to
		// the newest record rather than returning nothing.
		for _, i := range positions {
			if !found || attemptEntryLess(s.entries[leaf], s.entries[i]) {
				leaf, found = i, true
			}
		}
	}
	var chain []attemptEntry
	seen := make(map[string]bool, len(positions))
	for found && !seen[s.entries[leaf].ID] {
		en := s.entries[leaf]
		seen[en.ID] = true
		chain = append(chain, en)
		next, ok := s.byID[en.ParentID]
		if en.ParentID == "" || !ok {
			break
		}
		leaf = next
	}
	s.mu.RUnlock()

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return s.hydrateAll(chain)
}

func (s *Attempts) hydrateAll(entries []attemptEntry) []Attempt {
	out := make([]Attempt, 0, len(entries))
	for _, en := range entries {
		if a, ok := s.hydrate(en); ok {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Attempts) hydrate(en attemptEntry) (Attempt, bool) {
	if s.dir == "" {
		s.mu.RLock()
		a, ok := s.mem[en.ID]
		s.mu.RUnlock()
		return a, ok
	}
	if en.Offset >= 0 {
		if a, ok := s.readAt(en.Offset); ok && a.ID == en.ID {
			return a, true
		}
	}
	// Offsets drifted (hand-edited or pruned log): fall back to a scan.
	return s.scanFor(en.ID)
}

// hydrateLocked is hydrate for callers already holding the write lock.
func (s *Attempts) hydrateLocked(en attemptEntry) (Attempt, bool) {
	if s.dir == "" {
		a, ok := s.mem[en.ID]
		return a, ok
	}
	if en.Offset >= 0 {
		if a, ok := s.readAt(en.Offset); ok && a.ID == en.ID {
			return a, true
		}
	}
	return s.scanFor(en.ID)
}

func (s *Attempts) readAt(offset int64) (Attempt, bool) {
	path := s.logPath()
	if path == "" || offset < 0 {
		return Attempt{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return Attempt{}, false
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Attempt{}, false
	}
	line, err := attemptReadLine(bufio.NewReaderSize(f, 64*1024))
	if len(line) == 0 && err != nil {
		return Attempt{}, false
	}
	var a Attempt
	if json.Unmarshal(attemptTrimLine(line), &a) != nil {
		return Attempt{}, false
	}
	return a, a.Validate() == nil
}

func (s *Attempts) scanFor(id string) (Attempt, bool) {
	path := s.logPath()
	if path == "" {
		return Attempt{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return Attempt{}, false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := attemptReadLine(r)
		if len(line) > 0 {
			var a Attempt
			if json.Unmarshal(attemptTrimLine(line), &a) == nil && a.ID == id {
				return a, true
			}
		}
		if err != nil {
			return Attempt{}, false
		}
	}
}

// AttemptPrunePolicy bounds the store. A zero field means "use the default";
// an explicitly negative field means "no limit on this axis".
type AttemptPrunePolicy struct {
	MaxAttempts int
	MaxAge      time.Duration
}

func (p AttemptPrunePolicy) withDefaults() AttemptPrunePolicy {
	if p.MaxAttempts == 0 {
		p.MaxAttempts = DefaultMaxAttempts
	}
	if p.MaxAge == 0 {
		p.MaxAge = DefaultMaxAttemptAge
	}
	return p
}

// Prune drops old and excess attempts, rewrites the JSONL log compactly and
// returns how many records it removed.
func (s *Attempts) Prune(policy AttemptPrunePolicy) int {
	if s == nil || s.readOnly {
		return 0
	}
	policy = policy.withDefaults()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked(policy)
}

func (s *Attempts) pruneLocked(policy AttemptPrunePolicy) int {
	before := len(s.entries)
	cutoff := time.Time{}
	if policy.MaxAge > 0 {
		cutoff = s.now().Add(-policy.MaxAge)
	}
	kept := make([]attemptEntry, 0, len(s.entries))
	for _, en := range s.entries {
		if !cutoff.IsZero() && en.At.Before(cutoff) {
			continue
		}
		kept = append(kept, en)
	}
	if policy.MaxAttempts > 0 && len(kept) > policy.MaxAttempts {
		kept = kept[len(kept)-policy.MaxAttempts:]
	}
	if len(kept) == before {
		return 0
	}

	// Rewrite the log so the file shrinks too — an append-only log that is
	// never compacted is an unbounded log.
	if path := s.logPath(); path != "" {
		var (
			buf     []byte
			rebuilt []attemptEntry
		)
		for _, en := range kept {
			a, ok := s.hydrateLocked(en)
			if !ok {
				continue
			}
			data, err := json.Marshal(a)
			if err != nil {
				continue
			}
			rebuilt = append(rebuilt, attemptEntryOf(a, int64(len(buf))))
			buf = append(buf, data...)
			buf = append(buf, '\n')
		}
		if err := atomicfile.Write(path, buf, 0o600); err == nil {
			kept = rebuilt
		}
	} else {
		keep := make(map[string]bool, len(kept))
		for _, en := range kept {
			keep[en.ID] = true
		}
		for id := range s.mem {
			if !keep[id] {
				delete(s.mem, id)
			}
		}
	}
	s.setEntries(kept)
	s.dirty = true
	return before - len(s.entries)
}

// Flush writes the index if it changed. The log is already durable; the index
// is a cache, so failing to write it costs a rebuild, never data.
func (s *Attempts) Flush() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" || s.readOnly || !s.dirty {
		return nil
	}
	idx := attemptIndexFile{Version: attemptsIndexVersion, Updated: s.now().UTC(), Entries: s.entries}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.indexPath(), append(data, '\n'), 0o600); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// Close flushes and releases the store.
func (s *Attempts) Close() error { return s.Flush() }

// Warnings returns non-fatal problems: a corrupt file, a skipped record, an
// unwritable directory. Callers should surface these and never abort over them.
func (s *Attempts) Warnings() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

// ForgetAttempts deletes the store's files. Equivalent to
// `rm -rf .slmcode/attempts`, which is itself a supported operation.
func ForgetAttempts(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(root, slmStateDirName, AttemptsDirName))
}

// ── deterministic hypothesis derivation ─────────────────────────────────────

// DeriveHypothesis states what an attempt tried, using only text the run had
// already produced. There is NO model call here and nothing is invented: when
// no statement is derivable the answer is "", because an empty hypothesis is a
// gap a reader can see while a fabricated one is a lie a reader believes.
//
// In priority order:
//
//  1. the attempt's own finalize JSON — `summary`, then `notes`. This is the
//     model stating what it did, in its own words, in a field the contract
//     already requires;
//  2. the first meaningful prose line of the output, skipping fences, JSON,
//     ReAct frames and harness-appended sections;
//  3. the reviewer issue that motivated this attempt — for a corrector pass,
//     "what this tried" is "make that complaint go away".
func DeriveHypothesis(output string, motivating []string) string {
	if s := attemptFinalizeSummary(output); s != "" {
		return attemptOneLine(s, MaxAttemptHypothesisLen)
	}
	if s := attemptFirstProseLine(output); s != "" {
		return attemptOneLine(s, MaxAttemptHypothesisLen)
	}
	for _, m := range motivating {
		if m = attemptOneLine(m, MaxAttemptHypothesisLen); m != "" {
			return attemptOneLine("address review issue: "+m, MaxAttemptHypothesisLen)
		}
	}
	return ""
}

// attemptFinalizeSummary lifts summary/notes out of the worker finalize JSON.
func attemptFinalizeSummary(output string) string {
	raw := strings.TrimSpace(extractJSON(output))
	if !strings.HasPrefix(raw, "{") {
		return ""
	}
	var payload struct {
		Summary string `json:"summary"`
		Notes   string `json:"notes"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return ""
	}
	if s := strings.TrimSpace(payload.Summary); s != "" {
		return s
	}
	return strings.TrimSpace(payload.Notes)
}

// attemptNoisePrefixes start lines that describe the machinery rather than the
// intent: ReAct frames, harness sections, fences and raw JSON.
var attemptNoisePrefixes = []string{
	"```", "{", "}", "[", "]", "\"", "#", "|", "observation:", "thought:",
	"action:", "action input:", "tool:", "final answer:", "exit status",
	"exit code", "$ ", "> ",
}

// attemptFirstProseLine returns the first line of output that reads like a
// person describing an intent.
func attemptFirstProseLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-*•0123456789. \t")
		if len(line) < 8 {
			continue
		}
		lower := strings.ToLower(line)
		noisy := false
		for _, p := range attemptNoisePrefixes {
			if strings.HasPrefix(lower, p) {
				noisy = true
				break
			}
		}
		if noisy {
			continue
		}
		return line
	}
	return ""
}

// ── rendering rejected approaches for a prompt ──────────────────────────────

// RejectedApproach is one distinct way a task has already been attempted and
// refused, together with the reason it was refused.
//
// Distinctness is keyed on the REASON, not on the wording of the approach: an
// SLM that is told the same complaint three times learns nothing it did not
// learn the first time, and the repetition costs prompt budget that a different
// rejection could have used.
type RejectedApproach struct {
	// Attempts lists the attempt numbers that were refused this way, oldest
	// first.
	Attempts []int
	// Approaches are the distinct hypotheses that ran into this reason, newest
	// first. Empty when none was derivable.
	Approaches []string
	// Reason is why the reviewer or a gate refused it.
	Reason string
	// Verdict is the verdict of the most recent attempt in this group.
	Verdict string
	// FailureClass is the evolve fingerprint class, when one was recorded.
	FailureClass string
}

// MaxRenderedApproaches bounds how many distinct rejections are rendered into a
// prompt, however many are stored.
const MaxRenderedApproaches = 4

// maxApproachesPerReason bounds how many distinct wordings of "what was tried"
// are shown for a single reason.
const maxApproachesPerReason = 2

// RejectedApproaches collapses a lineage into the distinct rejections it
// contains, most recent first.
func RejectedApproaches(attempts []Attempt) []RejectedApproach {
	var (
		out  []RejectedApproach
		seen = map[string]int{}
	)
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		reason := attemptOneLine(a.Reason(), MaxAttemptTextLen)
		if reason == "" {
			continue
		}
		key := attemptNormalizeKey(reason)
		if j, ok := seen[key]; ok {
			out[j].Attempts = append([]int{a.N}, out[j].Attempts...)
			out[j].Approaches = appendApproach(out[j].Approaches, a.Hypothesis)
			if out[j].FailureClass == "" {
				out[j].FailureClass = a.FailureClass
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, RejectedApproach{
			Attempts:     []int{a.N},
			Approaches:   appendApproach(nil, a.Hypothesis),
			Reason:       reason,
			Verdict:      a.Verdict,
			FailureClass: a.FailureClass,
		})
	}
	return out
}

func appendApproach(list []string, approach string) []string {
	approach = attemptOneLine(approach, MaxAttemptHypothesisLen)
	if approach == "" || len(list) >= maxApproachesPerReason {
		return list
	}
	key := attemptNormalizeKey(approach)
	for _, s := range list {
		if attemptNormalizeKey(s) == key {
			return list
		}
	}
	return append(list, approach)
}

// RejectedApproachSection renders the lineage as the block a corrector needs:
// the approaches already tried, and the reason each one was refused.
//
// This is THE payload that stops a small model re-proposing an approach the
// reviewer already rejected, so it is deduplicated, ordered newest-first and
// bounded by budget bytes — a section that blows the prompt budget gets
// truncated by something downstream, and what gets truncated is whatever came
// last.
func RejectedApproachSection(attempts []Attempt, budget int) string {
	if budget <= 0 {
		return ""
	}
	groups := RejectedApproaches(attempts)
	if len(groups) == 0 {
		return ""
	}
	header := "\n\n## Approaches already tried at THIS task and REJECTED — do not propose them again\n"
	footer := "Every line above is an approach that was already made and already refused. " +
		"Do not restate it in different words: pick a DIFFERENT approach, re-read the " +
		"target file first, make a smaller and more precise change, and prove it with a " +
		"tool call rather than a claim.\n"

	var b strings.Builder
	b.WriteString(header)
	rendered := 0
	for _, g := range groups {
		if rendered >= MaxRenderedApproaches {
			break
		}
		line := renderApproachLine(g)
		if line == "" {
			continue
		}
		if b.Len()+len(line)+len(footer) > budget {
			break
		}
		b.WriteString(line)
		rendered++
	}
	if rendered == 0 {
		return ""
	}
	if b.Len()+len(footer) <= budget {
		b.WriteString(footer)
	}
	return b.String()
}

func renderApproachLine(g RejectedApproach) string {
	reason := attemptOneLine(g.Reason, MaxAttemptTextLen)
	if reason == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("- attempt")
	if len(g.Attempts) > 1 {
		b.WriteString("s ")
	} else {
		b.WriteString(" ")
	}
	for i, n := range g.Attempts {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(n))
	}
	if len(g.Approaches) > 0 {
		b.WriteString(" tried: ")
		for i, a := range g.Approaches {
			if i > 0 {
				b.WriteString(" / ")
			}
			b.WriteString(a)
		}
	}
	verdict := g.Verdict
	if verdict == "" {
		verdict = AttemptRejected
	}
	b.WriteString(" → " + verdict + " because: " + reason)
	if g.FailureClass != "" {
		b.WriteString(" [" + g.FailureClass + "]")
	}
	b.WriteString("\n")
	return b.String()
}

// ── small helpers ───────────────────────────────────────────────────────────

// attemptOneLine flattens s to a single clipped line. Every value that reaches
// a prompt goes through this: a stored attempt is model-authored text, and a
// multi-line value could otherwise plant a line that reads like a harness
// section inside a bulleted list.
func attemptOneLine(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return attemptClip(strings.TrimSpace(s), n)
}

// attemptClip shortens s to at most n bytes without splitting a rune.
func attemptClip(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// attemptDedupe trims, drops blanks and duplicates, and caps the list length,
// keeping the first occurrences.
func attemptDedupe(in []string, max, itemLen int) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = attemptOneLine(s, itemLen)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if max > 0 && len(out) >= max {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// attemptNormalizeKey is the identity used to decide whether two strings say
// the same thing. Deliberately crude: case and punctuation differences are not
// a new rejection.
func attemptNormalizeKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevSpace = false
		default:
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// attemptReadLine reads one '\n'-terminated line, refusing absurdly long ones
// so a corrupt file cannot exhaust memory.
func attemptReadLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > MaxAttemptLineLen {
			// Drain to the next newline and report the oversized line as blank.
			for err == bufio.ErrBufferFull {
				_, err = r.ReadSlice('\n')
			}
			return nil, err
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}

func attemptTrimLine(b []byte) []byte {
	return []byte(strings.TrimRight(string(b), "\r\n"))
}
