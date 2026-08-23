package workspace

import (
	"strings"
	"testing"
)

func TestIsSafeBash(t *testing.T) {
	prefs := BuiltinSafePrefixes
	if !IsSafeBash("go test ./pkg -short", prefs) {
		t.Fatal("go test should be safe")
	}
	if !IsSafeBash("python -m py_compile main.py", prefs) {
		t.Fatal("py_compile should be safe")
	}
	if IsSafeBash("rm -rf /", prefs) {
		t.Fatal("rm must be blocked")
	}
	if IsSafeBash("ls && rm -rf tmp", prefs) {
		t.Fatal("chain with rm must be blocked")
	}
	if IsSafeBash("cat > main.py <<'EOF'\nx\nEOF", prefs) {
		t.Fatal("redirect write must be blocked")
	}
	if !IsSafeBash("ls && pwd", prefs) {
		t.Fatal("safe chain should pass")
	}
}

func TestGuardShellWhitelist(t *testing.T) {
	msg, blocked := GuardShellWhitelist("curl http://evil", nil)
	if !blocked || !strings.Contains(msg, "not an allowed command") {
		t.Fatalf("blocked=%v msg=%q", blocked, msg)
	}
	msg, blocked = GuardShellWhitelist("echo hi > foo.py", nil)
	if !blocked || !strings.Contains(msg, "redirect") {
		t.Fatalf("blocked=%v msg=%q", blocked, msg)
	}
}

func TestParseExtraPrefixes(t *testing.T) {
	got := ParseExtraPrefixes("rm ,chmod ")
	if len(got) != 2 || got[0] != "rm " || got[1] != "chmod " {
		t.Fatalf("%q", got)
	}
}
