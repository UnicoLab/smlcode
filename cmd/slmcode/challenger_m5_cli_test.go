package main

import (
	"context"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// TestChallengerM5_WorkflowPrecedenceHierarchy verifies the authoritative precedence:
// CLI Flags > SLMCODE_* Environment Variables > Project config.yaml > Defaults.
func TestChallengerM5_WorkflowPrecedenceHierarchy(t *testing.T) {
	tmp := t.TempDir()

	// Level 1: Project config.yaml sets workflow: "discover"
	cfgFile := config.Default(tmp)
	cfgFile.Workflow = config.WorkflowDiscover
	if err := orchestrator.InitWorkspace(tmp, cfgFile); err != nil {
		t.Fatal(err)
	}
	if err := cfgFile.Save(); err != nil {
		t.Fatal(err)
	}

	// Case A: No env, no flag -> project config wins ("discover")
	loaded, err := config.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workflow != config.WorkflowDiscover {
		t.Errorf("expected workflow %s from config file, got %s", config.WorkflowDiscover, loaded.Workflow)
	}

	// Case B: Env set to "fast", no flag -> Env wins over config file
	t.Setenv("SLMCODE_WORKFLOW", "fast")
	loadedEnv, err := config.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if loadedEnv.Workflow != config.WorkflowFast {
		t.Errorf("expected workflow %s from env var, got %s", config.WorkflowFast, loadedEnv.Workflow)
	}

	// Case C: Flag set to "teamwork", env set to "fast", config set to "discover" -> Flag wins over ALL
	cmd := runCmd()
	if err := cmd.Flags().Set("workflow", "teamwork"); err != nil {
		t.Fatal(err)
	}
	query := "Deploy microservice"
	if err := applyRunFlags(cmd, loadedEnv, &query); err != nil {
		t.Fatalf("applyRunFlags failed: %v", err)
	}
	if loadedEnv.Workflow != config.WorkflowTeamwork {
		t.Errorf("expected workflow %s from CLI flag, got %s", config.WorkflowTeamwork, loadedEnv.Workflow)
	}
	if loadedEnv.Mode != config.ModeFull {
		t.Errorf("expected Mode %s under teamwork workflow, got %s", config.ModeFull, loadedEnv.Mode)
	}
	if loadedEnv.DynamicPipeline {
		t.Errorf("expected DynamicPipeline=false under teamwork workflow, got true")
	}
}

// TestChallengerM5_AdversarialWorkflowInjectionAttempts tests an array of hostile
// and malformed inputs to ensure they fail validation with exit code 2 and never escape.
func TestChallengerM5_AdversarialWorkflowInjectionAttempts(t *testing.T) {
	hostileInputs := []string{
		"; rm -rf /",
		"`rm -rf /`",
		"$(whoami)",
		"${PATH}",
		"teamwork; reboot",
		"teamwork && echo hacked",
		"../../../../etc/passwd",
		"' OR 1=1 --",
		"\" ; DROP TABLE users; --",
		"teamwork\x00injection",
		"teamwork\nteamwork",
		"teamwork\r\nteamwork",
		strings.Repeat("A", 4096), // buffer overflow probe
	}

	for _, input := range hostileInputs {
		t.Run(input, func(t *testing.T) {
			if config.IsValidWorkflow(input) {
				t.Fatalf("CRITICAL SECURITY: IsValidWorkflow accepted malicious input %q", input)
			}

			cmd := runCmd()
			if err := cmd.Flags().Set("workflow", input); err != nil {
				// Flag parsing failure is acceptable
				return
			}

			tmp := t.TempDir()
			cfg := config.Default(tmp)
			query := "test query"
			err := applyRunFlags(cmd, cfg, &query)
			if err == nil {
				t.Fatalf("expected applyRunFlags to fail for input %q, got nil", input)
			}

			code := exitCodeFor(err)
			if code != 2 {
				t.Errorf("expected exit code 2 for invalid workflow %q, got %d (err: %v)", input, code, err)
			}
		})
	}
}

// TestChallengerM5_WhitespaceAndUnicodeTolerance verifies that valid teamwork
// workflow arguments with leading/trailing whitespace or mixed tabs/newlines are
// normalized cleanly.
func TestChallengerM5_WhitespaceAndUnicodeTolerance(t *testing.T) {
	variants := []string{
		"teamwork",
		"  teamwork  ",
		"\tteamwork\n",
		"\r\n\t  teamwork  \t\r\n",
		"TEAMWORK",
		"TeAmWoRk",
	}

	for _, v := range variants {
		t.Run(v, func(t *testing.T) {
			if !config.IsValidWorkflow(v) {
				t.Errorf("IsValidWorkflow(%q) = false, want true", v)
			}
			norm := config.NormalizeWorkflow(v)
			if norm != config.WorkflowTeamwork {
				t.Errorf("NormalizeWorkflow(%q) = %q, want %q", v, norm, config.WorkflowTeamwork)
			}

			cmd := runCmd()
			if err := cmd.Flags().Set("workflow", v); err != nil {
				t.Fatal(err)
			}
			tmp := t.TempDir()
			cfg := config.Default(tmp)
			query := "sample query"
			if err := applyRunFlags(cmd, cfg, &query); err != nil {
				t.Fatalf("applyRunFlags failed on valid variant %q: %v", v, err)
			}
			if cfg.Workflow != config.WorkflowTeamwork {
				t.Errorf("cfg.Workflow = %q, want %q", cfg.Workflow, config.WorkflowTeamwork)
			}
		})
	}
}

// TestChallengerM5_RunCmdMissingQueryError verifies that runCmd refuses to run with empty query.
func TestChallengerM5_RunCmdMissingQueryError(t *testing.T) {
	cmd := runCmd()
	_ = cmd.Flags().Set("workflow", "teamwork")

	tmp := t.TempDir()
	flagRoot = tmp
	cfg := config.Default(tmp)
	_ = orchestrator.InitWorkspace(tmp, cfg)
	_ = cfg.Save()

	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, []string{}) // no query arguments
	if err == nil {
		t.Fatal("expected error when running with empty query arguments, got nil")
	}
	if !strings.Contains(err.Error(), "nothing to run") {
		t.Errorf("expected 'nothing to run' error, got: %v", err)
	}
}
