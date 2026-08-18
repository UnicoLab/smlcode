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

// Session is a resumable run snapshot (Claude Code–style).
type Session struct {
	ID        string     `json:"id"`
	Query     string     `json:"query"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
	Summary   string     `json:"summary,omitempty"`
	Success   bool       `json:"success"`
	Board     plan.Board `json:"board"`
}

func Dir(slmDir string) string { return filepath.Join(slmDir, "sessions") }

func Save(slmDir string, s Session) (string, error) {
	if err := os.MkdirAll(Dir(slmDir), 0o755); err != nil {
		return "", err
	}
	if s.ID == "" {
		s.ID = fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	now := time.Now().Format(time.RFC3339)
	if s.CreatedAt == "" {
		s.CreatedAt = now
	}
	s.UpdatedAt = now
	path := filepath.Join(Dir(slmDir), s.ID+".json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return path, atomicfile.Write(path, data, 0o644)
}

func Load(slmDir, id string) (*Session, error) {
	id = strings.TrimSuffix(id, ".json")
	path := filepath.Join(Dir(slmDir), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func List(slmDir string) ([]Session, error) {
	entries, err := os.ReadDir(Dir(slmDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := Load(slmDir, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

// Archive writes a human-readable markdown snapshot under .slmcode/archives/
// so completed queries keep a separate history thread per project.
func Archive(slmDir, runID, query, summary string) (string, error) {
	dir := filepath.Join(slmDir, "archives")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	stamp := time.Now().Format("20060102_150405")
	path := filepath.Join(dir, fmt.Sprintf("%s_%s.md", stamp, sanitizeID(runID)))

	// Bundle key docs for the archive thread.
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Archive %s\n\n", runID))
	b.WriteString(fmt.Sprintf("**When:** %s\n\n", time.Now().Format(time.RFC3339)))
	b.WriteString("## Query\n\n")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n\n## Summary\n\n")
	b.WriteString(strings.TrimSpace(summary))
	b.WriteString("\n\n")
	// Prefer query-scoped summary when present.
	if sum, err := os.ReadFile(filepath.Join(TurnDir(slmDir, runID), "summary.md")); err == nil && len(sum) > 0 {
		body := string(sum)
		if len(body) > 12000 {
			body = body[:12000] + "\n…\n"
		}
		b.WriteString("## summary.md\n\n")
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	for _, name := range []string{"PLAN.md", "TASKS.md", "MEMORY.md", "CONTEXT.md", "PROJECT.md"} {
		data, err := os.ReadFile(filepath.Join(slmDir, name))
		if err != nil || len(data) == 0 {
			continue
		}
		body := string(data)
		if len(body) > 12000 {
			body = body[:12000] + "\n…\n"
		}
		b.WriteString("## " + name + "\n\n")
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	return path, atomicfile.Write(path, []byte(b.String()), 0o644)
}

func sanitizeID(id string) string {
	id = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, id)
	if len(id) > 48 {
		return id[:48]
	}
	return id
}
