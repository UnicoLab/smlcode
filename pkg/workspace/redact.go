package workspace

import (
	"os"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/authstore"
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
var secretEnvVars = []string{
	"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY",
	"GROQ_API_KEY", "MISTRAL_API_KEY", "TOGETHER_API_KEY", "DEEPSEEK_API_KEY",
	"OPENROUTER_API_KEY", "XAI_API_KEY", "HF_TOKEN", "HUGGINGFACE_API_KEY",
	"SLMCODE_API_KEY", "SLMCODE_STUDIO_TOKEN",
}

type secretSet struct {
	mu     sync.RWMutex
	values []string
	loaded bool
}

// refresh reloads the secret list from the auth store and the environment.
// Cheap enough to do lazily once per Workspace; the store changes only when
// the operator runs `slmcode auth`.
func (s *secretSet) refresh(slmDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = true
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
	}
	for _, k := range secretEnvVars {
		add(os.Getenv(k))
	}
}

func (s *secretSet) scrub(slmDir, text string) string {
	if text == "" {
		return text
	}
	s.mu.RLock()
	loaded := s.loaded
	s.mu.RUnlock()
	if !loaded {
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
func (w *Workspace) RefreshSecrets() {
	if w == nil {
		return
	}
	w.secrets.refresh(w.SlmDir)
}
