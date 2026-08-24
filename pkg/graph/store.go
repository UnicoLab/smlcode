package graph

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

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// On-disk layout and bounds.
const (
	// DirName is the directory this package owns under .slmcode/.
	DirName = "graph"
	// LogFileName is the append-only edge log.
	LogFileName = "edges.jsonl"
	// IndexFileName is the rebuildable adjacency index.
	IndexFileName = "edges.index.json"
	// DefaultMaxEdges is the shipped ceiling on stored edges.
	DefaultMaxEdges = 20000

	indexVersion = 1
)

// Options configures OpenWith.
type Options struct {
	// ReadOnly opens the store without ever writing: no directory creation, no
	// appends, no index flush, no corrupt-file quarantine. For inspection and CI.
	ReadOnly bool
	// MaxEdges bounds the store. Zero uses DefaultMaxEdges.
	MaxEdges int
	// Now is injectable for deterministic tests.
	Now func() time.Time
}

// indexEntry is one edge plus where its line starts in the log. The projection
// is lossless — unlike an episode, an edge is small enough that splitting it
// between index and log would buy nothing and cost a seek per lookup. The
// offset is still kept, because it is what makes a stale index detectable and
// a prune able to rewrite the log.
type indexEntry struct {
	Edge
	Offset int64 `json:"offset"`
}

type indexFile struct {
	Version int          `json:"version"`
	Updated time.Time    `json:"updated"`
	Entries []indexEntry `json:"entries"`
	// Out and In are adjacency lists (node id → positions in Entries). They are
	// written so the index is directly greppable — `jq '.in["file:pkg/x.go"]'`
	// answers "what points at this file" — and recomputed from Entries on load,
	// which is O(n) with no I/O and cannot disagree with the entries it is
	// built from.
	Out map[string][]int `json:"out,omitempty"`
	In  map[string][]int `json:"in,omitempty"`
}

// Store is the edge index.
//
// A Store is always usable. Open never fails because of a corrupt or
// unwritable store: it degrades to in-memory operation and records the problem
// in Warnings. The graph is derived data — losing it costs a Backfill, and
// must never cost a run.
type Store struct {
	mu       sync.RWMutex
	dir      string // "" ⇒ in-memory only
	readOnly bool
	max      int
	now      func() time.Time

	entries  []indexEntry
	byID     map[string]int
	out      map[string][]int
	in       map[string][]int
	warnings []string
	dirty    bool
}

// Open opens (or creates) the edge index at <root>/.slmcode/graph.
//
// An empty root yields a fully in-memory store, which is the correct behavior
// for slmcode invoked outside a workspace.
func Open(root string) (*Store, error) { return OpenWith(root, Options{}) }

// OpenWith is Open with explicit options.
func OpenWith(root string, opt Options) (*Store, error) {
	max := opt.MaxEdges
	if max <= 0 {
		max = DefaultMaxEdges
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	s := &Store{
		readOnly: opt.ReadOnly,
		max:      max,
		now:      now,
		byID:     map[string]int{},
		out:      map[string][]int{},
		in:       map[string][]int{},
	}
	if root = strings.TrimSpace(root); root != "" {
		s.dir = filepath.Join(root, memory.SlmDirName, DirName)
		if err := s.ensure(); err != nil {
			s.warnings = append(s.warnings, "graph disabled: "+err.Error())
			s.dir = ""
		}
	}
	s.load()
	return s, nil
}

func (s *Store) ensure() error {
	if s.dir == "" || s.readOnly {
		return nil
	}
	return os.MkdirAll(s.dir, 0o750)
}

// Dir returns the store directory ("" when the store is in-process only).
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

func (s *Store) logPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, LogFileName)
}

func (s *Store) indexPath() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, IndexFileName)
}

// load reads the index and verifies it against the log. A missing, corrupt,
// truncated or hand-edited index is rebuilt from the log — never fatal.
func (s *Store) load() {
	if s.dir == "" {
		return
	}
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		s.rebuild()
		return
	}
	var idx indexFile
	if err := json.Unmarshal(data, &idx); err != nil {
		// Corrupt store: preserve the bad file rather than silently destroying
		// it, and rebuild from the log, which is the source of truth anyway.
		s.warnings = append(s.warnings, IndexFileName+" unreadable; moved aside and rebuilt from log")
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
		s.warnings = append(s.warnings, "graph index stale; rebuilt from log")
		s.rebuild()
	}
}

// quarantine moves a file aside to <name>.corrupt. Never in read-only mode:
// an inspection must not mutate the store it is inspecting.
func (s *Store) quarantine(path string) {
	if path == "" || s.readOnly {
		return
	}
	_ = os.Rename(path, path+".corrupt")
}

// indexLooksValid spot-checks the newest entry's offset against the log. A
// full verification would defeat the point of having an index.
func (s *Store) indexLooksValid() bool {
	if len(s.entries) == 0 {
		return true
	}
	last := s.entries[len(s.entries)-1]
	got, ok := s.readAt(last.Offset)
	return ok && got.ID() == last.ID()
}

// rebuild reconstructs the whole index by scanning the log.
func (s *Store) rebuild() {
	s.entries = nil
	s.byID = map[string]int{}
	s.out = map[string][]int{}
	s.in = map[string][]int{}

	path := s.logPath()
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	var (
		offset  int64
		corrupt int
	)
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			start := offset
			offset += int64(len(line))
			var e Edge
			if json.Unmarshal(trimLine(line), &e) == nil && e.Validate() == nil {
				// Same insert path as Add, so a rebuilt index and an appended
				// one cannot disagree about dedup or ordering.
				s.insertLocked(e, start)
			} else if strings.TrimSpace(string(line)) != "" {
				corrupt++
			}
		}
		if err != nil {
			break
		}
	}
	if corrupt > 0 {
		s.warnings = append(s.warnings, "skipped "+strconv.Itoa(corrupt)+" corrupt edge record(s)")
	}
	s.dirty = true
}

// setEntries installs a set of entries and rebuilds the adjacency maps from
// them. Deduplicates on the way in, so a hand-edited index cannot introduce a
// duplicate the rest of the package assumes cannot exist.
func (s *Store) setEntries(entries []indexEntry) {
	s.entries = make([]indexEntry, 0, len(entries))
	s.byID = make(map[string]int, len(entries))
	s.out = make(map[string][]int, len(entries))
	s.in = make(map[string][]int, len(entries))
	for _, en := range entries {
		if en.Validate() != nil {
			continue
		}
		if _, dup := s.byID[en.ID()]; dup {
			continue
		}
		i := len(s.entries)
		s.entries = append(s.entries, en)
		s.byID[en.ID()] = i
		s.out[en.From] = append(s.out[en.From], i)
		s.in[en.To] = append(s.in[en.To], i)
	}
}

// insertLocked adds or refreshes one already-normalized edge. Returns true
// when the edge was new.
func (s *Store) insertLocked(e Edge, offset int64) bool {
	id := e.ID()
	if i, ok := s.byID[id]; ok {
		// Re-observation: keep the newest sighting and the latest metadata.
		cur := &s.entries[i]
		if e.At.After(cur.At) {
			cur.At = e.At
		}
		if e.Confidence > 0 {
			cur.Confidence = e.Confidence
		}
		if e.RunID != "" {
			cur.RunID = e.RunID
		}
		if e.Note != "" {
			cur.Note = e.Note
		}
		s.dirty = true
		return false
	}
	i := len(s.entries)
	s.entries = append(s.entries, indexEntry{Edge: e, Offset: offset})
	s.byID[id] = i
	s.out[e.From] = append(s.out[e.From], i)
	s.in[e.To] = append(s.in[e.To], i)
	s.dirty = true
	return true
}

// Add appends edges, deduplicated by content address.
//
// Re-adding an edge that already exists refreshes its timestamp, confidence,
// run id and note in place instead of growing the log: the log records WHICH
// edges exist, the index carries the freshest metadata about them. (A rebuild
// from the log therefore falls back to the last logged metadata, which the
// next Add re-freshens. That is the price of an append-only log that does not
// grow when nothing new happened, and it is the right trade: the edge set is
// the load-bearing part, the timestamps are decoration.)
//
// Blank and self-referential edges are skipped and named in the returned
// error; the valid edges in the same batch are still stored, because one bad
// edge from a caller must not lose the good ones. A read-only store accepts
// the call and does nothing.
func (s *Store) Add(edges ...Edge) error {
	_, err := s.add(edges)
	return err
}

// add is Add plus the count of edges that were not already present.
func (s *Store) add(edges []Edge) (int, error) {
	if s == nil || len(edges) == 0 || s.readOnly {
		return 0, nil
	}
	now := s.now()
	var (
		valid []Edge
		errs  []error
	)
	for _, e := range edges {
		e.Normalize(now)
		if err := e.Validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		valid = append(valid, e)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	added := 0
	for _, e := range valid {
		if _, known := s.byID[e.ID()]; known {
			s.insertLocked(e, -1)
			continue
		}
		offset := int64(-1)
		if path := s.logPath(); path != "" {
			off, err := s.appendLine(path, e)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			offset = off
		}
		if s.insertLocked(e, offset) {
			added++
		}
	}
	if s.max > 0 && len(s.entries) > s.max*2 {
		// Hard backstop so a caller that never prunes still cannot blow up.
		s.pruneLocked(PrunePolicy{MaxEdges: s.max, MaxAge: -1})
	}
	return added, errors.Join(errs...)
}

// appendLine writes one record and returns the offset it was written at.
func (s *Store) appendLine(path string, e Edge) (int64, error) {
	data, err := json.Marshal(e)
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

// readAt reads the edge whose record starts at offset.
func (s *Store) readAt(offset int64) (Edge, bool) {
	path := s.logPath()
	if path == "" || offset < 0 {
		return Edge{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return Edge{}, false
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Edge{}, false
	}
	line, err := readLine(bufio.NewReaderSize(f, 64*1024))
	if len(line) == 0 && err != nil {
		return Edge{}, false
	}
	var e Edge
	if json.Unmarshal(trimLine(line), &e) != nil {
		return Edge{}, false
	}
	return e, e.Validate() == nil
}

// Out returns the edges leaving node, optionally filtered by edge type.
// Ordered by (target, type) so the answer never depends on insertion order.
func (s *Store) Out(node string, types ...string) []Edge {
	return s.adjacent(node, Outgoing, typeSet(types))
}

// In returns the edges arriving at node, optionally filtered by edge type.
// Ordered by (source, type).
func (s *Store) In(node string, types ...string) []Edge {
	return s.adjacent(node, Incoming, typeSet(types))
}

// adjacent is the one place that reads the adjacency lists. Results are copies
// and are sorted by the far end then the type, which makes every traversal
// reproducible regardless of the order the edges were written in.
func (s *Store) adjacent(node string, dir Direction, types map[string]bool) []Edge {
	if s == nil {
		return nil
	}
	node = strings.TrimSpace(node)
	if node == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var positions []int
	if dir == Outgoing || dir == Either {
		positions = append(positions, s.out[node]...)
	}
	if dir == Incoming || dir == Either {
		positions = append(positions, s.in[node]...)
	}
	out := make([]Edge, 0, len(positions))
	for _, i := range positions {
		if i < 0 || i >= len(s.entries) {
			continue
		}
		e := s.entries[i].Edge
		if types != nil && !types[e.Type] {
			continue
		}
		out = append(out, e)
	}
	sortEdges(out, node)
	return out
}

// sortEdges orders edges by the end that is not `from`, then by type, then by
// the near end (which differs only for Either).
func sortEdges(edges []Edge, from string) {
	sort.SliceStable(edges, func(i, j int) bool {
		a, _ := edges[i].Other(from)
		b, _ := edges[j].Other(from)
		if a != b {
			return a < b
		}
		if edges[i].Type != edges[j].Type {
			return edges[i].Type < edges[j].Type
		}
		return edges[i].From < edges[j].From
	})
}

// Neighbors returns the distinct nodes one hop from node, sorted.
func (s *Store) Neighbors(node string, dir Direction, types ...string) []string {
	edges := s.adjacent(node, dir, typeSet(types))
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		if other, ok := e.Other(node); ok {
			out = append(out, other)
		}
	}
	return sortedUnique(out)
}

// Get returns one edge by content address.
func (s *Store) Get(id string) (Edge, bool) {
	if s == nil {
		return Edge{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.byID[id]
	if !ok {
		return Edge{}, false
	}
	return s.entries[i].Edge, true
}

// Has reports whether an edge with these ends and type is stored.
func (s *Store) Has(from, to, edgeType string) bool {
	probe := Edge{From: from, To: to, Type: edgeType}
	probe.Normalize(time.Time{})
	_, ok := s.Get(probe.ID())
	return ok
}

// All returns every stored edge, in insertion order. Bounded by the cap.
func (s *Store) All() []Edge {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Edge, 0, len(s.entries))
	for _, en := range s.entries {
		out = append(out, en.Edge)
	}
	return out
}

// Count returns how many edges are indexed.
func (s *Store) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Stats summarizes the store.
type Stats struct {
	Edges  int            `json:"edges"`
	Nodes  int            `json:"nodes"`
	ByType map[string]int `json:"by_type,omitempty"`
	// Bytes is the size of edges.jsonl on disk, 0 for an in-memory store.
	Bytes int64 `json:"bytes"`
}

// Stats reports counts by edge type, the distinct node count and the log size.
func (s *Store) Stats() Stats {
	if s == nil {
		return Stats{}
	}
	s.mu.RLock()
	st := Stats{Edges: len(s.entries), ByType: map[string]int{}}
	nodes := make(map[string]bool, len(s.entries)*2)
	for _, en := range s.entries {
		st.ByType[en.Type]++
		nodes[en.From] = true
		nodes[en.To] = true
	}
	st.Nodes = len(nodes)
	path := s.logPath()
	s.mu.RUnlock()

	if path != "" {
		if info, err := os.Stat(path); err == nil {
			st.Bytes = info.Size()
		}
	}
	return st
}

// Flush writes the index if it changed. The log is already durable; the index
// is a cache, so failing to write it costs a rebuild, never data.
func (s *Store) Flush() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *Store) flushLocked() error {
	if s.dir == "" || s.readOnly || !s.dirty {
		return nil
	}
	idx := indexFile{
		Version: indexVersion,
		Updated: s.now().UTC(),
		Entries: s.entries,
		Out:     s.out,
		In:      s.in,
	}
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
func (s *Store) Close() error { return s.Flush() }

// Warnings returns non-fatal problems: a corrupt file, a skipped record, an
// unwritable directory. Callers should surface these and never abort over them.
func (s *Store) Warnings() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

// Forget deletes the store's files. Equivalent to `rm -rf .slmcode/graph`,
// which is itself a supported operation — the next Backfill rebuilds every
// edge from the records that implied it.
func Forget(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(root, memory.SlmDirName, DirName))
}

// readLine reads one '\n'-terminated line, refusing absurdly long ones so a
// corrupt file cannot exhaust memory.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > MaxEdgeLineLen {
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
