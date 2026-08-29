package loop

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/repomap"
)

// Telling a worker what the OTHER tasks in this run have already defined.
//
// One plan is one coherent change, and its tasks routinely depend on each
// other's types. Nothing showed a worker those types. The shared brief lists a
// sibling as a LINE — "T2 done: implemented the in-memory task store
// (pkg/tasks/store.go, types.go)" — which says the file exists and nothing
// about what is in it, so a worker writing against it has to guess.
//
// Measured on a live greenfield Go build: a worker scoped to cmd/server/main.go
// used task.Title, task.CreatedAt and task.UpdatedAt on a Task that a sibling
// task had defined as {ID, Description, Status}. pkg/tasks compiled and its
// tests passed; cmd/server did not build. The task retried to its attempt
// ceiling and was parked, because every retry re-derived the same invented API
// from the same absent information.
//
// Reads were never scope-limited, so the worker COULD have opened the file. The
// prompt now tells it to, and this hands it the answer directly — a small
// model that has the signature in front of it does not have to remember to go
// looking for it.

const (
	// siblingAPIMaxFiles bounds how many sibling files are summarized.
	siblingAPIMaxFiles = 4
	// siblingAPIMaxSymbols bounds the signatures shown per file.
	siblingAPIMaxSymbols = 12
	// siblingAPIMaxBytes is the hard ceiling on the whole section. This
	// competes with the code for a small model's window, so it stays small
	// enough to be worth its place.
	siblingAPIMaxBytes = 1200
)

// siblingAPISection renders the exported API of files other tasks in this run
// own, or "" when there is nothing useful to say.
func (r *Runner) siblingAPISection(board *plan.Board, current plan.Task) string {
	if r == nil || board == nil || strings.TrimSpace(r.Root) == "" {
		return ""
	}
	// Only an implementer needs somebody else's signatures. A tester runs a
	// command; an explorer is looking at the tree already.
	if !isGenericWorkerRole(current.Role) && !strings.Contains(strings.ToLower(current.Role), "worker") {
		return ""
	}
	mine := map[string]bool{}
	for _, f := range current.Files {
		mine[normalizeBriefPath(f)] = true
	}

	var paths []string
	seen := map[string]bool{}
	for _, t := range board.Tasks {
		if t.ID == current.ID {
			continue
		}
		for _, f := range t.Files {
			p := normalizeBriefPath(f)
			// A file this task owns is one it will read itself; a file already
			// listed is not worth repeating.
			if p == "" || mine[p] || seen[p] {
				continue
			}
			seen[p] = true
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	// Stable order: the section is part of a prompt whose prefix should stay
	// byte-identical across calls so the local KV cache keeps hitting.
	sort.Strings(paths)

	var b strings.Builder
	files := 0
	for _, p := range paths {
		if files >= siblingAPIMaxFiles || b.Len() > siblingAPIMaxBytes {
			break
		}
		sigs := exportedSignatures(r.Root, p)
		if len(sigs) == 0 {
			continue
		}
		b.WriteString("- " + p + "\n")
		for _, s := range sigs {
			b.WriteString("    " + s + "\n")
		}
		files++
	}
	if files == 0 {
		return ""
	}
	return "\n## API defined by the other tasks in this run\n" +
		"These files already exist. Match them exactly — do not invent fields or\n" +
		"signatures, and do not edit these files to fit your code unless they are\n" +
		"in your focus list.\n" + b.String()
}

// exportedSignatures returns the exported declarations of one file on disk.
//
// Uses the repo map's own language-aware extractor rather than a second
// parser, so a language it understands here is exactly a language it
// understands there.
func exportedSignatures(root, rel string) []string {
	if repomap.LangForPath(rel) == "" {
		return nil
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() || info.Size() > 512<<10 {
		return nil
	}
	src, err := os.ReadFile(full) //nolint:gosec // a workspace-relative path the board already named
	if err != nil {
		return nil
	}
	parsed := repomap.ExtractSource(rel, string(src))
	var out []string
	for _, s := range parsed.Symbols {
		if !s.Exported {
			continue
		}
		sig := strings.TrimSpace(s.Signature)
		if sig == "" {
			sig = strings.TrimSpace(s.Kind + " " + s.Name)
		}
		out = append(out, sig)
		if len(out) >= siblingAPIMaxSymbols {
			break
		}
	}
	return out
}
