package agents

import (
	"path/filepath"
	"strings"
)

// SpecialistExtensions maps a language worker id to the file extensions that
// identify its language.
//
// Only workers whose language has unambiguous extensions belong here. A
// specialist that cannot be identified from a path is better left out: the
// callers all treat "no match" as "use the generic worker", which is the safe
// direction.
var SpecialistExtensions = map[string][]string{
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

// specialistPriority breaks ties when one extension belongs to several
// specialists (.tsx is both ts-worker and react-worker) or when a task mixes
// languages. Earlier wins.
//
// React before ts: a .tsx file is a component, and the ts-worker prompt is
// about services and CLIs. Application languages before web: a task that
// touches both a Go file and an HTML template is Go work with a template in it.
//
// The component-library assemblers are deliberately NOT here. They are not a
// language — .tsx does not tell you whether a project assembles components or
// writes them — so choosing one is a question about the PROJECT and the
// request, answered in frontend.go, not a question about a file extension.
var specialistPriority = []string{
	"go-worker", "python-worker", "rust-worker", "java-worker", "kotlin-worker",
	"cpp-worker", "dotnet-worker", "ruby-worker", "php-worker", "swift-worker",
	"react-worker", "ts-worker", "web-worker", "shell-worker",
}

// SpecialistForFiles returns the language worker that owns these files, or ""
// when no specialist clearly does.
//
// This is what makes a MULTI-LANGUAGE team possible. The composition names one
// execute.default_role for the whole run, so a request for "a Go backend and a
// React frontend" put every task on one specialist — and, because a splitter
// may only emit the generic roles worker/tester/explorer/context, it could not
// have said otherwise. Files are the evidence the splitter already produces and
// the harness already reconciles against disk, so routing on them needs no
// extra model judgement and cannot be hallucinated.
//
// has reports whether an agent id is actually registered; a specialist the
// factory never built is skipped rather than routed to.
//
// A nil has REFUSES every id, and that direction is not a detail. Routing to an
// unregistered agent is not a degraded run, it is a hard task failure — the
// same reason pkg/loop's escalate() treats a nil HasRole as "no ladder". A
// caller that cannot say which agents exist has not proved this one does, so
// the task keeps the generic worker it already had.
//
// Ties go to specialistPriority, and a file set with no recognized extension
// returns "" so the caller keeps whatever role it already had.
func SpecialistForFiles(files []string, has func(string) bool) string {
	if len(files) == 0 || has == nil {
		return ""
	}
	counts := map[string]int{}
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(strings.TrimSpace(f)))
		if ext == "" {
			continue
		}
		for id, exts := range SpecialistExtensions {
			for _, want := range exts {
				if ext == want {
					counts[id]++
					break
				}
			}
		}
	}
	best, bestN := "", 0
	for _, id := range specialistPriority {
		n := counts[id]
		if n == 0 || n < bestN {
			continue
		}
		if !has(id) {
			continue
		}
		// Strictly greater, so an equal count leaves the higher-priority
		// specialist in place rather than letting map order decide.
		if n > bestN {
			best, bestN = id, n
		}
	}
	return best
}
