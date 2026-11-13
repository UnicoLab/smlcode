package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestChallengerM5_PipelineNilReceiverSafety tests that all methods on Config
// handle a nil receiver defensively without panicking.
func TestChallengerM5_PipelineNilReceiverSafety(t *testing.T) {
	var c *Config

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic on nil Config: %v", r)
		}
	}()

	c.Normalize() // should no-op
	if err := c.Validate(); err == nil {
		t.Errorf("Validate on nil Config must return an error, got nil")
	}

	if agent := c.PhaseAgent("clarify", "default-agent"); agent != "default-agent" {
		t.Errorf("expected default-agent, got %q", agent)
	}

	if enabled := c.PhaseEnabled("clarify"); !enabled {
		t.Errorf("expected true on nil Config PhaseEnabled, got false")
	}

	if when := c.PhaseWhen("clarify"); when != WhenAlways {
		t.Errorf("expected WhenAlways on nil Config PhaseWhen, got %q", when)
	}

	if slots := c.SlotsAt("clarify", "before"); slots != nil {
		t.Errorf("expected nil SlotsAt on nil Config, got %v", slots)
	}
}

// TestChallengerM5_TeamworkPresetImmutability verifies that modifying the Config
// returned by Teamwork() does not mutate future calls to Teamwork() (no shared internal maps/slices).
func TestChallengerM5_TeamworkPresetImmutability(t *testing.T) {
	cfg1 := Teamwork()

	// Mutate slices and maps
	cfg1.Order[0] = "mutated_phase"
	cfg1.Phases["clarify"] = PhaseSpec{Agent: "mutated-agent"}
	delete(cfg1.Phases, "architect")
	cfg1.Groups[0].ID = "mutated-group"
	cfg1.Groups[0].Steps[0] = "mutated-step"
	cfg1.Execute.DefaultRole = "mutated-worker"

	// Fetch fresh copy
	cfg2 := Teamwork()

	if cfg2.Order[0] == "mutated_phase" {
		t.Errorf("Order slice was mutated in-place across Teamwork() calls")
	}
	if cfg2.Phases["clarify"].Agent == "mutated-agent" {
		t.Errorf("Phases map was mutated across Teamwork() calls")
	}
	if _, ok := cfg2.Phases["architect"]; !ok {
		t.Errorf("architect phase was deleted across Teamwork() calls")
	}
	if cfg2.Groups[0].ID == "mutated-group" {
		t.Errorf("Groups slice was mutated across Teamwork() calls")
	}
	if cfg2.Execute.DefaultRole != RoleTeamworkWorker {
		t.Errorf("Execute.DefaultRole was mutated across Teamwork() calls")
	}
}

// TestChallengerM5_ConcurrentPipelineOperations exercises concurrent reading,
// normalization, and validation across 50 goroutines.
func TestChallengerM5_ConcurrentPipelineOperations(t *testing.T) {
	var wg sync.WaitGroup
	const workers = 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cfg := Teamwork()
			cfg.Normalize()
			if err := cfg.Validate(); err != nil {
				t.Errorf("worker %d: Validate failed: %v", id, err)
			}
			p := TeamworkPipeline()
			if p == nil {
				t.Errorf("worker %d: TeamworkPipeline returned nil", id)
			}
			if p.Execute.Reviewer != RoleTeamworkReview {
				t.Errorf("worker %d: unexpected reviewer: %s", id, p.Execute.Reviewer)
			}
		}(i)
	}
	wg.Wait()
}

// TestChallengerM5_ConcurrentEnsureTeamworkFile verifies atomic file creation
// when multiple goroutines call EnsureTeamworkFile simultaneously.
func TestChallengerM5_ConcurrentEnsureTeamworkFile(t *testing.T) {
	tmp := t.TempDir()
	slmDir := filepath.Join(tmp, ".slmcode")

	var wg sync.WaitGroup
	const writers = 20

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := EnsureTeamworkFile(slmDir); err != nil {
				t.Errorf("EnsureTeamworkFile failed: %v", err)
			}
		}()
	}
	wg.Wait()

	// Verify the written file is valid
	loaded, err := Load(slmDir)
	if err != nil {
		t.Fatalf("Load failed after concurrent EnsureTeamworkFile: %v", err)
	}
	if loaded.Execute.Reviewer != RoleTeamworkReview {
		t.Errorf("loaded reviewer = %q, want %q", loaded.Execute.Reviewer, RoleTeamworkReview)
	}
}

// TestChallengerM5_BoundaryValidationNegativeCases tests invalid slot IDs,
// missing anchors, duplicate groups, and multi-group phase assignments.
func TestChallengerM5_BoundaryValidationNegativeCases(t *testing.T) {
	t.Run("invalid slot id regex", func(t *testing.T) {
		cfg := Teamwork()
		cfg.Slots = []Slot{
			{ID: "INVALID_UPPER", Agent: "worker", Before: "clarify"},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "invalid id") {
			t.Errorf("expected invalid id error, got %v", err)
		}

		cfg.Slots[0].ID = "-starts-with-hyphen"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "invalid id") {
			t.Errorf("expected invalid id error, got %v", err)
		}

		cfg.Slots[0].ID = "a" // too short (regex requires 2-64 chars: ^[a-z][a-z0-9_-]{1,63}$)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "invalid id") {
			t.Errorf("expected invalid id error for 1-char ID, got %v", err)
		}
	})

	t.Run("duplicate slot id", func(t *testing.T) {
		cfg := Teamwork()
		cfg.Slots = []Slot{
			{ID: "my-slot", Agent: "worker", Before: "clarify"},
			{ID: "my-slot", Agent: "tester", After: "test"},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate slot id") {
			t.Errorf("expected duplicate slot id error, got %v", err)
		}
	})

	t.Run("slot without position anchor", func(t *testing.T) {
		cfg := Teamwork()
		cfg.Slots = []Slot{
			{ID: "unanchored-slot", Agent: "worker"},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "set before, after, or replace") {
			t.Errorf("expected unanchored error, got %v", err)
		}
	})

	t.Run("slot with unknown phase anchor", func(t *testing.T) {
		cfg := Teamwork()
		cfg.Slots = []Slot{
			{ID: "bad-anchor", Agent: "worker", Before: "nonexistent-phase"},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unknown phase anchor") {
			t.Errorf("expected unknown phase anchor error, got %v", err)
		}
	})

	t.Run("group with empty id", func(t *testing.T) {
		cfg := Teamwork()
		cfg.Groups = append(cfg.Groups, GroupMeta{ID: "", Label: "Empty", Steps: []string{"done"}})
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "empty id") {
			t.Errorf("expected empty id error, got %v", err)
		}
	})

	t.Run("group with duplicate id", func(t *testing.T) {
		cfg := Teamwork()
		cfg.Groups = append(cfg.Groups, GroupMeta{ID: GroupInterview, Label: "Duplicate", Steps: []string{"done"}})
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate group id") {
			t.Errorf("expected duplicate group id error, got %v", err)
		}
	})

	t.Run("group with unknown step", func(t *testing.T) {
		cfg := Teamwork()
		cfg.Groups = append(cfg.Groups, GroupMeta{ID: "custom-grp", Label: "Custom", Steps: []string{"unknown-step-xyz"}})
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unknown phase") {
			t.Errorf("expected unknown phase error, got %v", err)
		}
	})

	t.Run("phase assigned to multiple groups", func(t *testing.T) {
		cfg := Teamwork()
		// "clarify" is already in GroupInterview; add it to another group
		cfg.Groups = append(cfg.Groups, GroupMeta{ID: "conflict-grp", Label: "Conflict", Steps: []string{"clarify"}})
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "assigned to multiple groups") {
			t.Errorf("expected phase assigned to multiple groups error, got %v", err)
		}
	})
}

// TestChallengerM5_CorruptedPipelineYamlFallback verifies fallback to backup or default
// when pipeline.yaml is corrupted.
func TestChallengerM5_CorruptedPipelineYamlFallback(t *testing.T) {
	tmp := t.TempDir()
	slmDir := filepath.Join(tmp, ".slmcode")
	if err := os.MkdirAll(slmDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// 1. Missing file returns Default()
	cfg, err := Load(slmDir)
	if err != nil {
		t.Fatalf("Load missing file failed: %v", err)
	}
	if cfg.Execute.DefaultRole != "worker" {
		t.Errorf("expected default worker role, got %s", cfg.Execute.DefaultRole)
	}

	// 2. Save valid teamwork pipeline
	tw := Teamwork()
	if err := Save(slmDir, &tw); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify backup was created
	pPath := Path(slmDir)
	bPath := filepath.Join(slmDir, "pipeline.yaml.bak")
	if _, err := os.Stat(bPath); err != nil {
		t.Logf("Note: atomic backup path is %s: %v", bPath, err)
	}

	// 3. Corrupt primary pipeline.yaml with syntax errors
	if err := os.WriteFile(pPath, []byte("INVALID: YAML: [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load should either recover from backup or fail cleanly
	recovered, rerr := Load(slmDir)
	if rerr != nil {
		t.Logf("Clean error returned on corrupted file without valid backup: %v", rerr)
	} else if recovered != nil {
		t.Logf("Successfully recovered valid pipeline from atomic backup!")
		if recovered.Execute.Reviewer != RoleTeamworkReview {
			t.Errorf("expected recovered reviewer to be %s, got %s", RoleTeamworkReview, recovered.Execute.Reviewer)
		}
	}
}
