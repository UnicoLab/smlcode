package rewind

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// Snapshot is a restoreable copy of files touched in a wave.
type Snapshot struct {
	ID        string            `json:"id"`
	TurnID    string            `json:"turn_id"`
	Wave      int               `json:"wave"`
	CreatedAt string            `json:"created_at"`
	TaskIDs   []string          `json:"task_ids"`
	Files     map[string]string `json:"files"` // rel path → content (or "" if missing)
	Dir       string            `json:"dir"`
}

// Manager stores wave snapshots under .slmcode/waves/.
type Manager struct {
	SlmDir string
	Root   string
}

func (m *Manager) base() string {
	return filepath.Join(m.SlmDir, "waves")
}

// SnapshotPaths copies current file contents for the given relative paths.
func (m *Manager) SnapshotPaths(turnID string, wave int, taskIDs, relPaths []string) (*Snapshot, error) {
	if m.SlmDir == "" || m.Root == "" {
		return nil, fmt.Errorf("rewind: missing slm/root")
	}
	id := fmt.Sprintf("%s-w%02d-%d", sanitize(turnID), wave, time.Now().Unix())
	dir := filepath.Join(m.base(), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, rel := range uniq(relPaths) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		abs := filepath.Join(m.Root, filepath.FromSlash(rel))
		data, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				files[rel] = "" // marker: did not exist
				continue
			}
			continue
		}
		if len(data) > 2_000_000 {
			continue // skip huge binaries
		}
		files[rel] = string(data)
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		_ = atomicfile.Write(dst, data, 0o644)
	}
	snap := &Snapshot{
		ID: id, TurnID: turnID, Wave: wave,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		TaskIDs:   append([]string{}, taskIDs...),
		Files:     files, Dir: dir,
	}
	meta, _ := json.MarshalIndent(snap, "", "  ")
	_ = atomicfile.Write(filepath.Join(dir, "snapshot.json"), meta, 0o644)
	return snap, nil
}

// List returns newest-first snapshot IDs.
func (m *Manager) List() ([]Snapshot, error) {
	entries, err := os.ReadDir(m.base())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Snapshot
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := m.Load(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Load reads snapshot metadata.
func (m *Manager) Load(id string) (*Snapshot, error) {
	path := filepath.Join(m.base(), id, "snapshot.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	s.Dir = filepath.Join(m.base(), id)
	return &s, nil
}

// Restore writes snapshot file contents back to the workspace.
func (m *Manager) Restore(id string) (int, error) {
	s, err := m.Load(id)
	if err != nil {
		return 0, err
	}
	n := 0
	for rel, content := range s.Files {
		abs := filepath.Join(m.Root, filepath.FromSlash(rel))
		if content == "" {
			_ = os.Remove(abs)
			n++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return n, err
		}
		if err := atomicfile.Write(abs, []byte(content), 0o644); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// CopyTree is a helper for tests.
func CopyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		_ = out.Close()
		return err
	})
}

func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		return "turn"
	}
	return s
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
