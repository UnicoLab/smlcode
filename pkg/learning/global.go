package learning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/memory"
)

// GlobalMemoryPaths returns user-level memory locations. The first path is the
// write target; the others are read-only fallbacks kept for config-dir parity.
func GlobalMemoryPaths() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".slmcode", "MEMORY.md"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "slmcode", "MEMORY.md"))
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".config", "slmcode", "MEMORY.md"))
	}
	return uniqPaths(paths)
}

func ReadGlobalMemory() string {
	var b strings.Builder
	for _, path := range GlobalMemoryPaths() {
		data, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(string(data))
	}
	return b.String()
}

func AppendGlobalMemory(sectionTitle, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	paths := GlobalMemoryPaths()
	if len(paths) == 0 {
		return nil
	}
	path := paths[0]
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { // ~/.slmcode global memory, owner-only
		return err
	}
	existing, _ := os.ReadFile(path)
	if len(existing) == 0 {
		existing = []byte("# SLMCode Global Memory\n\n## Lessons\n\n")
	}
	stamp := time.Now().Format(time.RFC3339)
	block := fmt.Sprintf("\n\n## %s (%s)\n\n%s\n", sectionTitle, stamp, body)
	return atomicfile.Write(path, append(existing, []byte(block)...), 0o644)
}

// Adaptive-memory bounds. These are the prompt budget, unchanged from when the
// selection was a keyword allowlist: this is a relevance change, not a size one.
const (
	maxAdaptiveLines = 10
	maxAdaptiveBytes = 1600
)

// RecentAdaptiveMemory extracts compact high-signal lessons from project and
// global memory for injection into lean worker/corrector packs.
//
// This is the unconditioned form, for callers with no task text in hand. Prefer
// RecentAdaptiveMemoryFor, which ranks the same lines against the task instead
// of falling back to recency.
func RecentAdaptiveMemory(projectMemory, globalMemory string, maxBytes int) string {
	return RecentAdaptiveMemoryFor("", projectMemory, globalMemory, maxBytes)
}

// RecentAdaptiveMemoryFor selects the lessons most relevant to query from
// project and global memory.
//
// What this replaced, and why: selection used to be an eleven-substring
// allowlist (timeout, timed out, deadline, max_parallel, contention, smoke,
// qa_gate, acceptance, placeholder, stub, max retries). Any lesson not
// containing one of those words was discarded on the ONLY path from stored
// memory to a future prompt — measured against this repo's own MEMORY.md, four
// of seven lessons were silently thrown away. Worse, the list was a guess at
// what mattered, frozen at the moment it was typed: a project whose real
// recurring problem is flaky fixtures or an import cycle learned nothing,
// forever, because nobody had thought of those words.
//
// Relevance now comes from memory.RankText — the same BM25F scorer, coverage
// gate, relative floor and recency decay that rank episodes, already tested and
// tuned. Lines that match the task come first, in relevance order; the
// remaining slots are filled with the most recent lines so the block is never
// emptier than it used to be. With no query the fallback is pure recency, which
// is deterministic and needs no model.
func RecentAdaptiveMemoryFor(query, projectMemory, globalMemory string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = maxAdaptiveBytes
	}
	cands := appendAdaptiveLines(nil, globalMemory, "global")
	cands = appendAdaptiveLines(cands, projectMemory, "project")
	cands = dedupeAdaptive(cands)
	if len(cands) == 0 {
		return ""
	}

	order := rankAdaptive(query, cands)
	if len(order) > maxAdaptiveLines {
		order = order[:maxAdaptiveLines]
	}
	// Trim by dropping the least valuable line — the tail of the importance
	// order — rather than slicing bytes out of the middle of a sentence.
	for len(order) > 0 && adaptiveBytes(cands, order) > maxBytes {
		order = order[:len(order)-1]
	}
	if len(order) == 0 {
		return ""
	}
	if query == "" {
		// No ranking happened: keep the chronological reading order.
		sort.Ints(order)
	}
	out := make([]string, 0, len(order))
	for _, i := range order {
		out = append(out, cands[i].render())
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// adaptiveLine is one candidate lesson line with the provenance needed to rank
// it: which store it came from and, when the bullet recorded one, when.
type adaptiveLine struct {
	source string
	text   string
	at     time.Time
}

func (l adaptiveLine) render() string {
	return fmt.Sprintf("- %s memory: %s", l.source, l.text)
}

func appendAdaptiveLines(out []adaptiveLine, raw, source string) []adaptiveLine {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		body, fields := splitProvenance(line)
		body = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(body), "-* !+>~"))
		if body == "" {
			continue
		}
		l := adaptiveLine{source: source, text: body}
		if at, err := time.Parse(time.RFC3339, fields["at"]); err == nil {
			l.at = at
		}
		out = append(out, l)
	}
	return out
}

// rankAdaptive returns candidate indices in the order they should be offered to
// a prompt: matched-and-most-relevant first, then the most recent of whatever
// did not match. With no query it is pure recency (the tail of the file).
func rankAdaptive(query string, cands []adaptiveLine) []int {
	recent := make([]int, len(cands))
	for i := range cands {
		recent[i] = len(cands) - 1 - i // newest lines are last in the file
	}
	if strings.TrimSpace(query) == "" {
		return recent
	}
	texts := make([]memory.TextCandidate, len(cands))
	for i, c := range cands {
		texts[i] = memory.TextCandidate{Text: c.text, At: c.at}
	}
	hits := memory.RankText(memory.Query{Text: query}, texts, 0)
	if len(hits) == 0 {
		return recent
	}
	order := make([]int, 0, len(cands))
	seen := make(map[int]bool, len(hits))
	for _, h := range hits {
		order = append(order, h.Index)
		seen[h.Index] = true
	}
	for _, i := range recent {
		if !seen[i] {
			order = append(order, i)
		}
	}
	return order
}

func adaptiveBytes(cands []adaptiveLine, order []int) int {
	n := 0
	for i, idx := range order {
		if i > 0 {
			n++ // the joining newline
		}
		n += len(cands[idx].render())
	}
	return n
}

func dedupeAdaptive(in []adaptiveLine) []adaptiveLine {
	seen := make(map[string]bool, len(in))
	out := make([]adaptiveLine, 0, len(in))
	for _, l := range in {
		key := l.source + "\x00" + l.text
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, l)
	}
	return out
}

func uniqPaths(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "." || p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
