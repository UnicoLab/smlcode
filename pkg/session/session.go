package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/piotrlaczkowski/slmcode/pkg/plan"
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
	return path, os.WriteFile(path, data, 0o644)
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
