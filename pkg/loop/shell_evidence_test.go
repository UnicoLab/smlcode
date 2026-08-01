package loop

import "testing"

func TestHasShellFailureEvidence(t *testing.T) {
	if !hasShellFailureEvidence("Observation: exit error: exit status 1\nTraceback (most recent call last):") {
		t.Fatal("expected shell failure")
	}
	if !hasShellFailureEvidence("argparse.ArgumentError: argument --help: conflicting option string") {
		t.Fatal("expected argparse failure")
	}
	if hasShellFailureEvidence("Observation: wrote main.py (238 bytes)\n## Disk evidence\n- modified: main.py") {
		t.Fatal("clean write must not look like shell failure")
	}
}
