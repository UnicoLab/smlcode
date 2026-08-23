// Package authstore persists provider API keys in .slmcode/auth.json
// (prime-agent auth.json style) separate from config.yaml.
package authstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/internal/atomicfile"
)

// FileName is the auth store basename under .slmcode/.
const FileName = "auth.json"

// Store maps provider → API key (never logged).
type Store struct {
	Keys map[string]string `json:"keys"`
}

// mu guards the whole read-modify-write cycle, not just the individual file
// operations: Set used to release the lock between Load and Save, so a
// concurrent TUI + Studio write silently lost one of the keys.
var mu sync.Mutex

func normalizeProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "google" {
		return "gemini"
	}
	return p
}

// Path returns .slmcode/auth.json for a project root or slm dir.
func Path(slmDir string) string {
	return filepath.Join(slmDir, FileName)
}

// Load reads auth.json (empty store on missing file).
func Load(slmDir string) (*Store, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadLocked(slmDir)
}

func loadLocked(slmDir string) (*Store, error) {
	p := Path(slmDir)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{Keys: map[string]string{}}, nil
		}
		return nil, err
	}
	var s Store
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Keys == nil {
		s.Keys = map[string]string{}
	}
	return &s, nil
}

// Save writes auth.json with 0600 permissions.
func Save(slmDir string, s *Store) error {
	mu.Lock()
	defer mu.Unlock()
	return saveLocked(slmDir, s)
}

func saveLocked(slmDir string, s *Store) error {
	if s == nil {
		s = &Store{Keys: map[string]string{}}
	}
	if s.Keys == nil {
		s.Keys = map[string]string{}
	}
	if err := os.MkdirAll(slmDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(Path(slmDir), b, 0o600)
}

// Get returns a key for provider (normalized).
func Get(slmDir, provider string) (string, bool) {
	s, err := Load(slmDir)
	if err != nil || s == nil {
		return "", false
	}
	p := normalizeProvider(provider)
	if v := strings.TrimSpace(s.Keys[p]); v != "" {
		return v, true
	}
	for k, v := range s.Keys {
		if normalizeProvider(k) == p && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// Set stores a key for provider. Load→modify→Save happens under a single lock
// hold so concurrent writers cannot clobber each other's keys.
func Set(slmDir, provider, key string) error {
	mu.Lock()
	defer mu.Unlock()
	s, err := loadLocked(slmDir)
	if err != nil {
		return err
	}
	p := normalizeProvider(provider)
	if p == "" {
		p = strings.TrimSpace(provider)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		delete(s.Keys, p)
	} else {
		s.Keys[p] = key
	}
	return saveLocked(slmDir, s)
}

// PublicKeys returns provider names that have keys (values redacted).
func PublicKeys(slmDir string) map[string]bool {
	s, err := Load(slmDir)
	if err != nil || s == nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for k, v := range s.Keys {
		if strings.TrimSpace(v) != "" {
			out[normalizeProvider(k)] = true
		}
	}
	return out
}
