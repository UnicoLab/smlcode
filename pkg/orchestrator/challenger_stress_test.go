package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// TestChallengerTesterPhaseDiskSupremacyNegativeScenarios verifies that disk supremacy
// ONLY activates when real disk evidence actually satisfies the rename query, and
// stays off when the file is missing, the query is mismatched, or the intent is not a rename.
func TestChallengerTesterPhaseDiskSupremacyNegativeScenarios(t *testing.T) {
	testerRejectJSON := `{"passed":false,"summary":"tester thought file was missing","failures":["not found"]}`
	exec := &mockSubAgentExec{
		fn: func(agentID, input string) string {
			return testerRejectJSON
		},
	}

	t.Run("file not on disk keeps supremacy off and preserves tester rejection", func(t *testing.T) {
		o := setupTestOrchestrator(t, exec)
		// Explicitly ensure new_name.go is NOT on disk
		board := &plan.Board{
			QueryID: "rename-missing-run",
			Query:   "rename old_name.go to new_name.go",
			Tasks: []plan.Task{
				{
					ID:     "T1",
					Title:  "rename old_name.go to new_name.go",
					Files:  []string{"new_name.go"},
					Status: plan.StatusDone,
					Column: plan.ColInReview,
				},
			},
		}

		if renameDiskOK(o.cfg.Root, board.Query, board) {
			t.Fatalf("renameDiskOK should be false when file does not exist on disk")
		}

		verdict, err := o.runTesterPhase(context.Background(), board.Query, board, preTest{})
		if err != nil {
			t.Fatalf("runTesterPhase returned unexpected error: %v", err)
		}
		if verdict.FromDisk {
			t.Errorf("verdict.FromDisk must be false when file is missing from disk")
		}
		tr := plan.ParseTesterJSON(verdict.Output)
		if tr.Passed {
			t.Errorf("expected tester rejection to remain passed=false, got passed=true: %s", verdict.Output)
		}
		if !strings.Contains(verdict.Output, "tester thought file was missing") {
			t.Errorf("expected original tester rejection summary, got: %s", verdict.Output)
		}
	})

	t.Run("rename mismatch on disk keeps supremacy off and preserves tester rejection", func(t *testing.T) {
		o := setupTestOrchestrator(t, exec)
		// Write a file with an UNRELATED name
		unrelated := filepath.Join(o.cfg.Root, "unrelated_target.go")
		if err := os.WriteFile(unrelated, []byte("package test\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		board := &plan.Board{
			QueryID: "rename-mismatch-run",
			Query:   "rename old_name.go to expected_name.go",
			Tasks: []plan.Task{
				{
					ID:     "T1",
					Title:  "rename old_name.go to expected_name.go",
					Files:  []string{"expected_name.go"},
					Status: plan.StatusDone,
					Column: plan.ColInReview,
				},
			},
		}

		if renameDiskOK(o.cfg.Root, board.Query, board) {
			t.Fatalf("renameDiskOK should be false when disk file does not match rename target")
		}

		verdict, err := o.runTesterPhase(context.Background(), board.Query, board, preTest{})
		if err != nil {
			t.Fatalf("runTesterPhase returned unexpected error: %v", err)
		}
		if verdict.FromDisk {
			t.Errorf("verdict.FromDisk must be false for mismatched target")
		}
		tr := plan.ParseTesterJSON(verdict.Output)
		if tr.Passed {
			t.Errorf("expected passed=false, got passed=true: %s", verdict.Output)
		}
	})

	t.Run("non-rename query keeps supremacy off even if files exist on disk", func(t *testing.T) {
		o := setupTestOrchestrator(t, exec)
		// Write a file that exists
		existing := filepath.Join(o.cfg.Root, "feature.go")
		if err := os.WriteFile(existing, []byte("package test\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		board := &plan.Board{
			QueryID: "non-rename-run",
			Query:   "implement feature in feature.go",
			Tasks: []plan.Task{
				{
					ID:     "T1",
					Title:  "implement feature",
					Files:  []string{"feature.go"},
					Status: plan.StatusDone,
					Column: plan.ColInReview,
				},
			},
		}

		if renameDiskOK(o.cfg.Root, board.Query, board) {
			t.Fatalf("renameDiskOK must be false for non-rename query")
		}

		verdict, err := o.runTesterPhase(context.Background(), board.Query, board, preTest{})
		if err != nil {
			t.Fatalf("runTesterPhase returned unexpected error: %v", err)
		}
		if verdict.FromDisk {
			t.Errorf("verdict.FromDisk must be false for non-rename query")
		}
		tr := plan.ParseTesterJSON(verdict.Output)
		if tr.Passed {
			t.Errorf("expected passed=false, got passed=true: %s", verdict.Output)
		}
	})
}

// TestChallengerResumeReloadDurableDocsEdgeCases tests corrupted, empty, truncated,
// and uninitialized edge cases for reloadDurableDocs.
func TestChallengerResumeReloadDurableDocsEdgeCases(t *testing.T) {
	exec := &mockSubAgentExec{}

	t.Run("non-existent files do not pollute state or error", func(t *testing.T) {
		o := setupTestOrchestrator(t, exec)
		// Ensure slmDir exists but contains no docs
		if err := os.MkdirAll(o.cfg.SlmDir(), 0o755); err != nil {
			t.Fatal(err)
		}

		o.reloadDurableDocs()

		if _, ok := o.shared.GetGlobal("prd"); ok {
			t.Errorf("prd global should not be set when PRD.md does not exist")
		}
		if _, ok := o.shared.GetGlobal("contract"); ok {
			t.Errorf("contract global should not be set when CONTRACT.md does not exist")
		}
		if _, ok := o.shared.GetGlobal("review"); ok {
			t.Errorf("review global should not be set when REVIEW.md does not exist")
		}
	})

	t.Run("empty zero-byte files are ignored and do not overwrite existing state", func(t *testing.T) {
		o := setupTestOrchestrator(t, exec)
		slmDir := o.cfg.SlmDir()
		if err := os.MkdirAll(slmDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// Pre-populate with existing state
		o.shared.SetGlobal("prd", "initial-prd")
		_ = o.store.WriteBounded(contextstore.DocPRD, "initial-prd")

		// Create 0-byte files
		if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocPRD), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocContract), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocReview), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}

		o.reloadDurableDocs()

		// 0-byte files should not overwrite existing values in shared state
		prdVal, ok := o.shared.GetGlobal("prd")
		if !ok || prdVal != "initial-prd" {
			t.Errorf("prd global should remain 'initial-prd', got %q (ok=%v)", prdVal, ok)
		}
		if _, ok := o.shared.GetGlobal("contract"); ok {
			t.Errorf("contract global should not be set for 0-byte file")
		}
		if _, ok := o.shared.GetGlobal("review"); ok {
			t.Errorf("review global should not be set for 0-byte file")
		}
	})

	t.Run("truncated and binary files are safely loaded into store and shared state", func(t *testing.T) {
		o := setupTestOrchestrator(t, exec)
		slmDir := o.cfg.SlmDir()
		if err := os.MkdirAll(slmDir, 0o755); err != nil {
			t.Fatal(err)
		}

		binaryData := "\x00\xff\xfe\x01\x02malformed\x00data"
		truncatedContract := "# Cont"
		malformedReview := "{\"truncated\": "

		if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocPRD), []byte(binaryData), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocContract), []byte(truncatedContract), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slmDir, contextstore.DocReview), []byte(malformedReview), 0o644); err != nil {
			t.Fatal(err)
		}

		o.reloadDurableDocs()

		if val, ok := o.shared.GetGlobal("prd"); !ok || val != binaryData {
			t.Errorf("prd global expected binary data, got %q (ok=%v)", val, ok)
		}
		if val, ok := o.shared.GetGlobal("contract"); !ok || val != truncatedContract {
			t.Errorf("contract global expected %q, got %q (ok=%v)", truncatedContract, val, ok)
		}
		if val, ok := o.shared.GetGlobal("review"); !ok || val != malformedReview {
			t.Errorf("review global expected %q, got %q (ok=%v)", malformedReview, val, ok)
		}
	})

	t.Run("nil receiver and partial initialization permutations are panic-free", func(t *testing.T) {
		// 1. Nil orchestrator
		var nilOrch *Orchestrator
		nilOrch.reloadDurableDocs()

		// 2. Uninitialized orchestrator struct
		emptyOrch := &Orchestrator{}
		emptyOrch.reloadDurableDocs()

		// 3. Orchestrator with cfg, but nil store and nil shared
		root := t.TempDir()
		slmDir := filepath.Join(root, ".slmcode")
		if err := os.MkdirAll(slmDir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(slmDir, contextstore.DocPRD), []byte("# PRD"), 0o644)
		_ = os.WriteFile(filepath.Join(slmDir, contextstore.DocContract), []byte("# CONTRACT"), 0o644)
		_ = os.WriteFile(filepath.Join(slmDir, contextstore.DocReview), []byte("# REVIEW"), 0o644)

		oCfgOnly := setupTestOrchestrator(t, exec)
		oCfgOnly.store = nil
		oCfgOnly.shared = nil
		oCfgOnly.reloadDurableDocs() // must not panic

		// 4. Orchestrator with cfg and store, nil shared
		oStoreOnly := setupTestOrchestrator(t, exec)
		oStoreOnly.shared = nil
		oStoreOnly.reloadDurableDocs() // must not panic

		// 5. Orchestrator with cfg and shared, nil store
		oSharedOnly := setupTestOrchestrator(t, exec)
		oSharedOnly.store = nil
		oSharedOnly.reloadDurableDocs() // must not panic
	})
}
