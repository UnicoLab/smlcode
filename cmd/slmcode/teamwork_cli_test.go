package main

import (
	"context"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// TestWorkflowFlagRegistration verifies the --workflow flag is registered with "teamwork" in help.
func TestWorkflowFlagRegistration(t *testing.T) {
	cmd := runCmd()
	f := cmd.Flags().Lookup("workflow")
	if f == nil {
		t.Fatal("expected --workflow flag to be registered on runCmd")
	}
	if f.DefValue != "" {
		t.Errorf("expected default workflow flag value to be empty string, got %q", f.DefValue)
	}
	if !strings.Contains(f.Usage, "teamwork") {
		t.Errorf("expected --workflow help string to mention 'teamwork', got: %s", f.Usage)
	}
}

// TestWorkflowValidationAndNormalization tests IsValidWorkflow and NormalizeWorkflow logic,
// including mixed casing, whitespace variants, and injection rejection.
func TestWorkflowValidationAndNormalization(t *testing.T) {
	validCases := []struct {
		input    string
		expected string
	}{
		{"teamwork", config.WorkflowTeamwork},
		{"TEAMWORK", config.WorkflowTeamwork},
		{"Teamwork", config.WorkflowTeamwork},
		{"TeAmWoRk", config.WorkflowTeamwork},
		{"  TeAmWoRk  ", config.WorkflowTeamwork},
		{"auto", config.WorkflowAuto},
		{"code", config.WorkflowAuto},
		{"", config.WorkflowAuto},
		{"   ", config.WorkflowAuto},
		{"\t\n\r ", config.WorkflowAuto},
		{"discover", config.WorkflowDiscover},
		{"interview", config.WorkflowDiscover},
		{"scope", config.WorkflowDiscover},
		{"hve-rpi", config.WorkflowHveRPI},
		{"rpi", config.WorkflowHveRPI},
		{"plan", config.WorkflowPlan},
		{"plan-first", config.WorkflowPlan},
		{"fast", config.WorkflowFast},
		{"fast-build", config.WorkflowFast},
		{"specialist", config.WorkflowSpecialist},
	}

	for _, tc := range validCases {
		if !config.IsValidWorkflow(tc.input) {
			t.Errorf("IsValidWorkflow(%q) = false, want true", tc.input)
		}
		norm := config.NormalizeWorkflow(tc.input)
		if norm != tc.expected {
			t.Errorf("NormalizeWorkflow(%q) = %q, want %q", tc.input, norm, tc.expected)
		}
	}

	invalidCases := []string{
		"invalid_workflow",
		"random",
		"teamwork123",
		"supercode",
		"; rm -rf /",
		"../../etc/passwd",
		"$(whoami)",
		"' OR '1'='1",
		"teamwork\nrm -rf /",
	}

	for _, tc := range invalidCases {
		if config.IsValidWorkflow(tc) {
			t.Errorf("IsValidWorkflow(%q) = true, want false", tc)
		}
	}
}

// TestRunCmdInvalidWorkflowExitCode verifies an unknown workflow returns exit code 2.
func TestRunCmdInvalidWorkflowExitCode(t *testing.T) {
	cmd := runCmd()
	// Set an invalid workflow flag
	if err := cmd.Flags().Set("workflow", "nonexistent_workflow_xyz"); err != nil {
		t.Fatalf("failed to set workflow flag: %v", err)
	}

	// Fake workspace directory
	tmp := t.TempDir()
	flagRoot = tmp
	cfg := config.Default(tmp)
	cfg.Calibrate = config.CalibrateOff
	if err := orchestrator.InitWorkspace(tmp, cfg); err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg.Save failed: %v", err)
	}

	// Execute runCmd with a query argument
	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, []string{"Build a test module"})
	if err == nil {
		t.Fatal("expected error for invalid workflow, got nil")
	}

	code := exitCodeFor(err)
	if code != 2 {
		t.Errorf("expected exit code 2 for invalid workflow, got %d (err: %v)", code, err)
	}
	if !strings.Contains(err.Error(), "unknown workflow") || !strings.Contains(err.Error(), "teamwork") {
		t.Errorf("expected actionable error message naming teamwork, got: %v", err)
	}
}

// TestRunCmdWorkflowTeamworkConfigured verifies that --workflow teamwork sets config.Workflow,
// sets Mode=full, and disables DynamicPipeline by calling the real applyRunFlags helper.
func TestRunCmdWorkflowTeamworkConfigured(t *testing.T) {
	for _, wfArg := range []string{"teamwork", "TEAMWORK", "TeamWork", "  teamwork  "} {
		t.Run(wfArg, func(t *testing.T) {
			cmd := runCmd()
			if err := cmd.Flags().Set("workflow", wfArg); err != nil {
				t.Fatalf("failed to set workflow flag: %v", err)
			}

			tmp := t.TempDir()
			cfg := config.Default(tmp)
			cfg.DynamicPipeline = true // Default is true
			cfg.Mode = config.ModeSpecialist

			query := "Build a test module"
			if err := applyRunFlags(cmd, cfg, &query); err != nil {
				t.Fatalf("applyRunFlags failed: %v", err)
			}

			if cfg.Workflow != config.WorkflowTeamwork {
				t.Errorf("expected Config.Workflow to be 'teamwork', got %q", cfg.Workflow)
			}
			if cfg.Mode != config.ModeFull {
				t.Errorf("expected Config.Mode to be 'full', got %q", cfg.Mode)
			}
			if cfg.DynamicPipeline {
				t.Errorf("expected Config.DynamicPipeline to be false when workflow is teamwork")
			}
		})
	}
}

// TestRunCmdWorkflowOtherPresetsConfigured verifies other presets configure Mode and Plan settings
// through production applyRunFlags.
func TestRunCmdWorkflowOtherPresetsConfigured(t *testing.T) {
	tests := []struct {
		name         string
		workflow     string
		wantWorkflow string
		wantMode     string
		wantClarify  string
		wantApprove  string
		wantSkills   []string
	}{
		{
			name:         "discover preset",
			workflow:     "discover",
			wantWorkflow: config.WorkflowDiscover,
			wantMode:     config.ModeFull,
			wantClarify:  plan.ClarifyAsk,
			wantApprove:  plan.PlanApproveModeAsk,
			wantSkills:   []string{"hve-ux-design", "hve-requirements"},
		},
		{
			name:         "hve-rpi preset",
			workflow:     "hve-rpi",
			wantWorkflow: config.WorkflowHveRPI,
			wantMode:     config.ModeFull,
			wantSkills:   []string{"hve-rpi"},
		},
		{
			name:         "plan preset",
			workflow:     "plan",
			wantWorkflow: config.WorkflowPlan,
			wantMode:     config.ModeFull,
			wantApprove:  plan.PlanApproveModeAsk,
		},
		{
			name:         "fast preset",
			workflow:     "fast",
			wantWorkflow: config.WorkflowFast,
			wantMode:     config.ModeFull,
			wantClarify:  plan.ClarifyOff,
			wantApprove:  plan.PlanApproveModeAuto,
		},
		{
			name:         "specialist preset",
			workflow:     "specialist",
			wantWorkflow: config.WorkflowSpecialist,
			wantMode:     config.ModeSpecialist,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runCmd()
			if err := cmd.Flags().Set("workflow", tc.workflow); err != nil {
				t.Fatalf("failed to set workflow flag: %v", err)
			}

			tmp := t.TempDir()
			cfg := config.Default(tmp)
			query := "Task query"

			if err := applyRunFlags(cmd, cfg, &query); err != nil {
				t.Fatalf("applyRunFlags failed: %v", err)
			}

			if cfg.Workflow != tc.wantWorkflow {
				t.Errorf("Workflow = %q, want %q", cfg.Workflow, tc.wantWorkflow)
			}
			if tc.wantMode != "" && cfg.Mode != tc.wantMode {
				t.Errorf("Mode = %q, want %q", cfg.Mode, tc.wantMode)
			}
			if tc.wantClarify != "" && cfg.ClarifyMode != tc.wantClarify {
				t.Errorf("ClarifyMode = %q, want %q", cfg.ClarifyMode, tc.wantClarify)
			}
			if tc.wantApprove != "" && cfg.PlanApprove != tc.wantApprove {
				t.Errorf("PlanApprove = %q, want %q", cfg.PlanApprove, tc.wantApprove)
			}
			for _, skill := range tc.wantSkills {
				found := false
				for _, s := range cfg.PinnedSkills {
					if s == skill {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("PinnedSkills missing expected skill %q; got: %v", skill, cfg.PinnedSkills)
				}
			}
		})
	}
}

// TestRunCmdWorkflowPrecedenceConfigFilePreserved verifies that when workflow: teamwork
// is configured in config.yaml, omitting --workflow on the CLI does NOT overwrite it with "auto".
func TestRunCmdWorkflowPrecedenceConfigFilePreserved(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default(tmp)
	cfg.Workflow = config.WorkflowTeamwork
	if err := orchestrator.InitWorkspace(tmp, cfg); err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg.Save failed: %v", err)
	}

	loaded, err := config.Load(tmp)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	if loaded.Workflow != config.WorkflowTeamwork {
		t.Fatalf("loaded.Workflow = %q, want %q", loaded.Workflow, config.WorkflowTeamwork)
	}

	// Create command without touching --workflow flag (flagChanged is false)
	cmd := runCmd()
	query := "Deploy teamwork feature"

	if err := applyRunFlags(cmd, loaded, &query); err != nil {
		t.Fatalf("applyRunFlags failed: %v", err)
	}

	// Workflow must remain teamwork, NOT overwritten with auto
	if loaded.Workflow != config.WorkflowTeamwork {
		t.Errorf("precedence violation: workflow was overwritten to %q, want %q", loaded.Workflow, config.WorkflowTeamwork)
	}
	if loaded.Mode != config.ModeFull {
		t.Errorf("expected Mode full, got %q", loaded.Mode)
	}
	if loaded.DynamicPipeline {
		t.Errorf("expected DynamicPipeline false, got true")
	}
}

// TestRunCmdWorkflowPrecedenceFlagOverridesConfigFile verifies that an explicit CLI flag
// overrides whatever workflow was configured in config.yaml.
func TestRunCmdWorkflowPrecedenceFlagOverridesConfigFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default(tmp)
	cfg.Workflow = config.WorkflowTeamwork
	if err := orchestrator.InitWorkspace(tmp, cfg); err != nil {
		t.Fatalf("InitWorkspace failed: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("cfg.Save failed: %v", err)
	}

	loaded, err := config.Load(tmp)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	cmd := runCmd()
	if err := cmd.Flags().Set("workflow", "fast"); err != nil {
		t.Fatalf("failed to set workflow flag: %v", err)
	}

	query := "Run fast build"
	if err := applyRunFlags(cmd, loaded, &query); err != nil {
		t.Fatalf("applyRunFlags failed: %v", err)
	}

	if loaded.Workflow != config.WorkflowFast {
		t.Errorf("expected flag override to fast, got %q", loaded.Workflow)
	}
	if loaded.ClarifyMode != plan.ClarifyOff {
		t.Errorf("expected ClarifyMode off for fast workflow, got %q", loaded.ClarifyMode)
	}
	if loaded.PlanApprove != plan.PlanApproveModeAuto {
		t.Errorf("expected PlanApprove auto for fast workflow, got %q", loaded.PlanApprove)
	}
}

// TestRunCmdWorkflowPrecedenceEnvVarPreserved verifies that SLMCODE_WORKFLOW is preserved
// when --workflow flag is not specified.
func TestRunCmdWorkflowPrecedenceEnvVarPreserved(t *testing.T) {
	t.Setenv("SLMCODE_WORKFLOW", "teamwork")

	tmp := t.TempDir()
	cfg := config.Default(tmp)
	cfg.ApplyEnv()

	if cfg.Workflow != config.WorkflowTeamwork {
		t.Fatalf("expected Workflow 'teamwork' from env, got %q", cfg.Workflow)
	}

	cmd := runCmd()
	query := "Run env test"
	if err := applyRunFlags(cmd, cfg, &query); err != nil {
		t.Fatalf("applyRunFlags failed: %v", err)
	}

	if cfg.Workflow != config.WorkflowTeamwork {
		t.Errorf("expected Workflow 'teamwork' to be preserved, got %q", cfg.Workflow)
	}
}

// TestRunCmdWorkflowPrecedenceFlagOverridesEnvVar verifies that explicit --workflow flag
// takes precedence over SLMCODE_WORKFLOW env var.
func TestRunCmdWorkflowPrecedenceFlagOverridesEnvVar(t *testing.T) {
	t.Setenv("SLMCODE_WORKFLOW", "teamwork")

	tmp := t.TempDir()
	cfg := config.Default(tmp)
	cfg.ApplyEnv()

	cmd := runCmd()
	if err := cmd.Flags().Set("workflow", "plan"); err != nil {
		t.Fatalf("failed to set workflow flag: %v", err)
	}

	query := "Run plan test"
	if err := applyRunFlags(cmd, cfg, &query); err != nil {
		t.Fatalf("applyRunFlags failed: %v", err)
	}

	if cfg.Workflow != config.WorkflowPlan {
		t.Errorf("expected CLI flag 'plan' to override env var 'teamwork', got %q", cfg.Workflow)
	}
	if cfg.PlanApprove != plan.PlanApproveModeAsk {
		t.Errorf("expected PlanApprove 'ask', got %q", cfg.PlanApprove)
	}
}

// TestRunCmdWorkflowFlagPrecedenceOverriddenBySubFlags verifies that explicit sub-flags
// (--dynamic, --mode) take precedence over the workflow preset defaults.
func TestRunCmdWorkflowFlagPrecedenceOverriddenBySubFlags(t *testing.T) {
	cmd := runCmd()
	if err := cmd.Flags().Set("workflow", "teamwork"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("dynamic", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("mode", "specialist"); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	cfg := config.Default(tmp)
	query := "Test query"

	if err := applyRunFlags(cmd, cfg, &query); err != nil {
		t.Fatal(err)
	}

	if cfg.Workflow != config.WorkflowTeamwork {
		t.Errorf("expected Workflow teamwork, got %q", cfg.Workflow)
	}
	// Explicit --dynamic beats teamwork's dynamic=false preset default
	if !cfg.DynamicPipeline {
		t.Errorf("expected explicit --dynamic to override teamwork default, got false")
	}
	// Explicit --mode beats teamwork's mode=full preset default
	if cfg.Mode != config.ModeSpecialist {
		t.Errorf("expected explicit --mode specialist to override teamwork default, got %q", cfg.Mode)
	}
}
