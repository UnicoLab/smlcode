package workspace

import (
	"path/filepath"
	"sync"
)

// pathLocks serializes concurrent writers of the SAME file.
//
// A wave fans out one goroutine per task against ONE working tree, and the
// FocusGuard write allowlist is the UNION of the whole wave's files: it
// constrains the wave against the repo, it does not constrain the workers
// against each other. Two tasks whose file sets overlap therefore ran
// read-modify-write on one path concurrently — ws_edit reads the file, computes
// the replacement from the text it read, and writes the whole file back, so the
// second writer's replacement is derived from content the first writer had
// already replaced. The first worker's edit is simply gone, and nothing reports
// it: both calls answer "edited N replacement(s)".
//
// The lock has to be held across the whole read-modify-write, not just the
// write syscall — the lost update happens in the gap between the two.
//
// pkg/loop's wave admission keeps overlapping tasks out of the same wave in the
// first place; this is the correctness backstop for the contention scheduling
// cannot see, namely the files a worker touches that its task never listed.
type pathLocks struct {
	// cleaned path → *sync.Mutex. sync.Map rather than a guarded map because
	// the access pattern is exactly its second use case: keys written once and
	// read many times, from many goroutines, with no iteration ever. The map
	// only grows — one small entry per path a run wrote — which is bounded by
	// the working tree and freed with the Workspace.
	mus sync.Map
}

// lockPath takes the write lock for one path and returns its release func.
//
// Keyed on the CLEANED, slash-normalized relative path so "./a.go", "a.go" and
// "x/../a.go" contend on one mutex: the key must identify the file, not the
// spelling. Callers reach here after normalizeRelPath, so this only has to
// survive the residue.
func (w *Workspace) lockPath(rel string) func() {
	if w == nil {
		return func() {}
	}
	mu := w.pathMutex(pathLockKey(rel))
	mu.Lock()
	return mu.Unlock
}

// lockPaths takes the write locks for two paths and returns one release func
// for both.
//
// ws_mv is the only two-path write, and two crossing moves (a→b while b→a)
// deadlock if each takes its endpoints in argument order. Acquiring in sorted
// key order is what makes holding two of these locks safe; the same-path case
// (a move onto itself after normalization) takes the mutex once, because
// sync.Mutex is not reentrant.
func (w *Workspace) lockPaths(a, b string) func() {
	if w == nil {
		return func() {}
	}
	ka, kb := pathLockKey(a), pathLockKey(b)
	if ka == kb {
		return w.lockPath(a)
	}
	if kb < ka {
		ka, kb = kb, ka
	}
	first, second := w.pathMutex(ka), w.pathMutex(kb)
	first.Lock()
	second.Lock()
	return func() {
		second.Unlock()
		first.Unlock()
	}
}

func (w *Workspace) pathMutex(key string) *sync.Mutex {
	v, _ := w.pathMu.mus.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func pathLockKey(rel string) string {
	return filepath.ToSlash(filepath.Clean(rel))
}
