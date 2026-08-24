package main

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/autoresearch"
)

// `slmcode autoresearch` is the one command in the tree that deliberately
// rewrites files the user wrote. These tests pin the safety properties of its
// flag surface — the parts that decide whether it can do that by accident.

func TestAutoresearchDefaultsToDryRunSafe(t *testing.T) {
	cmd := autoresearchCmd()

	apply := cmd.Flags().Lookup("apply")
	if apply == nil {
		t.Fatal("there is no --apply flag, so nothing gates the writes")
	}
	if apply.DefValue != "false" {
		t.Fatalf("--apply defaults to %q — running with no flags would mutate prompts", apply.DefValue)
	}
	// The help text must SAY so: a safety property nobody can read is a trap.
	for _, want := range []string{"DRY RUN", "--apply", "autoresearch: true", "--restore"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("the long help never mentions %q", want)
		}
	}
}

func TestAutoresearchHasTheDocumentedFlags(t *testing.T) {
	cmd := autoresearchCmd()
	for _, name := range []string{
		"max-experiments", "budget", "seed", "deterministic",
		"dry-run", "surface", "restore", "json",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is missing", name)
		}
	}
	// The budget flags must default to the package's bounded values rather than
	// to "unlimited".
	if got := cmd.Flags().Lookup("max-experiments").DefValue; got == "0" {
		t.Error("--max-experiments defaults to 0 (unbounded)")
	}
	if got := cmd.Flags().Lookup("budget").DefValue; got == "0s" {
		t.Error("--budget defaults to 0s (unbounded)")
	}
	if want := autoresearch.DefaultBudget().MaxExperiments; cmd.Flags().Lookup("max-experiments").DefValue !=
		itoaCmd(want) {
		t.Errorf("--max-experiments default drifted from autoresearch.DefaultBudget()")
	}
}

// The command's own --dry-run must not be shadowed away by the root's
// persistent flag of the same name: they mean different things, and the local
// one is what the help promises.
func TestAutoresearchDryRunFlagIsLocal(t *testing.T) {
	cmd := autoresearchCmd()
	f := cmd.Flags().Lookup("dry-run")
	if f == nil {
		t.Fatal("--dry-run is missing")
	}
	if !strings.Contains(f.Usage, "apply nothing") {
		t.Errorf("--dry-run usage is the root's, not this command's: %q", f.Usage)
	}
}

func TestAutoresearchSurfaceRendersWithoutAModel(t *testing.T) {
	root := t.TempDir()
	s, err := autoresearch.Reflect(autoresearch.Options{Root: root})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	// Both render paths must work on a workspace that has never been
	// initialized: `--surface` is the first thing anyone runs.
	if err := renderSurface(s, false); err != nil {
		t.Fatalf("renderSurface: %v", err)
	}
	if err := renderSurface(s, true); err != nil {
		t.Fatalf("renderSurface --json: %v", err)
	}
}

func TestAutoresearchResultRendersADryRun(t *testing.T) {
	root := t.TempDir()
	s, err := autoresearch.Reflect(autoresearch.Options{Root: root})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	res := autoresearch.Result{
		DryRun:     true,
		StopReason: autoresearch.StopDryRun,
		StopDetail: autoresearch.StopDryRun.Sentence(),
	}
	if err := renderAutoresearchResult(res, s, false); err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := renderAutoresearchResult(res, s, true); err != nil {
		t.Fatalf("render --json: %v", err)
	}
}

func itoaCmd(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
