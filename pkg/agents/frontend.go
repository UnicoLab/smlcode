package agents

import (
	"path/filepath"
	"strings"
)

// Choosing HOW a frontend gets built, as opposed to in which language.
//
// A .tsx file says the work is React. It does not say whether this project
// writes its components or installs them, and on a small local model that is
// the difference that dominates: hand-writing a dialog with focus traps and
// keyboard handling is where a 7-32B model spends its runway and still ships
// something inaccessible. Installing a reviewed one with the library's own CLI
// and wiring it up is a different task shape entirely — mostly imports, props
// and layout, which is exactly what these models are good at.
//
// So there are three methods, and the harness picks between them from evidence
// rather than asking the operator to configure anything:
//
//	shadcn-worker      the project uses (or should use) shadcn/ui
//	untitledui-worker  the project uses (or should use) Untitled UI
//	""                 write components by hand — react-worker keeps the task
//
// Precedence, highest first, with the reason each rung exists:
//
//  1. The QUERY names a library, or names none on purpose ("from scratch").
//     An explicit instruction is not evidence to be weighed, it is an answer.
//  2. The PROJECT already committed to one. A components.json next to a
//     components/ui tree is a decision someone already made; adding a
//     hand-written Button beside twenty installed ones is not a neutral
//     choice, it is a duplicate.
//  3. GREENFIELD frontend work defaults to assembling. Nothing is being
//     matched, nothing is being overridden, and this is the case where reuse
//     buys the most.
//  4. Anything else keeps react-worker. An existing React app with no library
//     markers has house patterns to match, and introducing a component library
//     into it is a migration nobody asked for.

// Assembler ids. Exported because the composer names them in a handoff and the
// CLI names them in `slmcode agent list`.
const (
	ShadcnWorker     = "shadcn-worker"
	UntitledUIWorker = "untitledui-worker"
)

// DefaultAssembler is the method greenfield frontend work gets when the request
// does not name one.
//
// shadcn/ui rather than Untitled UI, for a reason that is about this harness
// rather than about the libraries: shadcn copies plain source into the repo
// with no runtime dependency to satisfy, so a run that half-finishes still
// leaves a tree that builds. Untitled UI stays fully available by name.
const DefaultAssembler = ShadcnWorker

// FrontendChoice is a method decision plus the reason for it, so the run can
// tell the operator which of the three it took and why.
type FrontendChoice struct {
	// Worker is the assembler agent id, or "" for hand-written components.
	Worker string
	// Why is one operator-facing sentence.
	Why string
	// FromQuery reports whether the request asked for this explicitly. An
	// explicit choice must never be silently overridden downstream.
	FromQuery bool
}

// shadcnMarkers / untitledMarkers are paths whose presence proves a project has
// already adopted a library. Checked against the workspace inventory, so they
// are cheap and cannot be faked by a model's prose.
var (
	shadcnMarkers   = []string{"components.json", "components/ui", "src/components/ui"}
	untitledMarkers = []string{"untitledui.json", "components/base", "src/components/base"}
)

// scratchPhrases are the ways a request declines a component library. Checked
// before anything else: a person who says "from scratch" has answered.
var scratchPhrases = []string{
	"from scratch", "no component library", "without a component library",
	"no ui library", "without a ui library", "hand-write", "hand write",
	"custom components", "own components", "vanilla react",
}

// ChooseFrontend decides how frontend work should be built.
//
// inventory is workspace-relative paths (plan.ListWorkspaceFiles output is
// exactly right). has reports whether an agent id is registered; a nil has
// refuses every assembler, for the same reason SpecialistForFiles does — an
// unregistered agent id is a hard task failure, not a degraded run.
//
// greenfield says the frontend does not exist yet. It is passed in rather than
// inferred here because the caller already knows: an empty inventory, or one
// with no component files in it.
func ChooseFrontend(query string, inventory []string, greenfield bool, has func(string) bool) FrontendChoice {
	if has == nil {
		return FrontendChoice{Why: "no agent registry — keeping hand-written components"}
	}
	q := strings.ToLower(query)

	// 1. The request answered the question.
	for _, p := range scratchPhrases {
		if strings.Contains(q, p) {
			return FrontendChoice{
				Why:       "the request asked for components written from scratch",
				FromQuery: true,
			}
		}
	}
	if named := assemblerNamedIn(q); named != "" {
		if has(named) {
			return FrontendChoice{
				Worker:    named,
				Why:       "the request named " + libraryName(named),
				FromQuery: true,
			}
		}
		return FrontendChoice{
			Why:       "the request named " + libraryName(named) + ", but its agent is not registered",
			FromQuery: true,
		}
	}

	// 2. The project already committed to one.
	if inventoryHasMarker(inventory, shadcnMarkers) && has(ShadcnWorker) {
		return FrontendChoice{Worker: ShadcnWorker, Why: "this project already uses shadcn/ui"}
	}
	if inventoryHasMarker(inventory, untitledMarkers) && has(UntitledUIWorker) {
		return FrontendChoice{Worker: UntitledUIWorker, Why: "this project already uses Untitled UI"}
	}

	// 3. Greenfield frontend work assembles by default.
	if greenfield && has(DefaultAssembler) {
		return FrontendChoice{
			Worker: DefaultAssembler,
			Why: "new frontend — assembling from " + libraryName(DefaultAssembler) +
				" instead of writing components by hand (say \"from scratch\" to opt out)",
		}
	}

	// 4. An existing app with no markers keeps its house patterns.
	return FrontendChoice{Why: "existing frontend with no component library — matching its current patterns"}
}

// reactFileExtensions are the files a component library actually produces.
//
// Not .ts: a .ts file in a React project is a hook, a store or an api client,
// and neither library installs those. Routing them to an assembler would hand a
// prompt about installing components to a task that has none to install.
var reactFileExtensions = []string{".jsx", ".tsx"}

// HasReactFiles reports whether any path is a React component file.
func HasReactFiles(files []string) bool {
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(strings.TrimSpace(f)))
		for _, want := range reactFileExtensions {
			if ext == want {
				return true
			}
		}
	}
	return false
}

// assemblerNamedIn returns the assembler a query names, or "".
func assemblerNamedIn(loweredQuery string) string {
	for _, kw := range []string{"shadcn", "shad cn", "shadcn/ui"} {
		if strings.Contains(loweredQuery, kw) {
			return ShadcnWorker
		}
	}
	for _, kw := range []string{"untitled ui", "untitledui", "untitled-ui"} {
		if strings.Contains(loweredQuery, kw) {
			return UntitledUIWorker
		}
	}
	return ""
}

// libraryName renders an assembler id as the library a human would name.
func libraryName(worker string) string {
	switch worker {
	case ShadcnWorker:
		return "shadcn/ui"
	case UntitledUIWorker:
		return "Untitled UI"
	}
	return worker
}

// inventoryHasMarker reports whether any marker path appears in the inventory.
//
// Matches a marker either as a full path prefix (a directory like
// components/ui) or as a file's own name, so it works whether the inventory
// lists the directory or only the files inside it.
func inventoryHasMarker(inventory, markers []string) bool {
	for _, raw := range inventory {
		p := strings.ToLower(strings.TrimSpace(filepath.ToSlash(raw)))
		if p == "" {
			continue
		}
		for _, m := range markers {
			if p == m || strings.HasPrefix(p, m+"/") || strings.Contains(p, "/"+m+"/") ||
				filepath.Base(p) == m {
				return true
			}
		}
	}
	return false
}
