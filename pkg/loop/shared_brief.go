package loop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
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
	root := ""
	if r != nil {
		root = r.Root
		if r.SharedBriefLimit < 0 {
			return ""
		}
		if r.SharedBriefLimit > 0 {
			limit = r.SharedBriefLimit
		}
	}
	body := buildSharedBriefWithRoot(board, current, limit, root)
	if body == "" {
		return ""
	}
	return "\n## Shared task handoff (bounded)\n" +
		"Use these completed sibling facts when relevant. Do not repeat solved work.\n" +
		body + "\n"
}

func buildSharedBrief(board *plan.Board, current plan.Task, limit int) string {
	return buildSharedBriefWithRoot(board, current, limit, "")
}

func buildSharedBriefWithRoot(board *plan.Board, current plan.Task, limit int, root string) string {
	if board == nil || limit <= 0 {
		return ""
	}
	items := rankSharedBriefItems(board.Tasks, current, root)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		line := formatSharedBriefLineWithRoot(item.task, root)
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

func rankSharedBriefItems(tasks []plan.Task, current plan.Task, root string) []sharedBriefItem {
	deps := map[string]bool{}
	for _, id := range current.DependsOn {
		id = strings.TrimSpace(id)
		if id != "" {
			deps[id] = true
		}
	}
	currentFiles := normalizedFileSet(current.Files, root)
	currentDirs := dirSet(current.Files, root)
	items := make([]sharedBriefItem, 0, len(tasks))
	for i, task := range tasks {
		task.Normalize()
		if task.ID == current.ID {
			continue
		}
		files := briefFiles(task, root)
		sameFile := sharesFile(files, currentFiles)
		sameDir := sharesDir(files, currentDirs)
		related := deps[task.ID] || sameFile || sameDir
		if !briefCandidate(task, related) {
			continue
		}
		score := 1
		if deps[task.ID] {
			score += 100
		}
		if sameFile {
			score += 80
		}
		if sameDir {
			score += 40
		}
		if task.Column == plan.ColBlocked || task.Status == plan.StatusFailed || strings.TrimSpace(task.Error) != "" {
			score += 45
		}
		if task.Column == plan.ColInReview || strings.TrimSpace(task.Review) != "" {
			score += 20
		}
		if task.Role == plan.RoleExplorer || plan.IsTesterRole(task.Role) {
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

func formatSharedBriefLineWithRoot(task plan.Task, root string) string {
	summary := taskSummary(task)
	if summary == "" {
		return ""
	}
	files := briefFiles(task, root)
	filePart := ""
	if len(files) > 0 {
		filePart = "; files=" + strings.Join(limitStrings(files, 4), ",")
	}
	status := sharedStatusHint(task)
	review := firstLine(task.Review)
	if review != "" {
		review = "; review=" + truncateASCII(review, 120)
	}
	errText := firstLine(task.Error)
	if errText != "" {
		errText = "; error=" + truncateASCII(errText, 120)
	}
	verify := sharedVerificationHint(task)
	if verify != "" {
		verify = "; verify=" + truncateASCII(verify, 160)
	}
	return fmt.Sprintf("- %s @%s: %s%s%s%s%s%s", task.ID, firstNonEmpty(task.Role, plan.RoleWorker), truncateASCII(summary, 220), status, filePart, review, errText, verify)
}

func briefCandidate(task plan.Task, related bool) bool {
	if strings.TrimSpace(task.Output) == "" &&
		strings.TrimSpace(task.Review) == "" &&
		strings.TrimSpace(task.Notes) == "" &&
		strings.TrimSpace(task.Error) == "" {
		return false
	}
	if task.Column == plan.ColDone {
		return true
	}
	return related && (task.Column == plan.ColBlocked ||
		task.Column == plan.ColInReview ||
		task.Status == plan.StatusFailed ||
		strings.TrimSpace(task.Review) != "" ||
		strings.TrimSpace(task.Error) != "")
}

func briefFiles(task plan.Task, root string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(files []string) {
		for _, f := range files {
			display, ok := displayBriefPath(f, root)
			if !ok {
				continue
			}
			norm := normalizeBriefPath(display)
			if norm == "" || seen[norm] {
				continue
			}
			seen[norm] = true
			out = append(out, display)
		}
	}
	add(task.Files)
	add(parseFilesChanged(task.Output))
	return out
}

func sharedStatusHint(task plan.Task) string {
	var parts []string
	if task.Column != "" && task.Column != plan.ColDone {
		parts = append(parts, "status="+task.Column)
	}
	if task.Status != "" && task.Status != sharedStatusFromColumn(task.Column) {
		parts = append(parts, "state="+task.Status)
	}
	if task.Retries > 0 {
		parts = append(parts, fmt.Sprintf("retries=%d", task.Retries))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, ",")
}

func sharedStatusFromColumn(col string) string {
	switch col {
	case plan.ColToScope, plan.ColScoped:
		return plan.StatusPending
	case plan.ColReadyToDev:
		return plan.StatusReady
	case plan.ColInProgress:
		return plan.StatusRunning
	case plan.ColInReview:
		return plan.StatusReview
	case plan.ColDone:
		return plan.StatusDone
	case plan.ColBlocked:
		return plan.StatusFailed
	default:
		return plan.StatusPending
	}
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

func sharedVerificationHint(task plan.Task) string {
	if strings.EqualFold(task.Role, plan.RoleTester) {
		tr := plan.ParseTesterJSON(task.Output)
		switch {
		case len(tr.Failures) > 0:
			return "fix " + firstLine(tr.Failures[0])
		case len(tr.Commands) > 0:
			return strings.Join(limitStrings(tr.Commands, 3), ",")
		case tr.Passed:
			return "tester passed"
		}
	}
	if strings.TrimSpace(task.Acceptance) == "" {
		return ""
	}
	return firstLine(task.Acceptance)
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

func normalizedFileSet(files []string, root string) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		f = normalizeBriefPathWithRoot(f, root)
		if f != "" {
			out[f] = true
		}
	}
	return out
}

func dirSet(files []string, root string) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		f = normalizeBriefPathWithRoot(f, root)
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
	display, ok := displayBriefPath(path, "")
	if !ok {
		return ""
	}
	return strings.ToLower(display)
}

func normalizeBriefPathWithRoot(path, root string) string {
	display, ok := displayBriefPath(path, root)
	if !ok {
		return ""
	}
	return strings.ToLower(display)
}

func displayBriefPath(path, root string) (string, bool) {
	path = strings.Trim(strings.TrimSpace(path), "`'\"")
	if path == "" || strings.ContainsRune(path, '\x00') {
		return "", false
	}
	localPath := filepath.FromSlash(path)
	if filepath.IsAbs(localPath) {
		if strings.TrimSpace(root) == "" {
			return "", false
		}
		cleanRoot, err := filepath.Abs(root)
		if err != nil {
			return "", false
		}
		rel, err := filepath.Rel(cleanRoot, filepath.Clean(localPath))
		if err != nil {
			return "", false
		}
		localPath = rel
	}
	clean := filepath.ToSlash(filepath.Clean(localPath))
	if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return "", false
	}
	return clean, true
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

// truncateASCII clips s to at most n bytes on a RUNE boundary. The name is
// historical: it used to byte-slice, which split multi-byte runes and produced
// invalid UTF-8 in prompts.
func truncateASCII(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return textutil.Clip(s, n)
	}
	return textutil.Clip(s, n-3) + "..."
}
