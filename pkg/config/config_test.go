package config

import (
	"testing"

	"github.com/piotrlaczkowski/slmcode/pkg/permissions"
)

func TestApplyPatchPartialDoesNotClearDryRun(t *testing.T) {
	c := Default(t.TempDir())
	c.DryRun = true
	c.Permission = permissions.ModeDryRun

	model := "test-model"
	c.ApplyPatch(Patch{Model: &model})

	if !c.DryRun {
		t.Fatal("partial model update cleared dry_run")
	}
	if c.Permission != permissions.ModeDryRun {
		t.Fatalf("permission=%s", c.Permission)
	}
	if c.Model != model {
		t.Fatalf("model=%s", c.Model)
	}
}

func TestApplyPatchPermissionSync(t *testing.T) {
	c := Default(t.TempDir())

	perm := permissions.ModeReview
	c.ApplyPatch(Patch{Permission: &perm})
	if c.Permission != permissions.ModeReview || c.DryRun {
		t.Fatalf("review: perm=%s dry=%v", c.Permission, c.DryRun)
	}

	dry := true
	c.ApplyPatch(Patch{DryRun: &dry})
	if c.Permission != permissions.ModeDryRun || !c.DryRun {
		t.Fatalf("dry-run: perm=%s dry=%v", c.Permission, c.DryRun)
	}

	off := false
	c.ApplyPatch(Patch{DryRun: &off})
	if c.Permission != permissions.ModeAuto || c.DryRun {
		t.Fatalf("clear dry-run: perm=%s dry=%v", c.Permission, c.DryRun)
	}
}

func TestApplyPatchMaxRetriesZero(t *testing.T) {
	c := Default(t.TempDir())
	zero := 0
	c.ApplyPatch(Patch{MaxRetries: &zero})
	if c.MaxRetries != 0 {
		t.Fatalf("max_retries=%d want 0", c.MaxRetries)
	}
}

func TestNormalizePermissionFromYAML(t *testing.T) {
	root := t.TempDir()
	c := Default(root)
	c.Permission = "dry-run"
	c.DryRun = false
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.DryRun || loaded.Permission != permissions.ModeDryRun {
		t.Fatalf("load sync: dry=%v perm=%s", loaded.DryRun, loaded.Permission)
	}
}
