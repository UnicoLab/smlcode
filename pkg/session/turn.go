package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Turn is one user query interaction with a dedicated plan, tasks, and summary.
// Live PLAN.md / TASKS.md / board.json are mirrors of the *current* turn only;
// prior turns live under .slmcode/queries/<id>/ and enrich MEMORY via summaries.
type Turn struct {
	ID          string     `json:"id"`
	Query       string     `json:"query"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
	Success     bool       `json:"success"`
	Summary     string     `json:"summary,omitempty"`
	Board       plan.Board `json:"board"`
	Interrupted bool       `json:"interrupted,omitempty"`
	Phase       string     `json:"phase,omitempty"`       // last completed/current pipeline phase
	ResumeFrom  string     `json:"resume_from,omitempty"` // phase to continue from after /stop
}

// QueriesDir returns .slmcode/queries/
func QueriesDir(slmDir string) string {
	return filepath.Join(slmDir, "queries")
}

// TurnDir returns .slmcode/queries/<runID>/
func TurnDir(slmDir, runID string) string {
	return filepath.Join(QueriesDir(slmDir), sanitizeID(runID))
}

// SummariesIndexPath is a rolling markdown index of recent turn summaries
// used to enrich context on later queries (not a forever-mutating plan board).
func SummariesIndexPath(slmDir string) string {
	return filepath.Join(slmDir, "summaries", "INDEX.md")
}

// BeginTurn creates a fresh query-scoped directory and clears live board mirrors
// so the new interaction starts with a dedicated empty plan/tasks (rewrite, not patch).
func BeginTurn(slmDir, runID, query string) (*Turn, error) {
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	now := time.Now().Format(time.RFC3339)
	t := &Turn{
		ID:        runID,
		Query:     strings.TrimSpace(query),
		CreatedAt: now,
		UpdatedAt: now,
		Board: plan.Board{
			QueryID: runID,
			Query:   strings.TrimSpace(query),
			Plan: plan.Plan{
				Summary: "(planning for this query…)",
				Steps:   nil,
			},
			Tasks: nil,
		},
	}
	dir := TurnDir(slmDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(slmDir, "summaries"), 0o755); err != nil {
		return nil, err
	}
	if err := writeTurnFiles(slmDir, t); err != nil {
		return nil, err
	}
	// Seed live mirrors with empty rewrite for this query (not previous board).
	if err := mirrorLive(slmDir, t); err != nil {
		return nil, err
	}
	return t, nil
}

// SaveTurnBoard persists the query-scoped board + live mirrors.
func SaveTurnBoard(slmDir string, t *Turn, board plan.Board) error {
	if t == nil {
		return fmt.Errorf("nil turn")
	}
	board.QueryID = t.ID
	if board.Query == "" {
		board.Query = t.Query
	}
	t.Board = board
	t.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := writeTurnFiles(slmDir, t); err != nil {
		return err
	}
	return mirrorLive(slmDir, t)
}

// WriteTurnSummary builds summary.md for the turn and appends to the rolling index.
func WriteTurnSummary(slmDir string, t *Turn, board plan.Board, extraNotes string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("nil turn")
	}
	t.Board = board
	t.Success = board.FailedCount() == 0 && board.AllDone()
	t.UpdatedAt = time.Now().Format(time.RFC3339)

	body := BuildSummaryMarkdown(t, board, extraNotes)
	t.Summary = firstSummaryLine(body)

	dir := TurnDir(slmDir, t.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "summary.md")
	if err := atomicfile.Write(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	if err := writeTurnMeta(dir, t); err != nil {
		return "", err
	}
	_ = appendSummariesIndex(slmDir, t, body)
	return path, nil
}

// BuildSummaryMarkdown renders a structured post-run summary for one query turn.
func BuildSummaryMarkdown(t *Turn, board plan.Board, extraNotes string) string {
	var b strings.Builder
	b.WriteString("# Query summary\n\n")
	b.WriteString(fmt.Sprintf("**Query ID:** %s\n\n", t.ID))
	b.WriteString(fmt.Sprintf("**When:** %s\n\n", t.UpdatedAt))
	b.WriteString("## Asked\n\n")
	b.WriteString(strings.TrimSpace(t.Query))
	b.WriteString("\n\n## Outcome\n\n")
	done, total, failed := boardStats(board)
	status := "success"
	if failed > 0 || !board.AllDone() {
		status = "incomplete"
	}
	if failed > 0 {
		status = "failed"
	}
	b.WriteString(fmt.Sprintf("- **Status:** %s\n", status))
	b.WriteString(fmt.Sprintf("- **Tasks:** %d/%d done, %d failed\n", done, total, failed))
	if board.Plan.Summary != "" {
		b.WriteString(fmt.Sprintf("- **Plan:** %s\n", strings.TrimSpace(board.Plan.Summary)))
	}
	b.WriteString("\n## Files touched\n\n")
	files := collectTouchedFiles(board)
	if len(files) == 0 {
		b.WriteString("(none recorded)\n")
	} else {
		for _, f := range files {
			b.WriteString("- `" + f + "`\n")
		}
	}
	b.WriteString("\n## Tasks\n\n")
	for _, task := range board.Tasks {
		task.Normalize()
		mark := "○"
		switch {
		case task.Column == plan.ColDone:
			mark = "✓"
		case task.Column == plan.ColBlocked || task.Status == plan.StatusFailed || task.Error != "":
			mark = "✗"
		}
		b.WriteString(fmt.Sprintf("- %s **%s** (%s) — %s\n", mark, task.ID, task.Column, task.Title))
		if task.Error != "" {
			b.WriteString(fmt.Sprintf("  - error: %s\n", firstLine(task.Error)))
		}
	}
	if strings.TrimSpace(extraNotes) != "" {
		b.WriteString("\n## Notes / lessons\n\n")
		b.WriteString(strings.TrimSpace(extraNotes))
		b.WriteString("\n")
	}
	b.WriteString("\n## Plan snapshot\n\n")
	if board.Plan.Summary != "" {
		b.WriteString(board.Plan.Summary + "\n")
	}
	for i, s := range board.Plan.Steps {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, s))
	}
	return b.String()
}

// RecentSummaries returns markdown excerpts from the last N query turns
// for enriching context on a new interaction (project knowledge, not live plan).
func RecentSummaries(slmDir string, n int) string {
	if n <= 0 {
		n = 5
	}
	index, err := os.ReadFile(SummariesIndexPath(slmDir))
	if err == nil && len(index) > 0 {
		body := string(index)
		if len(body) > 8000 {
			body = body[len(body)-8000:]
			if i := strings.Index(body, "\n## "); i > 0 {
				body = body[i+1:]
			}
		}
		return strings.TrimSpace(body)
	}
	// Fallback: scan query dirs
	entries, err := os.ReadDir(QueriesDir(slmDir))
	if err != nil {
		return ""
	}
	type item struct {
		name string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	var b strings.Builder
	for i, it := range items {
		if i >= n {
			break
		}
		data, err := os.ReadFile(filepath.Join(QueriesDir(slmDir), it.name, "summary.md"))
		if err != nil || len(data) == 0 {
			continue
		}
		excerpt := string(data)
		if len(excerpt) > 1500 {
			excerpt = excerpt[:1500] + "\n…"
		}
		b.WriteString(excerpt)
		b.WriteString("\n\n---\n\n")
	}
	return strings.TrimSpace(b.String())
}

// ListQueries returns recent query turns newest-first.
func ListQueries(slmDir string) ([]Turn, error) {
	entries, err := os.ReadDir(QueriesDir(slmDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Turn
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := LoadTurn(slmDir, e.Name())
		if err != nil {
			continue
		}
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

// LoadTurn loads a query turn from disk.
func LoadTurn(slmDir, runID string) (*Turn, error) {
	dir := TurnDir(slmDir, runID)
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var t Turn
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	if bdata, err := os.ReadFile(filepath.Join(dir, "board.json")); err == nil {
		_ = json.Unmarshal(bdata, &t.Board)
	}
	return &t, nil
}

func writeTurnFiles(slmDir string, t *Turn) error {
	dir := TurnDir(slmDir, t.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_ = atomicfile.Write(filepath.Join(dir, "QUERY.md"),
		[]byte("# Query\n\n"+t.Query+"\n"), 0o644)
	planMD, tasksMD := t.Board.ToMarkdown()
	_ = atomicfile.Write(filepath.Join(dir, "PLAN.md"), []byte(planMD), 0o644)
	_ = atomicfile.Write(filepath.Join(dir, "TASKS.md"), []byte(tasksMD), 0o644)
	data, err := json.MarshalIndent(t.Board, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(dir, "board.json"), data, 0o644); err != nil {
		return err
	}
	return writeTurnMeta(dir, t)
}

func writeTurnMeta(dir string, t *Turn) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, "meta.json"), data, 0o644)
}

func mirrorLive(slmDir string, t *Turn) error {
	planMD, tasksMD := t.Board.ToMarkdown()
	if err := atomicfile.Write(filepath.Join(slmDir, "PLAN.md"), []byte(planMD), 0o644); err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(slmDir, "TASKS.md"), []byte(tasksMD), 0o644); err != nil {
		return err
	}
	_ = atomicfile.Write(filepath.Join(slmDir, "QUERY.md"),
		[]byte("# Current Query\n\n"+t.Query+"\n"), 0o644)
	data, err := json.MarshalIndent(t.Board, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(slmDir, "board.json"), data, 0o644)
}

func appendSummariesIndex(slmDir string, t *Turn, fullBody string) error {
	path := SummariesIndexPath(slmDir)
	existing, _ := os.ReadFile(path)
	var b strings.Builder
	b.WriteString(string(existing))
	b.WriteString(fmt.Sprintf("\n\n## Turn %s (%s)\n\n", t.ID, t.UpdatedAt))
	b.WriteString("**Query:** " + firstLine(t.Query) + "\n\n")
	// Keep index lean — outcome + files + lessons excerpt.
	excerpt := fullBody
	if i := strings.Index(excerpt, "## Outcome"); i >= 0 {
		excerpt = excerpt[i:]
	}
	if len(excerpt) > 2000 {
		excerpt = excerpt[:2000] + "\n…"
	}
	b.WriteString(excerpt)
	b.WriteString("\n")
	body := b.String()
	// Cap index growth for SLM context.
	if len(body) > 24_000 {
		body = body[len(body)-20_000:]
		if i := strings.Index(body, "\n## "); i >= 0 {
			body = "# Prior query summaries\n" + body[i:]
		}
	} else if !strings.HasPrefix(strings.TrimSpace(body), "#") {
		body = "# Prior query summaries\n" + body
	}
	return atomicfile.Write(path, []byte(body), 0o644)
}

func boardStats(board plan.Board) (done, total, failed int) {
	total = len(board.Tasks)
	failed = board.FailedCount()
	for _, t := range board.Tasks {
		t.Normalize()
		if t.Column == plan.ColDone {
			done++
		}
	}
	return done, total, failed
}

func collectTouchedFiles(board plan.Board) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range board.Tasks {
		for _, f := range t.Files {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func firstSummaryLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- **Status:**") {
			return line
		}
	}
	return firstLine(body)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
