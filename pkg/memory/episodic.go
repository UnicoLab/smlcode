package memory

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Episode field caps. An episode is a *record*, not a transcript: it must stay
// small enough that a few hundred of them load in single-digit milliseconds.
const (
	MaxEpisodeFiles    = 24
	MaxEpisodeTools    = 16
	MaxEpisodePlan     = 12
	MaxEpisodeFailures = 8
	MaxEpisodeGates    = 8
	MaxEpisodeCommands = 8
	MaxEpisodeTags     = 8
	MaxEpisodeTextLen  = 600
	MaxEpisodeLineLen  = 32 * 1024 // a longer JSONL line is treated as corrupt
)

// Verdicts.
const (
	VerdictSuccess = "success"
	VerdictPartial = "partial"
	VerdictFailure = "failure"
)

// GateOutcome is one quality gate and whether it passed.
type GateOutcome struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// Command is one shell command an episode ran and whether it worked. These are
// the raw material for the "build/test commands that actually worked" facts.
type Command struct {
	Cmd string `json:"cmd"`
	OK  bool   `json:"ok"`
}

// FailureNote records one failure and how (or whether) it was resolved.
type FailureNote struct {
	Fingerprint string `json:"fingerprint,omitempty"`
	Class       string `json:"class,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Message     string `json:"message"`
	Resolution  string `json:"resolution,omitempty"`
	ResolvedBy  string `json:"resolved_by,omitempty"` // rule id | "llm" | "retry" | ""
	Attempts    int    `json:"attempts,omitempty"`
}

// Resolved reports whether the failure ended up fixed.
func (f FailureNote) Resolved() bool { return strings.TrimSpace(f.ResolvedBy) != "" }

// FromMemory reports whether the fix came from a stored repair rule rather than
// a fresh LLM round-trip.
func (f FailureNote) FromMemory() bool {
	by := strings.TrimSpace(f.ResolvedBy)
	return by != "" && by != "llm" && by != "human"
}

// Episode is one completed task or turn — the unit of long-term episodic
// memory. One JSON object per line in .slmcode/memory/episodes.jsonl.
type Episode struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	RunID    string    `json:"run_id,omitempty"`
	Query    string    `json:"query"`
	Summary  string    `json:"summary,omitempty"`
	Plan     []string  `json:"plan,omitempty"`
	Language string    `json:"language,omitempty"`
	Model    string    `json:"model,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Role     string    `json:"role,omitempty"`

	FilesChanged []string      `json:"files_changed,omitempty"`
	ToolsUsed    []string      `json:"tools_used,omitempty"`
	Commands     []Command     `json:"commands,omitempty"`
	Failures     []FailureNote `json:"failures,omitempty"`
	Gates        []GateOutcome `json:"gates,omitempty"`
	Tags         []string      `json:"tags,omitempty"`

	EditFormat     string `json:"edit_format,omitempty"`
	EditsAttempted int    `json:"edits_attempted,omitempty"`
	EditsApplied   int    `json:"edits_applied,omitempty"`
	LLMCalls       int    `json:"llm_calls,omitempty"`
	TokensIn       int    `json:"tokens_in,omitempty"`
	TokensOut      int    `json:"tokens_out,omitempty"`
	WallMS         int64  `json:"wall_ms,omitempty"`
	Retries        int    `json:"retries,omitempty"`

	Success bool   `json:"success"`
	Verdict string `json:"verdict,omitempty"`
}

// Normalize fills defaults and clamps every unbounded field. Always called
// before persisting.
func (e *Episode) Normalize(now time.Time) {
	if e.At.IsZero() {
		e.At = now
	}
	e.At = e.At.UTC().Truncate(time.Second)
	e.Query = clip(e.Query, MaxEpisodeTextLen)
	e.Summary = clip(e.Summary, MaxEpisodeTextLen)
	e.Language = strings.ToLower(strings.TrimSpace(e.Language))
	e.Model = strings.TrimSpace(e.Model)
	e.Provider = strings.TrimSpace(e.Provider)
	e.Role = strings.TrimSpace(e.Role)
	e.EditFormat = strings.TrimSpace(e.EditFormat)

	e.Plan = dedupe(e.Plan, MaxEpisodePlan)
	for i := range e.Plan {
		e.Plan[i] = clip(e.Plan[i], 160)
	}
	e.FilesChanged = dedupe(e.FilesChanged, MaxEpisodeFiles)
	e.ToolsUsed = dedupe(e.ToolsUsed, MaxEpisodeTools)
	e.Tags = dedupe(e.Tags, MaxEpisodeTags)

	if len(e.Failures) > MaxEpisodeFailures {
		e.Failures = e.Failures[:MaxEpisodeFailures]
	}
	for i := range e.Failures {
		e.Failures[i].Message = clip(e.Failures[i].Message, 240)
		e.Failures[i].Resolution = clip(e.Failures[i].Resolution, 240)
	}
	if len(e.Gates) > MaxEpisodeGates {
		e.Gates = e.Gates[:MaxEpisodeGates]
	}
	for i := range e.Gates {
		e.Gates[i].Detail = clip(e.Gates[i].Detail, 200)
	}
	if len(e.Commands) > MaxEpisodeCommands {
		e.Commands = e.Commands[len(e.Commands)-MaxEpisodeCommands:]
	}
	for i := range e.Commands {
		e.Commands[i].Cmd = firstLine(e.Commands[i].Cmd, 160)
	}
	if e.Verdict == "" {
		switch {
		case e.Success:
			e.Verdict = VerdictSuccess
		case len(e.FilesChanged) > 0:
			e.Verdict = VerdictPartial
		default:
			e.Verdict = VerdictFailure
		}
	}
	if e.ID == "" {
		e.ID = hashID("ep_", e.At.Format(time.RFC3339Nano), e.Query, strings.Join(e.FilesChanged, ","))
	}
}

// GatesPassed returns how many gates passed out of how many ran.
func (e Episode) GatesPassed() (passed, total int) {
	for _, g := range e.Gates {
		total++
		if g.Passed {
			passed++
		}
	}
	return passed, total
}

// indexEntry is the searchable projection of an episode. Recall runs against
// these, so a query never has to parse the full JSONL.
type indexEntry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Offset   int64     `json:"offset"`
	Query    string    `json:"query,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Files    []string  `json:"files,omitempty"`
	Tools    []string  `json:"tools,omitempty"`
	Tags     []string  `json:"tags,omitempty"`
	Language string    `json:"language,omitempty"`
	Model    string    `json:"model,omitempty"`
	Success  bool      `json:"success,omitempty"`
	Failures int       `json:"failures,omitempty"`

	doc *document // lazily built token bag, never serialized
}

func entryOf(e Episode, offset int64) indexEntry {
	return indexEntry{
		ID: e.ID, At: e.At, Offset: offset,
		Query: e.Query, Summary: e.Summary,
		Files: e.FilesChanged, Tools: e.ToolsUsed, Tags: e.Tags,
		Language: e.Language, Model: e.Model, Success: e.Success,
		Failures: len(e.Failures),
	}
}

type indexFile struct {
	Version int          `json:"version"`
	Updated time.Time    `json:"updated"`
	Entries []indexEntry `json:"entries"`
}

// Episodes is the append-only episodic store.
type Episodes struct {
	mu       sync.RWMutex
	dir      string // "" ⇒ in-memory only
	max      int
	entries  []indexEntry
	byID     map[string]int
	warnings []string
	dirty    bool
	now      func() time.Time
}

func openEpisodes(dir string, max int, now func() time.Time) *Episodes {
	if max <= 0 {
		max = DefaultMaxEpisodes
	}
	if now == nil {
		now = time.Now
	}
	ep := &Episodes{dir: dir, max: max, byID: map[string]int{}, now: now}
	ep.load()
	return ep
}

func (s *Episodes) logPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "episodes.jsonl")
}

func (s *Episodes) indexPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "episodes.index.json")
}

// load reads the index, verifying it against the log. A missing, truncated or
// hand-edited index is silently rebuilt from the log — never fatal.
func (s *Episodes) load() {
	if s.dir == "" {
		return
	}
	if data, err := os.ReadFile(s.indexPath()); err == nil { //nolint:gosec // path derived from the caller's own memory dir
		var idx indexFile
		if json.Unmarshal(data, &idx) == nil && len(idx.Entries) > 0 {
			s.setEntries(idx.Entries)
			if s.indexLooksValid() {
				return
			}
			s.warnings = append(s.warnings, "episode index stale; rebuilt from log")
		}
	}
	s.rebuild()
}

// indexLooksValid spot-checks the newest entry's offset against the log. A
// full verification would defeat the point of having an index.
func (s *Episodes) indexLooksValid() bool {
	if len(s.entries) == 0 {
		return true
	}
	last := s.entries[len(s.entries)-1]
	got, ok := s.readAt(last.Offset)
	return ok && got.ID == last.ID
}

func (s *Episodes) rebuild() {
	path := s.logPath()
	if path == "" {
		return
	}
	f, err := os.Open(path) //nolint:gosec // path derived from the caller's own memory dir
	if err != nil {
		s.setEntries(nil)
		return
	}
	defer func() { _ = f.Close() }()

	var (
		entries []indexEntry
		offset  int64
		corrupt int
	)
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			start := offset
			offset += int64(len(line))
			var e Episode
			if json.Unmarshal(trimLine(line), &e) == nil && e.ID != "" {
				entries = append(entries, entryOf(e, start))
			} else if strings.TrimSpace(string(line)) != "" {
				corrupt++
			}
		}
		if err != nil {
			break
		}
	}
	if corrupt > 0 {
		s.warnings = append(s.warnings, "skipped "+itoa(corrupt)+" corrupt episode record(s)")
	}
	s.setEntries(entries)
	s.dirty = true
}

func (s *Episodes) setEntries(entries []indexEntry) {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At.Before(entries[j].At) })
	s.entries = entries
	s.byID = make(map[string]int, len(entries))
	for i, e := range entries {
		s.byID[e.ID] = i
	}
}

// Append persists one episode and updates the index.
func (s *Episodes) Append(e Episode) (Episode, error) {
	e.Normalize(s.now())
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.byID[e.ID]; dup {
		return e, nil
	}
	offset := int64(-1)
	if path := s.logPath(); path != "" {
		data, err := json.Marshal(e)
		if err != nil {
			return e, err
		}
		data = append(data, '\n')
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return e, err
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path derived from the caller's own memory dir
		if err != nil {
			return e, err
		}
		if info, statErr := f.Stat(); statErr == nil {
			offset = info.Size()
		}
		// A single Write of a sub-PIPE_BUF line is atomic on POSIX, so a
		// crashed run leaves whole records, never a spliced one.
		_, wErr := f.Write(data)
		cErr := f.Close()
		if err := errors.Join(wErr, cErr); err != nil {
			return e, err
		}
	}
	idx := len(s.entries)
	s.entries = append(s.entries, entryOf(e, offset))
	s.byID[e.ID] = idx
	s.dirty = true
	if s.max > 0 && len(s.entries) > s.max*2 {
		// Hard backstop so a run that never calls Prune still cannot blow up.
		s.pruneLocked(PrunePolicy{MaxEpisodes: s.max})
	}
	return e, nil
}

// Count returns how many episodes are indexed.
func (s *Episodes) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Get hydrates one episode by id.
func (s *Episodes) Get(id string) (Episode, bool) {
	s.mu.RLock()
	i, ok := s.byID[id]
	var entry indexEntry
	if ok {
		entry = s.entries[i]
	}
	s.mu.RUnlock()
	if !ok {
		return Episode{}, false
	}
	return s.hydrate(entry)
}

// Recent returns the n most recent episodes, newest first.
func (s *Episodes) Recent(n int) []Episode {
	s.mu.RLock()
	entries := append([]indexEntry(nil), s.entries...)
	s.mu.RUnlock()
	out := make([]Episode, 0, n)
	for i := len(entries) - 1; i >= 0 && (n <= 0 || len(out) < n); i-- {
		if e, ok := s.hydrate(entries[i]); ok {
			out = append(out, e)
		}
	}
	return out
}

// All returns every stored episode, oldest first. Bounded by the store cap.
func (s *Episodes) All() []Episode {
	s.mu.RLock()
	entries := append([]indexEntry(nil), s.entries...)
	s.mu.RUnlock()
	out := make([]Episode, 0, len(entries))
	for _, en := range entries {
		if e, ok := s.hydrate(en); ok {
			out = append(out, e)
		}
	}
	return out
}

func (s *Episodes) hydrate(en indexEntry) (Episode, bool) {
	if s.dir == "" {
		// In-memory mode keeps only the searchable projection.
		return Episode{
			ID: en.ID, At: en.At, Query: en.Query, Summary: en.Summary,
			FilesChanged: en.Files, ToolsUsed: en.Tools, Tags: en.Tags,
			Language: en.Language, Model: en.Model, Success: en.Success,
		}, true
	}
	if en.Offset >= 0 {
		if e, ok := s.readAt(en.Offset); ok && e.ID == en.ID {
			return e, true
		}
	}
	// Offsets drifted (hand-edited log): fall back to a scan for this id.
	return s.scanFor(en.ID)
}

func (s *Episodes) readAt(offset int64) (Episode, bool) {
	path := s.logPath()
	if path == "" || offset < 0 {
		return Episode{}, false
	}
	f, err := os.Open(path) //nolint:gosec // path derived from the caller's own memory dir
	if err != nil {
		return Episode{}, false
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Episode{}, false
	}
	line, err := readLine(bufio.NewReaderSize(f, 64*1024))
	if len(line) == 0 && err != nil {
		return Episode{}, false
	}
	var e Episode
	if json.Unmarshal(trimLine(line), &e) != nil {
		return Episode{}, false
	}
	return e, e.ID != ""
}

func (s *Episodes) scanFor(id string) (Episode, bool) {
	path := s.logPath()
	if path == "" {
		return Episode{}, false
	}
	f, err := os.Open(path) //nolint:gosec // path derived from the caller's own memory dir
	if err != nil {
		return Episode{}, false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			var e Episode
			if json.Unmarshal(trimLine(line), &e) == nil && e.ID == id {
				return e, true
			}
		}
		if err != nil {
			return Episode{}, false
		}
	}
}

// Flush writes the index if it changed.
func (s *Episodes) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *Episodes) flushLocked() error {
	if s.dir == "" || !s.dirty {
		return nil
	}
	idx := indexFile{Version: 1, Updated: s.now().UTC(), Entries: s.entries}
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

// Warnings returns non-fatal problems encountered while loading.
func (s *Episodes) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

// readLine reads one '\n'-terminated line, refusing absurdly long ones so a
// corrupt file cannot exhaust memory.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > MaxEpisodeLineLen {
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

func trimLine(b []byte) []byte {
	return []byte(strings.TrimRight(string(b), "\r\n"))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
