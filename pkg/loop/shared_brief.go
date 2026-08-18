package loop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

const defaultSharedBriefLimit = 1400

type sharedBriefItem struct {
	task  plan.Task
	score int
	order int
}

func (r *Runner) sharedBriefSection(board *plan.Board, current plan.Task) string {
	limit := defaultSharedBriefLimit
	if r != nil {
		if r.SharedBriefLimit < 0 {
			return ""
		}
		if r.SharedBriefLimit > 0 {
			limit = r.SharedBriefLimit
		}
	}
	body := buildSharedBrief(board, current, limit)
	if body == "" {
		return ""
	}
	return "\n## Shared task handoff (bounded)\n" +
		"Use these completed sibling facts when relevant. Do not repeat solved work.\n" +
		body + "\n"
}

func buildSharedBrief(board *plan.Board, current plan.Task, limit int) string {
	if board == nil || limit <= 0 {
		return ""
	}
	items := rankSharedBriefItems(board.Tasks, current)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		line := formatSharedBriefLine(item.task)
		if line == "" {
			continue
		}
		if b.Len()+len(line)+1 > limit {
			if b.Len() == 0 {
				b.WriteString(truncateASCII(line, limit))
			}
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
		if b.Len() >= limit {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func rankSharedBriefItems(tasks []plan.Task, current plan.Task) []sharedBriefItem {
	deps := map[string]bool{}
	for _, id := range current.DependsOn {
		id = strings.TrimSpace(id)
		if id != "" {
			deps[id] = true
		}
	}
	currentFiles := normalizedFileSet(current.Files)
	currentDirs := dirSet(current.Files)
	items := make([]sharedBriefItem, 0, len(tasks))
	for i, task := range tasks {
		task.Normalize()
		if task.ID == current.ID || task.Column != plan.ColDone {
			continue
		}
		if strings.TrimSpace(task.Output) == "" && strings.TrimSpace(task.Review) == "" && strings.TrimSpace(task.Notes) == "" {
			continue
		}
		score := 1
		if deps[task.ID] {
			score += 100
		}
		if sharesFile(task.Files, currentFiles) {
			score += 80
		}
		if sharesDir(task.Files, currentDirs) {
			score += 40
		}
		if task.Role == plan.RoleExplorer || task.Role == plan.RoleTester {
			score += 15
		}
		items = append(items, sharedBriefItem{task: task, score: score, order: i})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].order > items[j].order
		}
		return items[i].score > items[j].score
	})
	if len(items) > 6 {
		items = items[:6]
	}
	return items
}

func formatSharedBriefLine(task plan.Task) string {
	summary := taskSummary(task)
	if summary == "" {
		return ""
	}
	files := task.Files
	if len(files) == 0 {
		files = parseFilesChanged(task.Output)
	}
	filePart := ""
	if len(files) > 0 {
		filePart = "; files=" + strings.Join(limitStrings(files, 4), ",")
	}
	review := firstLine(task.Review)
	if review != "" {
		review = "; review=" + truncateASCII(review, 120)
	}
	return fmt.Sprintf("- %s @%s: %s%s%s", task.ID, firstNonEmpty(task.Role, plan.RoleWorker), truncateASCII(summary, 220), filePart, review)
}

func taskSummary(task plan.Task) string {
	if strings.EqualFold(task.Role, plan.RoleTester) {
		tr := plan.ParseTesterJSON(task.Output)
		if tr.Summary != "" {
			return tr.Summary
		}
		if tr.Passed {
			return "tester passed"
		}
		if len(tr.Failures) > 0 {
			return tr.Failures[0]
		}
	}
	var payload struct {
		Summary string `json:"summary"`
		Notes   string `json:"notes"`
		Status  string `json:"status"`
	}
	if raw := firstJSONObject(task.Output); raw != "" {
		_ = json.Unmarshal([]byte(raw), &payload)
	}
	if payload.Summary != "" {
		return payload.Summary
	}
	if payload.Notes != "" {
		return payload.Notes
	}
	if task.Notes != "" {
		return task.Notes
	}
	return firstMeaningfulOutputLine(task.Output)
}

func firstJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

func firstMeaningfulOutputLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "{") || strings.HasPrefix(line, "}") {
			continue
		}
		if strings.HasPrefix(line, "- ") && strings.Contains(line, ":") {
			continue
		}
		return line
	}
	return ""
}

func normalizedFileSet(files []string) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		f = normalizeBriefPath(f)
		if f != "" {
			out[f] = true
		}
	}
	return out
}

func dirSet(files []string) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		f = normalizeBriefPath(f)
		if f == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(f))
		if dir != "." && dir != "" {
			out[dir] = true
		}
	}
	return out
}

func sharesFile(files []string, current map[string]bool) bool {
	for _, f := range files {
		if current[normalizeBriefPath(f)] {
			return true
		}
	}
	return false
}

func sharesDir(files []string, currentDirs map[string]bool) bool {
	for _, f := range files {
		f = normalizeBriefPath(f)
		if f == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(f))
		if currentDirs[dir] {
			return true
		}
	}
	return false
}

func normalizeBriefPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.Trim(path, "`'\"")
	return strings.ToLower(path)
}

func limitStrings(in []string, n int) []string {
	if n <= 0 || len(in) <= n {
		return in
	}
	out := append([]string{}, in[:n]...)
	out = append(out, fmt.Sprintf("+%d more", len(in)-n))
	return out
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func truncateASCII(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
