package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The lost-update test.
//
// Every goroutine does a read-modify-write of ONE file through the real tool
// entrypoint: read the current text, replace the anchor with "anchor + my
// line", write the whole file back. Without the per-path lock, a worker that
// reads between another's read and write computes its replacement from text
// that is about to be thrown away, and that other worker's line vanishes — with
// both calls reporting "edited 1 replacement(s)". -race says nothing about it:
// the collision is on the filesystem, not on memory. The assertion is that all
// N lines survive.
func TestConcurrentEditsOfOnePathAreSerialized(t *testing.T) {
	const workers = 16

	for _, tc := range []struct {
		name  string
		edit  func(w *Workspace, path string, n int) error
		lines func(n int) string
	}{
		{
			name: "ws_edit",
			edit: func(w *Workspace, path string, n int) error {
				_, err := w.editFile(context.Background(), map[string]interface{}{
					"path": path, "old_str": anchor, "new_str": lineFor(n) + "\n" + anchor,
				})
				return err
			},
		},
		{
			name: "ws_patch",
			edit: func(w *Workspace, path string, n int) error {
				_, err := w.patchFile(context.Background(), map[string]interface{}{
					"path": path,
					"patch": "<<<<<<< SEARCH\n" + anchor + "\n=======\n" +
						lineFor(n) + "\n" + anchor + "\n>>>>>>> REPLACE\n",
				})
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			w := &Workspace{Root: root}
			const rel = "notes.txt"
			if err := os.WriteFile(filepath.Join(root, rel), []byte(anchor+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			var wg sync.WaitGroup
			errs := make([]error, workers)
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(n int) {
					defer wg.Done()
					errs[n] = tc.edit(w, rel, n)
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("worker %d: %v", i, err)
				}
			}

			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			var lost []string
			for i := 0; i < workers; i++ {
				if !strings.Contains(text, lineFor(i)) {
					lost = append(lost, lineFor(i))
				}
			}
			if len(lost) > 0 {
				t.Fatalf("lost %d of %d concurrent updates (%s); file:\n%s",
					len(lost), workers, strings.Join(lost, ", "), text)
			}
		})
	}
}

// Different paths must NOT serialize against each other — the lock is per file,
// and a wave whose workers touch disjoint files has to stay parallel.
func TestPathLocksAreIndependentPerPath(t *testing.T) {
	w := &Workspace{Root: t.TempDir()}
	releaseA := w.lockPath("a/one.go")
	defer releaseA()

	done := make(chan struct{})
	go func() {
		w.lockPath("a/two.go")()
		close(done)
	}()
	<-done // hangs (and fails the run by timeout) if the lock were global
}

// The key is the file, not the spelling: an agent writes "./a.go" as readily as
// "a.go", and both must contend on one mutex.
func TestPathLockKeyIsCleaned(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a.go", "a.go"},
		{"./a.go", "a.go"},
		{"x/../a.go", "a.go"},
		{"pkg//foo/bar.go", "pkg/foo/bar.go"},
		{"src/", "src"},
	} {
		if got := pathLockKey(tc.in); got != tc.want {
			t.Fatalf("pathLockKey(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
	w := &Workspace{Root: t.TempDir()}
	if w.pathMutex(pathLockKey("./a.go")) != w.pathMutex(pathLockKey("a.go")) {
		t.Fatal("./a.go and a.go must take the same mutex")
	}
}

// ws_mv holds two locks at once, so two crossing moves must not deadlock. The
// sorted acquisition order is the only thing preventing it.
func TestLockPathsOrdersAcquisitionAndHandlesSelf(t *testing.T) {
	w := &Workspace{Root: t.TempDir()}
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				if n%2 == 0 {
					w.lockPaths("a.go", "b.go")()
					return
				}
				w.lockPaths("b.go", "a.go")()
			}(i)
		}
		wg.Wait()
		// Same path both ends: taking a non-reentrant mutex twice would hang.
		w.lockPaths("a.go", "./a.go")()
		close(done)
	}()
	<-done
}

const anchor = "<<END>>"

func lineFor(n int) string { return fmt.Sprintf("line-%02d", n) }
