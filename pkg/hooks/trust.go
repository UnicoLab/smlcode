package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Repo-supplied hooks are code execution by design.
//
// `.slmcode/hooks.json` lives INSIDE the project the harness was pointed at, so
// `git clone && slmcode run` used to be remote code execution: hooks_enabled
// defaults to true, Load read whatever the repository shipped, and the first
// tool call ran `bash -c <attacker string>` with no prompt and no notice.
// That is not the same trust level as a Makefile — the operator chooses to run
// `make`, but never chooses to fire a PreToolUse hook.
//
// The file is still fully supported; it is now opt-in per (project, content).
// The approval record lives in the USER's config directory, never in the
// repository, so a repo cannot ship its own approval. Any edit to hooks.json
// changes its digest and needs approval again.

// ErrUntrusted is returned by Load when a hooks file exists but the operator
// has not approved its current contents.
var ErrUntrusted = errors.New("hooks file is not trusted")

// TrustEnvVar force-trusts every hooks file. For CI images that build the
// hooks file themselves; never set it when running third-party repositories.
const TrustEnvVar = "SLMCODE_TRUST_HOOKS"

var trustMu sync.Mutex

// trustStorePath is the per-user approval database.
func trustStorePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", herr
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "slmcode", "trusted-hooks.json"), nil
}

// Digest is the SHA-256 of a hooks file's bytes, used as the approval key.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func loadTrustStore() map[string]string {
	out := map[string]string{}
	p, err := trustStorePath()
	if err != nil {
		return out
	}
	data, err := os.ReadFile(p) //nolint:gosec // our own per-user config path
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

// IsTrusted reports whether the operator has approved this exact file content
// for this exact hooks path.
func IsTrusted(path string, data []byte) bool {
	if envTrue(TrustEnvVar) {
		return true
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	trustMu.Lock()
	defer trustMu.Unlock()
	return loadTrustStore()[abs] == Digest(data)
}

// Trust records the operator's approval of a hooks file's current contents.
// Call it only from an interactive, explicit operator action.
func Trust(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs) //nolint:gosec // operator-named hooks file
	if err != nil {
		return err
	}
	store, err := trustStorePath()
	if err != nil {
		return err
	}
	trustMu.Lock()
	defer trustMu.Unlock()
	m := loadTrustStore()
	m[abs] = Digest(data)
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(store, buf, 0o600)
}

// Untrust removes an approval.
func Untrust(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	store, err := trustStorePath()
	if err != nil {
		return err
	}
	trustMu.Lock()
	defer trustMu.Unlock()
	m := loadTrustStore()
	delete(m, abs)
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		return err
	}
	return os.WriteFile(store, buf, 0o600)
}

// Describe renders every command a config would run, for the approval prompt
// and for the run banner. The operator must be able to SEE what they approve.
func (c Config) Describe() string {
	if len(c.Hooks) == 0 {
		return ""
	}
	events := make([]string, 0, len(c.Hooks))
	for e := range c.Hooks {
		events = append(events, e)
	}
	sort.Strings(events)
	var b strings.Builder
	for _, e := range events {
		for _, h := range c.Hooks[e] {
			cmd := strings.TrimSpace(h.Command)
			if cmd == "" {
				continue
			}
			matcher := strings.TrimSpace(h.Matcher)
			if matcher == "" {
				matcher = "*"
			}
			fmt.Fprintf(&b, "  %s [%s]: %s\n", e, matcher, cmd)
		}
	}
	return b.String()
}

// UntrustedError builds the operator-facing refusal, naming the exact commands
// that were NOT run and how to approve them.
func UntrustedError(path string, c Config) error {
	// Multi-line on purpose: this string is what the operator reads when the
	// harness refuses to run a repo's hooks, and it must show the commands.
	//nolint:staticcheck // ST1005: operator-facing, deliberately multi-line
	return fmt.Errorf(
		"%w: %s\n\n"+
			"This file makes the harness run shell commands on every tool call, and it lives "+
			"inside the project — a cloned repository can ship one. It was NOT loaded.\n\n"+
			"Commands it would run:\n%s\n"+
			"Read them, then approve with `slmcode hooks trust` (or set %s=1 for a hooks file "+
			"you generated yourself). Any later edit needs approval again.",
		ErrUntrusted, path, c.Describe(), TrustEnvVar)
}

func envTrue(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
