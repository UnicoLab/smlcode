package plan

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	pathLike = regexp.MustCompile(`(?i)\b([a-zA-Z0-9_./-]+\.(?:go|ts|tsx|js|jsx|py|md|json|yaml|yml|toml|rs|java|css|html))\b`)
	fakePath = regexp.MustCompile(`(?i)path/to/|placeholder|/file/containing|example\.com|TODO_PATH`)
)

// ExtractFilePaths pulls likely repo-relative file paths from free text (exploration, notes).
func ExtractFilePaths(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range pathLike.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		p := strings.TrimSpace(m[1])
		p = strings.TrimPrefix(p, "./")
		if fakePath.MatchString(p) || strings.Contains(p, "..") {
			continue
		}
		// Prefer short relative paths
		if strings.Count(p, "/") > 6 {
			continue
		}
		base := filepath.Base(p)
		if base == p && !strings.Contains(p, ".") {
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// SanitizeTasks cleans SLM splitter output for reliable execution.
// root, when set, drops paths that do not exist on disk (kills SLM hallucinations).
// - drops invented placeholder paths
// - injects exploration-discovered files
// - collapses trivial multi-step locate→edit chains into a single worker task when helpful
func SanitizeTasks(tasks []Task, exploration string, query string) []Task {
	return SanitizeTasksIn(tasks, exploration, query, "")
}

// SanitizeTasksIn is SanitizeTasks with an optional workspace root for existence checks.
func SanitizeTasksIn(tasks []Task, exploration, query, root string) []Task {
	known := ExtractFilePaths(exploration)
	known = append(known, ExtractFilePaths(query)...)
	if root != "" {
		known = FilterExisting(root, known)
		if len(known) == 0 {
			known = DiscoverRelevantFiles(root, query, exploration)
		}
	}

	for i := range tasks {
		tasks[i].Normalize()
		var files []string
		for _, f := range tasks[i].Files {
			f = strings.TrimSpace(f)
			if f == "" || fakePath.MatchString(f) || strings.HasPrefix(f, "/") {
				// Drop absolute / invented paths; prefer exploration hits
				continue
			}
			files = append(files, f)
		}
		if root != "" {
			files = FilterExisting(root, files)
		}
		if len(files) == 0 && len(known) > 0 {
			files = append(files, known...)
		}
		tasks[i].Files = ReconcileFiles(root, files, known)
	}

	if shouldCollapse(tasks, query) {
		return collapseToWorker(tasks, known, query)
	}

	// Drop pure "locate" explorer tasks when we already know files
	if len(known) > 0 {
		var kept []Task
		removed := map[string]bool{}
		for _, t := range tasks {
			title := strings.ToLower(t.Title + " " + t.Description)
			if t.Role == RoleExplorer && (strings.Contains(title, "locate") || strings.Contains(title, "find ") || strings.Contains(title, "search")) {
				removed[t.ID] = true
				continue
			}
			kept = append(kept, t)
		}
		if len(kept) > 0 && len(kept) < len(tasks) {
			for i := range kept {
				var deps []string
				for _, d := range kept[i].DependsOn {
					if !removed[d] {
						deps = append(deps, d)
					}
				}
				kept[i].DependsOn = deps
			}
			tasks = kept
		}
	}

	for i := range tasks {
		tasks[i].Normalize()
	}
	return tasks
}

func shouldCollapse(tasks []Task, query string) bool {
	if len(tasks) <= 1 {
		return false
	}
	q := strings.ToLower(query)
	tiny := strings.Contains(q, "tiny") || strings.Contains(q, "doc comment") ||
		strings.Contains(q, "one-line") || strings.Contains(q, "minimal") ||
		(strings.Contains(q, "add") && strings.Contains(q, "comment"))
	if !tiny {
		return false
	}
	// Tiny request → single worker task (avoid research chains)
	return true
}

func collapseToWorker(tasks []Task, known []string, query string) []Task {
	files := known
	for _, t := range tasks {
		files = append(files, t.Files...)
	}
	files = uniq(files)
	desc := "Complete the user request in one shot. Use workspace tools (ws_read/ws_edit/ws_write). Stay minimal.\n" +
		"ONLY edit files that exist — never invent paths like internal/... unless listed below.\n\nRequest:\n" + query
	if len(files) > 0 {
		desc += "\n\nKnown files (authoritative):\n- " + strings.Join(files, "\n- ")
	}
	return []Task{{
		ID:          "T1",
		Title:       "Implement request",
		Description: desc,
		Role:        RoleWorker,
		Column:      ColReadyToDev,
		Files:       files,
		Acceptance:  "Change matches the request; keep scope tiny; target files must exist and be edited via tools",
	}}
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
