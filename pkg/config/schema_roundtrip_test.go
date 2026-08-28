package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ── Every schema key must actually be settable ───────────────────────────
//
// The schema is what `slmcode config set`, `config show --all`, the Studio's
// settings form and the config docs are all generated from. A key that appears
// there but does not round-trip through Set/Get is a setting the user is
// offered, types a value into, and which then does nothing — with no error to
// tell them so.
//
// Nothing checked that the two agreed. This walks every patchable key and
// proves it.

// sampleFor returns a value the schema itself says is valid for a field.
func sampleFor(f FieldSchema) (any, bool) {
	if len(f.Enum) > 0 {
		for _, v := range f.Enum {
			if strings.TrimSpace(v) != "" {
				return v, true
			}
		}
		return nil, false
	}
	switch f.Type {
	case "string":
		return "round-trip-probe", true
	case "int":
		return 7, true
	case "float":
		return 0.25, true
	case "bool":
		return true, true
	case "duration":
		return "90s", true
	case "string[]":
		return []string{"alpha", "beta"}, true
	default:
		// map / list shapes have no single safe sample; the typed patch tests
		// cover those individually.
		return nil, false
	}
}

func TestEverySettableSchemaKeyRoundTrips(t *testing.T) {
	for _, f := range Schema() {
		if _, patchable := PatchableField(f.Key); !patchable {
			continue
		}
		sample, ok := sampleFor(f)
		if !ok {
			continue
		}
		t.Run(f.Key, func(t *testing.T) {
			c := Default(t.TempDir())
			if err := c.Set(f.Key, sample); err != nil {
				t.Fatalf("Set(%q, %v) on a %s field: %v", f.Key, sample, f.Type, err)
			}
			got, ok := c.Get(f.Key)
			if !ok {
				t.Fatalf("Get(%q) reported the key does not exist, but Set accepted it", f.Key)
			}
			// Compared as rendered text: Set may normalize (a duration, a
			// lower-cased enum), and the contract is that the value STUCK, not
			// that it survived byte-for-byte.
			if strings.TrimSpace(render(got)) == "" && strings.TrimSpace(render(sample)) != "" {
				t.Errorf("Set(%q, %v) was accepted but Get returned empty — the key does nothing",
					f.Key, sample)
			}
		})
	}
}

// A key the schema marks read-only must not be settable through a PATCH.
//
// Config.Set itself must keep accepting them — it is the low-level setter that
// env-var and config-file loading both go through, and a read-only key still
// has to be loadable from the file that declares it. The flag is a statement
// about REMOTE editing, so the place it has to hold is the patch path.
//
// It did not. `slmcode config set` refused these keys and the Studio's form
// never offered them, but a PUT /api/config carrying one in the untyped half of
// the patch set it anyway — three surfaces disagreeing about the same key.
func TestAPatchCannotSetAReadOnlyKey(t *testing.T) {
	for _, f := range Schema() {
		if _, patchable := PatchableField(f.Key); patchable {
			continue
		}
		sample, ok := sampleFor(f)
		if !ok {
			continue
		}
		t.Run(f.Key, func(t *testing.T) {
			c := Default(t.TempDir())
			before, _ := c.Get(f.Key)

			body, err := json.Marshal(map[string]any{f.Key: sample})
			if err != nil {
				t.Fatal(err)
			}
			var p Patch
			if err := json.Unmarshal(body, &p); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			c.ApplyPatch(p)

			if after, _ := c.Get(f.Key); render(before) != render(after) {
				t.Errorf("a patch set the read-only key %q (%v → %v)", f.Key, before, after)
			}
		})
	}
}

// The most consequential of them, stated on its own so the reason survives.
//
// MCP servers are external processes whose tools agents can call, so a request
// that could register one is tool-execution surface. The Studio panel that
// lists them says "configured in config.yaml" — file-only is exactly what the
// read-only flag was declaring.
func TestAPatchCannotRegisterAnMCPServer(t *testing.T) {
	c := Default(t.TempDir())
	var p Patch
	body := `{"mcp_servers":[{"name":"attacker","command":"/bin/sh","args":["-c","id"]}]}`
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	c.ApplyPatch(p)
	if len(c.MCPServers) != 0 {
		t.Errorf("a patch registered %d MCP server(s): %+v", len(c.MCPServers), c.MCPServers)
	}
}

// A patchable key must still go through, or the check would have turned the
// settings form read-only.
func TestAPatchStillSetsTheKeysItShould(t *testing.T) {
	c := Default(t.TempDir())
	var p Patch
	if err := json.Unmarshal([]byte(`{"max_parallel":5}`), &p); err != nil {
		t.Fatal(err)
	}
	c.ApplyPatch(p)
	if c.MaxParallel != 5 {
		t.Errorf("max_parallel = %d, want the patch applied", c.MaxParallel)
	}
}

// Every schema key must resolve through Field(), including via any alias —
// `config set` looks the key up there before it looks anywhere else.
func TestEverySchemaKeyResolves(t *testing.T) {
	for _, f := range Schema() {
		if _, ok := Field(f.Key); !ok {
			t.Errorf("Schema lists %q but Field() cannot resolve it", f.Key)
		}
		if got := CanonicalKey(f.Key); got != f.Key {
			t.Errorf("CanonicalKey(%q) = %q — the schema lists a non-canonical key", f.Key, got)
		}
	}
}

func render(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}
