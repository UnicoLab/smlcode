package installmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

const (
	DirName  = "slmcode"
	FileName = "install.json"
)

// Meta records how SLMCode was installed so `slmcode update` can refresh it.
type Meta struct {
	Source      string `json:"source"`
	GoLangGraph string `json:"golanggraph,omitempty"`
	Prefix      string `json:"prefix"`
	Mode        string `json:"mode"`   // user | system
	Method      string `json:"method"` // source | binary
	Version     string `json:"version"`
	GitCommit   string `json:"git_commit,omitempty"`
	Binary      string `json:"binary"`
	Repo        string `json:"repo,omitempty"`
	InstalledAt string `json:"installed_at"`
}

// Dir is the preferred config directory (~/.config/slmcode).
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, DirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", DirName), nil
}

func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, FileName), nil
}

func candidatePaths() []string {
	var out []string
	if p, err := Path(); err == nil {
		out = append(out, p)
	}
	if d, err := os.UserConfigDir(); err == nil {
		out = append(out, filepath.Join(d, DirName, FileName))
	}
	return out
}

func Load() (*Meta, error) {
	var lastErr error
	for _, p := range candidatePaths() {
		data, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		var m Meta
		if err := json.Unmarshal(data, &m); err != nil {
			lastErr = err
			continue
		}
		return &m, nil
	}
	if lastErr == nil {
		lastErr = os.ErrNotExist
	}
	return nil, lastErr
}

func Save(m *Meta) error {
	if m == nil {
		return nil
	}
	if m.InstalledAt == "" {
		m.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	}
	d, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	p := filepath.Join(d, FileName)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(p, append(data, '\n'), 0o644)
}
