package orchestrator

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// TestShellWhitelistNoticeMatchesTheGuard pins defect 8.
//
// The notice is what the operator is told the harness will refuse. It was a
// hand-written sentence naming "python, node, make, npx, `go run`, cp, mv and
// sed" and it went stale the moment the shell guard grew the exec-flag and
// out-of-jail audits — so an operator reading it concluded `find -exec`,
// `go test -exec`, `go generate` and `cmake -P` were allowed. It is now derived
// from workspace.GuardShellWhitelist; this test is the thing that keeps the
// derivation honest.
func TestShellWhitelistNoticeMatchesTheGuard(t *testing.T) {
	for _, p := range shellRefusedProbes {
		if _, blocked := workspace.GuardShellWhitelist(p.sample, nil); !blocked {
			t.Errorf("the notice claims %q is refused, but %q is allowed", p.label, p.sample)
		}
	}
	for _, p := range shellAllowedProbes {
		if reason, blocked := workspace.GuardShellWhitelist(p.sample, nil); blocked {
			t.Errorf("the notice claims %q still runs, but %q is refused: %s", p.label, p.sample, reason)
		}
	}
}

// TestShellWhitelistNoticeNamesEveryRefusalClass asserts the notice actually
// mentions each audit the security review added. A probe list that silently
// lost an entry would keep this test passing but the operator uninformed.
func TestShellWhitelistNoticeNamesEveryRefusalClass(t *testing.T) {
	notice := ShellWhitelistNotice
	for _, want := range []string{
		"env", "find -exec", "-toolexec", "go generate", "cmake -P",
		"mkdir", "outside the project root", "shell_allow",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice never mentions %q:\n%s", want, notice)
		}
	}
	// …and it must still say verification is unaffected, or an operator turns
	// the whitelist off to get their tests back.
	for _, want := range []string{"go test", "pytest", "Still allowed"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice never says %q is still allowed:\n%s", want, notice)
		}
	}
}

// TestShellWhitelistNoticeIsRefreshedFromTheGuard covers the case that made the
// old constant wrong: a NEW refusal class must be able to reach the operator
// without anyone remembering to rewrite prose.
func TestShellWhitelistNoticeIsRefreshedFromTheGuard(t *testing.T) {
	saved := shellRefusedProbes
	t.Cleanup(func() { shellRefusedProbes = saved })

	shellRefusedProbes = append(append([]shellNoticeProbe{}, saved...),
		shellNoticeProbe{"a brand-new refusal class", "python -c 'import os'"},
		// A claim the guard does NOT back must not survive into the notice.
		shellNoticeProbe{"something the guard allows", "go test ./..."},
	)
	got := buildShellWhitelistNotice()
	if !strings.Contains(got, "a brand-new refusal class") {
		t.Errorf("a real refusal never reached the notice:\n%s", got)
	}
	if strings.Contains(got, "something the guard allows") {
		t.Errorf("an unbacked claim reached the notice:\n%s", got)
	}
}
