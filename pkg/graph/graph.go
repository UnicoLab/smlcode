// Package graph is a traversable edge index over records the harness already
// writes.
//
// slmcode already records episodes, distilled facts, repair rules and
// regression checks, and those records already reference each other: a fact
// names the episodes it was distilled from, a rule names its evidence, an
// episode names the files it changed and the failures it hit. Every one of
// those references is stored as a dead string. Nothing can walk from a file to
// the failure classes it has produced to the rule that fixed them. This
// package materializes those references as typed edges and gives them a
// bounded, deterministic traversal.
//
// It obeys the same rules as pkg/memory and pkg/evolve:
//
//   - Deterministic core, no LLM. Nothing here calls a model. The same store
//     answers the same query identically, in the same order, every time.
//   - No entity resolution. A node is an opaque typed string and edges join
//     exact ids only — no fuzzy matching, no aliasing, no similarity. That is
//     deliberate: a missing edge is a gap, a wrong edge is a lie that gets
//     believed forever.
//   - Bounded and prunable. The store caps at DefaultMaxEdges and Prune
//     rewrites the log so the file actually shrinks.
//   - Safe to be wrong. A corrupt index is moved aside and rebuilt from the
//     log, a corrupt log line is skipped, and problems are reported through
//     Warnings rather than failing a caller.
//   - Inspectable and reversible. Plain JSONL and JSON under .slmcode/graph.
//     `rm -rf .slmcode/graph` is a supported operation: the next Backfill
//     rebuilds every edge from the records that implied it.
package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Node kinds. A node id is "<kind>:<value>": an exact, typed, opaque string.
// Use the constructors below rather than assembling ids by hand.
const (
	NodeFile     = "file"     // file:<repo-relative-path>
	NodeSymbol   = "symbol"   // symbol:<file>#<name>
	NodeEpisode  = "episode"  // episode:<episode id>
	NodeRun      = "run"      // run:<run id>
	NodeTask     = "task"     // task:<run id>/<task id>
	NodeAttempt  = "attempt"  // attempt:<run id>/<task id>/<n>
	NodeRule     = "rule"     // rule:<rule id>
	NodeFact     = "fact"     // fact:<fact id>
	NodeFailure  = "failure"  // failure:<fingerprint>
	NodeCommit   = "commit"   // commit:<sha>
	NodeArtifact = "artifact" // artifact:<path>
)

// Edge types. This is the vocabulary the package speaks. Add accepts any
// non-empty type so a caller can record something new without a release, but
// the query helpers only understand these.
const (
	// DerivedFrom points at where something came from: fact → episode,
	// rule → episode. Follow it backwards to audit a claim.
	DerivedFrom = "derived_from"
	// ResolvedBy points a failure at the rule or decision that fixed it.
	ResolvedBy = "resolved_by"
	// Touched points a record at a file it changed.
	Touched = "touched"
	// Produced points a record at a failure or artifact it created.
	Produced = "produced"
	// Contradicts joins two claims that cannot both hold.
	Contradicts = "contradicts"
	// Supersedes points at the record this one replaces.
	Supersedes = "supersedes"
	// ParentOf is containment: run → task → attempt.
	ParentOf = "parent_of"
	// EvaluatedBy points work at the gate or check that judged it.
	EvaluatedBy = "evaluated_by"
	// DependsOn is a code dependency between files or symbols.
	DependsOn = "depends_on"
	// Mentions is a weaker Touched: referenced, not changed.
	Mentions = "mentions"
)

// Field caps. An edge is a pointer between two records, never a payload: it
// must stay small enough that twenty thousand of them load instantly.
const (
	// MaxNodeLen caps a node id. Node ids are paths, hashes and ids — never
	// prose — so anything longer is a caller bug, not a long name.
	MaxNodeLen = 512
	// MaxNoteLen caps an edge's free-form note.
	MaxNoteLen = 240
	// MaxEdgeLineLen is the longest JSONL line that will be parsed; a longer
	// one is treated as corrupt so a damaged file cannot exhaust memory.
	MaxEdgeLineLen = 32 * 1024
)

// edgeTypes is the known vocabulary, in the order Stats reports it.
var edgeTypes = []string{
	DerivedFrom, ResolvedBy, Touched, Produced, Contradicts,
	Supersedes, ParentOf, EvaluatedBy, DependsOn, Mentions,
}

// EdgeTypes returns the known edge vocabulary in a stable order.
func EdgeTypes() []string { return append([]string(nil), edgeTypes...) }

// Direction selects which way a traversal follows edges.
type Direction int

const (
	// Outgoing follows edges away from the node. The zero value.
	Outgoing Direction = iota
	// Incoming follows edges into the node.
	Incoming
	// Either follows edges in both directions.
	Either
)

// String renders the direction for logs and test failures.
func (d Direction) String() string {
	switch d {
	case Incoming:
		return "incoming"
	case Either:
		return "either"
	case Outgoing:
		return "outgoing"
	default:
		return "outgoing"
	}
}

// Edge is one typed, directed, dated link between two nodes.
//
// Edges are content-addressed on (From, To, Type): re-observing an edge is not
// a new edge, it is the same edge seen again. That is what lets Backfill run
// after every single run without the log growing.
type Edge struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	Type string    `json:"type"`
	At   time.Time `json:"at"`
	// RunID is the run that observed this edge, when one is known.
	RunID string `json:"run_id,omitempty"`
	// Confidence is in [0,1]; 0 means unset, not "certainly false". Edges
	// derived from records the harness itself wrote leave it unset, because
	// they are facts about the log, not beliefs about the world.
	Confidence float64 `json:"confidence,omitempty"`
	// Note is free-form, for humans reading the JSONL.
	Note string `json:"note,omitempty"`
}

// ID is the edge's content address: a hash of (From, To, Type).
func (e Edge) ID() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{e.From, e.To, e.Type}, "\x00")))
	return "e_" + hex.EncodeToString(sum[:])[:12]
}

// Normalize fills defaults and clamps every unbounded field. Always called
// before an edge is stored or hashed, so the content address is stable.
func (e *Edge) Normalize(now time.Time) {
	e.From = clip(strings.TrimSpace(e.From), MaxNodeLen)
	e.To = clip(strings.TrimSpace(e.To), MaxNodeLen)
	e.Type = strings.ToLower(strings.TrimSpace(e.Type))
	e.RunID = clip(strings.TrimSpace(e.RunID), MaxNodeLen)
	e.Note = clip(strings.TrimSpace(e.Note), MaxNoteLen)
	if e.At.IsZero() {
		e.At = now
	}
	e.At = e.At.UTC().Truncate(time.Second)
	switch {
	case e.Confidence < 0:
		e.Confidence = 0
	case e.Confidence > 1:
		e.Confidence = 1
	}
}

// Validate reports why an edge cannot be stored.
func (e Edge) Validate() error {
	switch {
	case e.From == "":
		return fmt.Errorf("graph: edge %q → ? has no source", e.To)
	case e.To == "":
		return fmt.Errorf("graph: edge %q → ? has no target", e.From)
	case e.Type == "":
		return fmt.Errorf("graph: edge %s → %s has no type", e.From, e.To)
	case e.From == e.To:
		// A self edge says nothing and only complicates traversal.
		return fmt.Errorf("graph: self edge on %s", e.From)
	}
	return nil
}

// Other returns the end of the edge that is not node, and whether node was on
// it at all.
func (e Edge) Other(node string) (string, bool) {
	switch node {
	case e.From:
		return e.To, true
	case e.To:
		return e.From, true
	default:
		return "", false
	}
}

// FileNode returns the node id for a repo-relative path.
//
// The path is canonicalized (slashes, "./" prefix, "." segments) but never
// resolved: "pkg/x.go" and "./pkg/x.go" are the same node, "pkg/x.go" and an
// absolute path to the same file are not.
func FileNode(rel string) string { return nodeID(NodeFile, cleanPath(rel)) }

// SymbolNode returns the node id for a named symbol in a file.
func SymbolNode(file, name string) string {
	file, name = cleanPath(file), strings.TrimSpace(name)
	if file == "" || name == "" {
		return ""
	}
	return nodeID(NodeSymbol, file+"#"+name)
}

// EpisodeNode returns the node id for an episodic-memory record.
func EpisodeNode(id string) string { return nodeID(NodeEpisode, id) }

// RunNode returns the node id for a run.
func RunNode(id string) string { return nodeID(NodeRun, id) }

// TaskNode returns the node id for a task within a run.
func TaskNode(runID, taskID string) string {
	runID, taskID = strings.TrimSpace(runID), strings.TrimSpace(taskID)
	if runID == "" || taskID == "" {
		return ""
	}
	return nodeID(NodeTask, runID+"/"+taskID)
}

// AttemptNode returns the node id for the nth attempt at a task.
func AttemptNode(runID, taskID string, n int) string {
	runID, taskID = strings.TrimSpace(runID), strings.TrimSpace(taskID)
	if runID == "" || taskID == "" {
		return ""
	}
	return nodeID(NodeAttempt, runID+"/"+taskID+"/"+strconv.Itoa(n))
}

// RuleNode returns the node id for a repair rule.
func RuleNode(id string) string { return nodeID(NodeRule, id) }

// FactNode returns the node id for a semantic-memory fact.
func FactNode(id string) string { return nodeID(NodeFact, id) }

// FailureNode returns the node id for a failure fingerprint.
func FailureNode(fingerprint string) string { return nodeID(NodeFailure, fingerprint) }

// CommitNode returns the node id for a commit.
func CommitNode(sha string) string { return nodeID(NodeCommit, sha) }

// ArtifactNode returns the node id for a produced artifact.
func ArtifactNode(p string) string { return nodeID(NodeArtifact, cleanPath(p)) }

// NodeKind returns the kind prefix of a node id, or "" if it has none.
func NodeKind(node string) string {
	if i := strings.IndexByte(node, ':'); i > 0 {
		return node[:i]
	}
	return ""
}

// NodeValue returns the part of a node id after its kind prefix. For a string
// with no prefix it returns the string unchanged, so it is safe to call on
// anything.
func NodeValue(node string) string {
	if i := strings.IndexByte(node, ':'); i > 0 {
		return node[i+1:]
	}
	return node
}

// IsKind reports whether node is of the given kind.
func IsKind(node, kind string) bool { return NodeKind(node) == kind }

func nodeID(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return clip(kind+":"+value, MaxNodeLen)
}

// cleanPath canonicalizes a repo-relative path without touching the disk.
func cleanPath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "./")
	if p == "." {
		return ""
	}
	return p
}

// clip shortens s to at most n bytes without splitting a rune.
func clip(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// sortedUnique returns in sorted with blanks and duplicates removed. Nothing
// to return is nil, not an empty slice: "we know of none" reads the same as
// "we have not looked", and callers comparing results should not have to care
// which they got.
func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// typeSet turns a variadic type filter into a lookup set. A nil result means
// "every type", which is what an empty filter asks for.
func typeSet(types []string) map[string]bool {
	if len(types) == 0 {
		return nil
	}
	set := make(map[string]bool, len(types))
	for _, t := range types {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			set[t] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}
