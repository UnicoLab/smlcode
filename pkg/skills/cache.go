package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Loader.List used to run a full filepath.WalkDir plus a SKILL.md parse over
// every root on EVERY call, and List is called by Get, ResolveForRun,
// MatchForAgent and PackForAgent — 20+ filesystem walks per run. This adds an
// mtime+size-invalidated cache behind a RWMutex.

type cacheEntry struct {
	skills []Skill
	stamp  string // fingerprint of every SKILL.md path+mtime+size under the roots
}

type loaderCache struct {
	mu    sync.RWMutex
	entry *cacheEntry
}

// scan walks the roots once and returns both the parsed skills and a
// fingerprint of the files they came from.
func scanRoots(roots []string) ([]Skill, string) {
	seen := map[string]bool{}
	var out []Skill
	var stamps []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // unreadable subtrees are skipped
			}
			// Skip nested _bundled when walking the project skills root — loaded
			// via its own root. Do not SkipDir when root itself is …/_bundled.
			if d.IsDir() {
				if d.Name() == "_bundled" && filepath.Clean(path) != filepath.Clean(root) {
					return filepath.SkipDir
				}
				return nil
			}
			base := d.Name()
			if !strings.EqualFold(base, "SKILL.md") && !strings.HasSuffix(strings.ToLower(base), ".skill.md") {
				return nil
			}
			if info, ierr := d.Info(); ierr == nil {
				stamps = append(stamps, path+"|"+itoa64(info.ModTime().UnixNano())+"|"+itoa64(info.Size()))
			} else {
				stamps = append(stamps, path+"|?")
			}
			sk, perr := ParseFile(path)
			if perr != nil {
				return nil
			}
			key := strings.ToLower(sk.Name)
			if seen[key] {
				return nil
			}
			seen[key] = true
			out = append(out, sk)
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.Strings(stamps)
	return out, strings.Join(stamps, "\n")
}

// stampRoots computes only the fingerprint (no parsing) so a cache hit costs a
// stat walk instead of a parse walk.
func stampRoots(roots []string) string {
	var stamps []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr
			}
			if d.IsDir() {
				if d.Name() == "_bundled" && filepath.Clean(path) != filepath.Clean(root) {
					return filepath.SkipDir
				}
				return nil
			}
			base := d.Name()
			if !strings.EqualFold(base, "SKILL.md") && !strings.HasSuffix(strings.ToLower(base), ".skill.md") {
				return nil
			}
			if info, ierr := d.Info(); ierr == nil {
				stamps = append(stamps, path+"|"+itoa64(info.ModTime().UnixNano())+"|"+itoa64(info.Size()))
			} else {
				stamps = append(stamps, path+"|?")
			}
			return nil
		})
	}
	sort.Strings(stamps)
	return strings.Join(stamps, "\n")
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// InvalidateCache forces the next List to re-walk and re-parse.
func (l *Loader) InvalidateCache() {
	if l == nil {
		return
	}
	l.cache.mu.Lock()
	l.cache.entry = nil
	l.cache.mu.Unlock()
}
