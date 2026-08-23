package config

import (
	"encoding/json"
	"testing"
	"time"
)

// Studio PUTs a JSON body that decodes into config.Patch. Before the untyped
// half existed, a key with no hand-written pointer field here was silently
// dropped — which is how a knob could be in the schema, rendered in the
// settings page, and still do nothing when saved.
func TestPatchAppliesKeysWithNoTypedField(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		check func(*Config) bool
	}{
		{
			name:  "new bool",
			body:  `{"architect_editor": true}`,
			check: func(c *Config) bool { return c.ArchitectEditor },
		},
		{
			name:  "new enum",
			body:  `{"qa_bootstrap": "auto"}`,
			check: func(c *Config) bool { return c.QABootstrap == QABootstrapAuto },
		},
		{
			name:  "new int",
			body:  `{"repo_map_tokens": 1500}`,
			check: func(c *Config) bool { return c.RepoMapTokens == 1500 },
		},
		{
			name:  "duration as a string",
			body:  `{"shell_timeout": "45s"}`,
			check: func(c *Config) bool { return c.ShellTimeout == 45*time.Second },
		},
		{
			name:  "float",
			body:  `{"retrieval_min_score": 0.4}`,
			check: func(c *Config) bool { return c.RetrievalMinScore == 0.4 },
		},
		{
			name:  "typed field still wins its own path",
			body:  `{"max_parallel": 7}`,
			check: func(c *Config) bool { return c.MaxParallel == 7 },
		},
		{
			name:  "both halves in one body",
			body:  `{"max_parallel": 7, "evolve": false}`,
			check: func(c *Config) bool { return c.MaxParallel == 7 && !c.Evolve },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var p Patch
			if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
				t.Fatal(err)
			}
			c := Default(t.TempDir())
			c.ApplyPatch(p)
			if !tc.check(c) {
				t.Fatalf("patch %s did not take effect", tc.body)
			}
		})
	}
}

func TestPatchIgnoresUnknownAndBadKeys(t *testing.T) {
	var p Patch
	if err := json.Unmarshal([]byte(`{"not_a_key": 1, "evolve": false}`), &p); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Extra()["not_a_key"]; ok {
		t.Fatal("an unknown key must not be carried")
	}
	c := Default(t.TempDir())
	c.ApplyPatch(p)
	if c.Evolve {
		t.Fatal("the valid key in the same body should still apply")
	}

	// A bad value in the untyped half must not discard the rest of the save.
	var bad Patch
	if err := json.Unmarshal([]byte(`{"qa_bootstrap": "maybe", "memory_tokens": 900}`), &bad); err != nil {
		t.Fatal(err)
	}
	c2 := Default(t.TempDir())
	c2.ApplyPatch(bad)
	if c2.QABootstrap != DefaultQABootstrap {
		t.Fatalf("bad enum applied: %q", c2.QABootstrap)
	}
	if c2.MemoryTokens != 900 {
		t.Fatalf("the good key was dropped: %d", c2.MemoryTokens)
	}
}

func TestPatchWithValues(t *testing.T) {
	p := Patch{}.WithValues(map[string]any{"parallel": 6, "nope": 1})
	if p.Extra()["max_parallel"] != 6 {
		t.Fatalf("alias not canonicalized: %v", p.Extra())
	}
	if _, ok := p.Extra()["nope"]; ok {
		t.Fatal("unknown key carried")
	}
}
