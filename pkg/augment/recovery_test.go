package augment

import (
	"strings"
	"testing"
)

func TestFailureRecovery(t *testing.T) {
	edit := FailureRecovery("ws_edit", "main.py")
	if !strings.Contains(edit, "ws_read main.py") || !strings.Contains(edit, "Never escalate") {
		t.Fatal(edit)
	}
	write := FailureRecovery("ws_write", "main.py")
	if !strings.Contains(write, "already exists") {
		t.Fatal(write)
	}
}
