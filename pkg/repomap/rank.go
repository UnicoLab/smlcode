package repomap

import "strings"

// PageRank parameters. 12 iterations is well past convergence for the small
// graphs (a few thousand nodes) a single repository produces.
const (
	damping        = 0.85
	rankIterations = 12
)

// rank builds the file reference graph and runs a PageRank-style pass.
//
// Edge A→B exists when file A references an identifier that file B defines, or
// when A imports a path that resolves to B's directory. Edge weight is the
// number of distinct referenced symbols, divided across all files defining that
// symbol so a name defined in ten places does not dominate.
func (m *Map) rank() {
	n := len(m.Files)
	if n == 0 {
		return
	}
	out := make([]map[int]float64, n)
	for i := range out {
		out[i] = map[int]float64{}
	}

	dirIndex := map[string][]int{}
	for i, f := range m.Files {
		dir := f.Path
		if j := strings.LastIndexByte(dir, '/'); j >= 0 {
			dir = dir[:j]
		} else {
			dir = "."
		}
		dirIndex[dir] = append(dirIndex[dir], i)
		if f.Package != "" {
			dirIndex["pkg:"+f.Package] = append(dirIndex["pkg:"+f.Package], i)
		}
	}

	for i, f := range m.Files {
		for _, ref := range f.Refs {
			targets := m.defs[ref]
			if len(targets) == 0 || len(targets) > 8 {
				continue // undefined here, or too ambiguous to be signal
			}
			w := 1.0 / float64(len(targets))
			for _, t := range targets {
				if t == i {
					continue
				}
				out[i][t] += w
			}
		}
		// Import edges: a Go import path ending in a known directory, or a JS
		// relative import, links the whole target directory.
		for _, imp := range f.Imports {
			for _, t := range resolveImport(imp, f.Path, dirIndex) {
				if t != i {
					out[i][t] += 0.5
				}
			}
		}
	}

	rank := make([]float64, n)
	next := make([]float64, n)
	for i := range rank {
		rank[i] = 1.0 / float64(n)
	}
	for iter := 0; iter < rankIterations; iter++ {
		for i := range next {
			next[i] = (1 - damping) / float64(n)
		}
		for i := 0; i < n; i++ {
			total := 0.0
			for _, w := range out[i] {
				total += w
			}
			if total == 0 {
				// Dangling node: spread evenly.
				share := damping * rank[i] / float64(n)
				for j := range next {
					next[j] += share
				}
				continue
			}
			for j, w := range out[i] {
				next[j] += damping * rank[i] * w / total
			}
		}
		copy(rank, next)
	}

	maxRank := 0.0
	for _, r := range rank {
		if r > maxRank {
			maxRank = r
		}
	}
	for i := range m.Files {
		r := rank[i]
		if maxRank > 0 {
			r /= maxRank
		}
		// A file with no symbols is worthless in a map regardless of centrality.
		if len(m.Files[i].Symbols) == 0 {
			r *= 0.05
		}
		m.Files[i].Rank = r
		m.Total += r
	}
}

func resolveImport(imp, from string, dirIndex map[string][]int) []int {
	imp = strings.TrimSpace(imp)
	if imp == "" {
		return nil
	}
	if strings.HasPrefix(imp, ".") {
		// JS/TS relative import.
		dir := from
		if j := strings.LastIndexByte(dir, '/'); j >= 0 {
			dir = dir[:j]
		} else {
			dir = "."
		}
		joined := normalizeJoin(dir, imp)
		if idx, ok := dirIndex[joined]; ok {
			return idx
		}
		if j := strings.LastIndexByte(joined, '/'); j >= 0 {
			if idx, ok := dirIndex[joined[:j]]; ok {
				return idx
			}
		}
		return nil
	}
	// Go/Java style: match on the trailing path/package segment.
	last := imp
	for _, sep := range []string{"/", "::", "."} {
		if j := strings.LastIndex(last, sep); j >= 0 {
			last = last[j+len(sep):]
		}
	}
	if idx, ok := dirIndex["pkg:"+last]; ok {
		return idx
	}
	// Try progressively shorter suffixes of the import path against directories.
	parts := strings.Split(imp, "/")
	for i := 0; i < len(parts); i++ {
		cand := strings.Join(parts[i:], "/")
		if idx, ok := dirIndex[cand]; ok {
			return idx
		}
	}
	return nil
}

func normalizeJoin(dir, rel string) string {
	segs := strings.Split(dir, "/")
	if dir == "." || dir == "" {
		segs = nil
	}
	for _, part := range strings.Split(rel, "/") {
		switch part {
		case "", ".":
		case "..":
			if len(segs) > 0 {
				segs = segs[:len(segs)-1]
			}
		default:
			segs = append(segs, part)
		}
	}
	if len(segs) == 0 {
		return "."
	}
	return strings.Join(segs, "/")
}
