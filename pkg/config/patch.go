package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// Patch carries a partial config update over JSON (Studio's settings page, the
// TUI, embedders). Its typed fields predate the reflective setter in fields.go
// and stay for compatibility.
//
// Any key that has no typed field but IS a real config key is captured into
// `extra` by UnmarshalJSON and applied by ApplyPatch through Config.Set. That
// closes the gap that made a new config field invisible to Studio until
// somebody hand-added a matching pointer field here — which is precisely how
// forty patchable keys ended up missing from the schema in the first place.

// patchJSONKeys is the set of JSON names Patch handles with a typed field.
var (
	patchKeysOnce sync.Once
	patchJSONKeys map[string]bool
)

func typedPatchKeys() map[string]bool {
	patchKeysOnce.Do(func() {
		patchJSONKeys = map[string]bool{}
		t := reflect.TypeOf(Patch{})
		for i := 0; i < t.NumField(); i++ {
			tag := t.Field(i).Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			name := strings.Split(tag, ",")[0]
			if name != "" {
				patchJSONKeys[name] = true
			}
		}
	})
	return patchJSONKeys
}

// UnmarshalJSON decodes the typed fields and keeps every other recognized
// config key aside for ApplyPatch.
func (p *Patch) UnmarshalJSON(data []byte) error {
	type alias Patch // avoid recursing into this method
	var typed alias
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	*p = Patch(typed)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	known := typedPatchKeys()
	for name, msg := range raw {
		key := CanonicalKey(name)
		if known[name] || known[key] || !HasKey(key) {
			continue
		}
		var val any
		if json.Unmarshal(msg, &val) != nil {
			continue
		}
		if p.extra == nil {
			p.extra = map[string]any{}
		}
		p.extra[key] = val
	}
	return nil
}

// Extra returns the keys this patch carries that have no typed field.
func (p Patch) Extra() map[string]any { return p.extra }

// WithValues returns a copy of p carrying additional raw key/value pairs.
// Callers that already have a key→value map (a YAML layer, a form post) can
// use this instead of hand-building typed pointers.
func (p Patch) WithValues(values map[string]any) Patch {
	if len(values) == 0 {
		return p
	}
	merged := make(map[string]any, len(p.extra)+len(values))
	for k, v := range p.extra {
		merged[k] = v
	}
	for k, v := range values {
		key := CanonicalKey(k)
		if HasKey(key) {
			merged[key] = v
		}
	}
	p.extra = merged
	return p
}

// applyExtra writes the untyped half of a patch. Errors are dropped on purpose:
// ApplyPatch has never had an error return, and one rejected key must not
// discard the rest of a settings save. Use Config.Set directly when the caller
// needs to report a bad value (that is what `slmcode config set` does).
func (c *Config) applyExtra(p Patch) {
	for _, k := range sortedKeys(p.extra) {
		if err := c.Set(k, p.extra[k]); err != nil {
			continue
		}
		// A patch is a person editing settings in Studio or the TUI, so the
		// key stops being a default. Consumers that improve on defaults —
		// pkg/calibrate — read this through Config.Explicit.
		c.Provenance().Mark(k, LayerProject, "")
	}
}
