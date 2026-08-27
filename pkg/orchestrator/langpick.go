package orchestrator

import (
	"path/filepath"
	"strings"
)

// Choosing the language specialists for a run.
//
// Two sources disagree, and which one wins is not a matter of taste:
//
//   - the PROJECT language is a fact about the repository — a go.mod, a
//     pyproject.toml, the extensions of the files that are actually there.
//   - the QUERY hint is a word in a sentence a human typed.
//
// The query used to win unconditionally, and the failure it produced was
// self-contradictory rather than merely suboptimal: in a Go repository, a query
// that happened to contain "python" assembled a team of python-worker and
// python-tester while the very same handoff contract said "Detected project
// language: Go" and "verify with go test ./... -count=1". The workers were
// briefed on one language and the verification on another.
//
// So the repository wins — with one exception that matters. A query hint that
// the INVENTORY CORROBORATES is not a stray word, it is a request to work on a
// part of the repo the top-level language detector did not name: the web/ tree
// in a Go project, the scripts/ directory in a TypeScript one. That case keeps
// the query's answer, because there the query knows something the detector
// does not.

// specialistExtensions maps a worker id to the file extensions that would prove
// a repository actually contains that language.
//
// Only workers whose language has unambiguous extensions appear here. A hint
// that cannot be corroborated is treated as uncorroborated, which is the safe
// direction: it defers to the detected project language.
var specialistExtensions = map[string][]string{
	"python-worker": {".py"},
	"go-worker":     {".go"},
	"rust-worker":   {".rs"},
	"java-worker":   {".java"},
	"kotlin-worker": {".kt", ".kts"},
	"cpp-worker":    {".cpp", ".cc", ".cxx", ".hpp", ".h"},
	"dotnet-worker": {".cs", ".csproj", ".sln"},
	"ruby-worker":   {".rb"},
	"php-worker":    {".php"},
	"swift-worker":  {".swift"},
	"ts-worker":     {".ts", ".tsx"},
	"react-worker":  {".jsx", ".tsx"},
	"web-worker":    {".html", ".htm", ".css", ".js"},
	"shell-worker":  {".sh", ".bash"},
}

// inventoryHasLanguage reports whether the workspace inventory contains files
// belonging to a specialist's language.
func inventoryHasLanguage(worker string, inventory []string) bool {
	exts := specialistExtensions[strings.ToLower(strings.TrimSpace(worker))]
	if len(exts) == 0 {
		return false
	}
	for _, path := range inventory {
		got := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
		if got == "" {
			continue
		}
		for _, want := range exts {
			if got == want {
				return true
			}
		}
	}
	return false
}

// pickSpecialists resolves the worker/tester pair for a run.
//
// Precedence, highest first:
//
//  1. the query hint, when the inventory corroborates it;
//  2. the detected project language;
//  3. the query hint, when the project language is unknown — a greenfield
//     request in an empty directory has nothing else to go on, and "write a
//     Python CLI" in an empty folder should not fall back to a generic worker;
//  4. the generic worker/tester.
func pickSpecialists(query, lang string, inventory []string) (worker, tester string) {
	qw, qt := queryLanguageSpecialists(query)
	pw, pt := projectLanguageSpecialists(lang)

	switch {
	case qw != "" && inventoryHasLanguage(qw, inventory):
		worker, tester = qw, qt
	case pw != "":
		worker, tester = pw, pt
	case qw != "":
		worker, tester = qw, qt
	}
	if worker == "" {
		worker = "worker"
	}
	if tester == "" {
		tester = "tester"
	}
	return worker, tester
}
