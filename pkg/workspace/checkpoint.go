package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileCheckpointer snapshots file contents before the first write/edit/patch
// of a session (little-coder checkpoint — first-write-wins).
type FileCheckpointer struct {
	mu      sync.Mutex
	SlmDir  string
	Root    string
	Session string
	seen    map[string]bool
}

// NewFileCheckpointer creates a best-effort pre-write backup store under
// .slmcode/checkpoints/<session>/.
func NewFileCheckpointer(slmDir, root, session string) *FileCheckpointer {
	if session == "" {
		session = "default"
	}
	return &FileCheckpointer{
		SlmDir: slmDir, Root: root, Session: session,
		seen: map[string]bool{},
	}
}

func (c *FileCheckpointer) dir() string {
	return filepath.Join(c.SlmDir, "checkpoints", sanitizeSession(c.Session))
}

// BackupIfNeeded copies the current file once per session path.
func (c *FileCheckpointer) BackupIfNeeded(rel string) {
	if c == nil || c.SlmDir == "" || c.Root == "" {
		return
	}
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || strings.Contains(rel, "..") {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen[rel] {
		return
	}
	c.seen[rel] = true
	abs := filepath.Join(c.Root, filepath.FromSlash(rel))
	_ = os.MkdirAll(c.dir(), 0o755)
	name := safeCheckpointName(rel)
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			_ = os.WriteFile(filepath.Join(c.dir(), name+".absent"), nil, 0o644)
		}
		return
	}
	if len(data) > 2_000_000 {
		return
	}
	_ = os.WriteFile(filepath.Join(c.dir(), name), data, 0o644)
}

// Restore writes the first-write backup back to the workspace (best-effort).
func (c *FileCheckpointer) Restore(rel string) error {
	if c == nil {
		return nil
	}
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	name := safeCheckpointName(rel)
	abs := filepath.Join(c.Root, filepath.FromSlash(rel))
	absent := filepath.Join(c.dir(), name+".absent")
	if _, err := os.Stat(absent); err == nil {
		return os.Remove(abs)
	}
	data, err := os.ReadFile(filepath.Join(c.dir(), name))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

func sanitizeSession(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 80 {
		out = out[len(out)-80:]
	}
	return out
}

func safeCheckpointName(rel string) string {
	s := strings.ReplaceAll(rel, "/", "__")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 200 {
		out = out[len(out)-200:]
	}
	return out
}
