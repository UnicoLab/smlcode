package config

import (
	"testing"
	"time"
)

func TestMigrateRenamesAndVersions(t *testing.T) {
	tests := []struct {
		name        string
		doc         map[string]any
		wantFrom    int
		wantAbsent  []string
		wantPresent map[string]any
	}{
		{
			name:        "unversioned file reports version 0",
			doc:         map[string]any{"provider": "ollama"},
			wantFrom:    0,
			wantPresent: map[string]any{"config_version": CurrentConfigVersion},
		},
		{
			name:       "the embedded root path is dropped",
			doc:        map[string]any{"root": "/home/someone-else/project"},
			wantFrom:   0,
			wantAbsent: []string{"root"},
		},
		{
			name:        "legacy short keys are renamed",
			doc:         map[string]any{"parallel": 7, "qa_rounds": 5, "perm": "review"},
			wantFrom:    0,
			wantAbsent:  []string{"parallel", "qa_rounds", "perm"},
			wantPresent: map[string]any{"max_parallel": 7, "qa_gate_max_rounds": 5, "permission": "review"},
		},
		{
			name:        "a rename never clobbers the canonical spelling",
			doc:         map[string]any{"parallel": 7, "max_parallel": 3},
			wantFrom:    0,
			wantPresent: map[string]any{"max_parallel": 3},
		},
		{
			name:        "a current file is left alone",
			doc:         map[string]any{"config_version": CurrentConfigVersion, "provider": "openai"},
			wantFrom:    CurrentConfigVersion,
			wantPresent: map[string]any{"provider": "openai", "config_version": CurrentConfigVersion},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from := migrate(tc.doc)
			if from != tc.wantFrom {
				t.Errorf("version = %d, want %d", from, tc.wantFrom)
			}
			for _, k := range tc.wantAbsent {
				if _, ok := tc.doc[k]; ok {
					t.Errorf("%q should have been migrated away: %v", k, tc.doc)
				}
			}
			for k, want := range tc.wantPresent {
				if got := tc.doc[k]; got != want {
					t.Errorf("%q = %v, want %v", k, got, want)
				}
			}
		})
	}
}

func TestLoadMigratesAnOldFile(t *testing.T) {
	isolateHome(t)
	root := writeProject(t, `root: /home/someone-else/another-machine
provider: ollama
parallel: 7
qa_rounds: 5
escalate_ask_timeout: 30000000000
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Root != root {
		t.Fatalf("root = %q — a stale absolute path from the file was honored", cfg.Root)
	}
	if cfg.MaxParallel != 7 || cfg.QAGateMaxRounds != 5 {
		t.Fatalf("legacy keys lost: parallel=%d rounds=%d", cfg.MaxParallel, cfg.QAGateMaxRounds)
	}
	// yaml.v3 used to marshal time.Duration as nanoseconds; that spelling must
	// still decode to the same wall time.
	if cfg.EscalateAskTimeout != 30*time.Second {
		t.Fatalf("escalate_ask_timeout = %s", cfg.EscalateAskTimeout)
	}
	prov := cfg.Provenance()
	if !prov.Migrated || prov.FromVersion != 0 {
		t.Fatalf("migration not reported: migrated=%v from=%d", prov.Migrated, prov.FromVersion)
	}
	if len(MigrationNotes(0)) == 0 {
		t.Fatal("MigrationNotes(0) should describe the upgrade")
	}
}

func TestLoadKeepsGoingPastOneBadKey(t *testing.T) {
	isolateHome(t)
	root := writeProject(t, "max_parallel: not-a-number\nfast_model: tiny\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("one bad key must not make the workspace unopenable: %v", err)
	}
	if cfg.FastModel != "tiny" {
		t.Fatalf("the rest of the file was discarded: fast_model=%q", cfg.FastModel)
	}
	if cfg.MaxParallel != DefaultMaxParallel {
		t.Fatalf("the bad value was applied: %d", cfg.MaxParallel)
	}
	if len(cfg.Provenance().Warnings) == 0 {
		t.Fatal("the bad key must be reported")
	}
}
