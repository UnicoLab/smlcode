package workspace

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/authstore"
	"gopkg.in/yaml.v3"
)

// Secret redaction on the way OUT of the tool layer.
//
// The path-based guards (CheckHarnessStateRead, HideFromListing) close
// ws_read / ws_grep / ws_glob / ws_list. They cannot close ws_shell, because
// ws_shell is a command allowlist and not a filesystem jail: `cat`, `grep`,
// `find`, `head` and `env` are all legitimately whitelisted and all of them
// can name .slmcode/auth.json (or read $OPENAI_API_KEY) one way or another.
//
// Whatever route the value takes, it has to come back through a tool result to
// be worth anything to an attacker — a prompt-injected model can only exfiltrate
// what lands in its context. So the last thing every tool result passes through
// is a scrub of the values the harness knows are secret. This is complete for
// the tool-result channel in a way that enumerating bad commands never is.
//
// It is NOT a substitute for keeping credentials out of the workspace; see the
// residual-risk note in the security docs.

// RedactedMarker replaces a secret value in a tool result.
const RedactedMarker = "[redacted: slmcode credential]"

// minRedactableLen avoids turning a short or empty configured value into a
// match against ordinary text.
const minRedactableLen = 12

// secretEnvVars are provider key variables whose values must never survive in
// a tool result (`env`, `printenv`, a test that echoes its environment).
// Keep this in sync with pkg/config.ResolveAPIKey — every variable that can
// BECOME the harness's API key must also be a variable that never survives a
// tool result.
var secretEnvVars = []string{
	"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY",
	"GROQ_API_KEY", "MISTRAL_API_KEY", "TOGETHER_API_KEY", "DEEPSEEK_API_KEY",
	"OPENROUTER_API_KEY", "XAI_API_KEY", "HF_TOKEN", "HUGGINGFACE_API_KEY",
	// OMLX_API_KEY was missing: pkg/config.ResolveAPIKey reads it for the
	// default `omlx` provider, so it was the ONE provider variable an `env`
	// through ws_shell still returned in full.
	"OMLX_API_KEY",
	"SLMCODE_API_KEY", "SLMCODE_STUDIO_TOKEN",
}

type secretSet struct {
	mu     sync.RWMutex
	values []string
	extra  []string
	loaded bool
	// stamp fingerprints auth.json so a key stored MID-RUN (Studio's auth
	// endpoint, `slmcode auth set` in another terminal) is picked up. The list
	// used to load exactly once per Workspace, so a key added after the first
	// tool call was never redacted for the rest of the run.
	stamp string
}

// authStamp fingerprints the credential sources without reading them.
func authStamp(slmDir string) string {
	if slmDir == "" {
		return ""
	}
	var b strings.Builder
	for _, p := range []string{authstore.Path(slmDir), filepath.Join(slmDir, "config.yaml")} {
		st, err := os.Stat(p)
		if err != nil {
			b.WriteString("-;")
			continue
		}
		b.WriteString(strconv.FormatInt(st.ModTime().UnixNano(), 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(st.Size(), 10))
		b.WriteByte(';')
	}
	return b.String()
}

// refresh reloads the secret list from the auth store, the project config file
// and the environment. scrub calls it whenever auth.json's fingerprint changes,
// so a key stored mid-run is covered too.
func (s *secretSet) refresh(slmDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = true
	s.stamp = authStamp(slmDir)
	s.values = s.values[:0]
	add := func(v string) {
		v = strings.TrimSpace(v)
		if len(v) < minRedactableLen {
			return
		}
		for _, existing := range s.values {
			if existing == v {
				return
			}
		}
		s.values = append(s.values, v)
	}
	if slmDir != "" {
		if st, err := authstore.Load(slmDir); err == nil && st != nil {
			for _, v := range st.Keys {
				add(v)
			}
		}
		// `api_key:` can also be persisted straight into config.yaml
		// (SLMCODE_PERSIST_API_KEY). That value is a live credential and was
		// not in the scrub set, so `cat .slmcode/config.yaml` through ws_shell
		// returned it verbatim. Read only the key fields — this is not a config
		// load, and must never fail a run.
		for _, v := range configFileKeys(filepath.Join(slmDir, "config.yaml")) {
			add(v)
		}
	}
	for _, k := range secretEnvVars {
		add(os.Getenv(k))
	}
	for _, v := range s.extra {
		add(v)
	}
}

// configKeyDoc is the minimum of config.yaml needed to find credential values.
// Deliberately a private struct rather than config.Config: this runs on the
// tool-result path and must not migrate, normalize or otherwise touch the file.
type configKeyDoc struct {
	APIKey          string `yaml:"api_key"`
	EmbeddingAPIKey string `yaml:"embedding_api_key"`
	Auth            struct {
		APIKey    string `yaml:"api_key"`
		APIKeyAlt string `yaml:"apikey"`
	} `yaml:"auth"`
}

func configFileKeys(path string) []string {
	data, err := os.ReadFile(path) //nolint:gosec // our own .slmcode/config.yaml path
	if err != nil {
		return nil
	}
	var doc configKeyDoc
	if yaml.Unmarshal(data, &doc) != nil {
		return nil
	}
	return []string{doc.APIKey, doc.EmbeddingAPIKey, doc.Auth.APIKey, doc.Auth.APIKeyAlt}
}

// addExtra registers a credential the harness knows about but cannot discover
// (a key set in config.yaml, or handed in on the command line).
func (s *secretSet) addExtra(values []string) {
	s.mu.Lock()
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < minRedactableLen {
			continue
		}
		s.extra = append(s.extra, v)
	}
	s.loaded = false
	s.mu.Unlock()
}

func (s *secretSet) scrub(slmDir, text string) string {
	if text == "" {
		return text
	}
	s.mu.RLock()
	stale := !s.loaded || s.stamp != authStamp(slmDir)
	s.mu.RUnlock()
	if stale {
		s.refresh(slmDir)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.values {
		if strings.Contains(text, v) {
			text = strings.ReplaceAll(text, v, RedactedMarker)
		}
	}
	return text
}

// RedactSecrets removes known credential values from a tool result.
func (w *Workspace) RedactSecrets(s string) string {
	if w == nil {
		return s
	}
	return w.secrets.scrub(w.SlmDir, s)
}

// RefreshSecrets re-reads the credential list (call after `slmcode auth set`).
// Not usually needed: scrub re-reads automatically when auth.json changes.
func (w *Workspace) RefreshSecrets() {
	if w == nil {
		return
	}
	w.secrets.refresh(w.SlmDir)
}

// AddSecrets registers credential values the workspace cannot discover on its
// own — notably `api_key:` set in config.yaml or passed as `--api-key`, which
// live only in the resolved Config. Call it once at wiring time; values shorter
// than minRedactableLen are ignored so a placeholder cannot blank out prose.
func (w *Workspace) AddSecrets(values ...string) {
	if w == nil || len(values) == 0 {
		return
	}
	w.secrets.addExtra(values)
}
