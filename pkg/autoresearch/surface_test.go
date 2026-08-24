package autoresearch

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestReflectFindsAgentAndConfigKnobs(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)

	want := map[string]string{
		"agent:worker.temperature":   "0.2",
		"agent:worker.max_tokens":    "3072",
		"agent:worker.max_iter":      "12",
		"agent:worker.system_prompt": "Do the task.\nKeep diffs small.",
		"config:think_passes":        "1",
	}
	for id, value := range want {
		k, ok := s.Knob(id)
		if !ok {
			t.Fatalf("surface is missing %s; has %v", id, knobIDs(s))
		}
		if k.Value != value {
			t.Errorf("%s = %q, want %q", id, k.Value, value)
		}
	}

	// Every whitelisted config key must be present even when config.yaml does
	// not mention it — otherwise a fresh project has almost no surface.
	for _, ck := range ConfigWhitelist() {
		if _, ok := s.Knob("config:" + ck.Key); !ok {
			t.Errorf("whitelisted key %s is not on the surface", ck.Key)
		}
	}

	// A non-scalar field is not a knob: flattening `skills:` into a string
	// would corrupt the file.
	if _, ok := s.Knob("agent:worker.skills"); ok {
		t.Error("skills list was exposed as a knob")
	}
}

func TestReflectIsDeterministicallyOrdered(t *testing.T) {
	root := newTestProject(t)
	first := knobIDs(mustReflect(t, root))
	for i := 0; i < 5; i++ {
		if got := knobIDs(mustReflect(t, root)); !equalStrings(got, first) {
			t.Fatalf("surface order changed between reflections:\n%v\n%v", first, got)
		}
	}
	if !sort.StringsAreSorted(first) {
		t.Errorf("surface is not sorted by id: %v", first)
	}
}

func TestReflectHandlesMissingAgentsDirAndCorruptFile(t *testing.T) {
	root := t.TempDir()
	// No .slmcode at all: config knobs still reflect off the built-in defaults.
	s, err := Reflect(Options{Root: root})
	if err != nil {
		t.Fatalf("Reflect on an empty root: %v", err)
	}
	if s.Len() != len(ConfigWhitelist()) {
		t.Fatalf("empty root gave %d knobs, want %d", s.Len(), len(ConfigWhitelist()))
	}

	// One unparseable agent file must cost that file, not the whole surface.
	agents := filepath.Join(root, ".slmcode", "agents")
	mkdirAll(t, agents)
	writeFile(t, filepath.Join(agents, "broken.yaml"), "id: [unterminated\n")
	writeFile(t, filepath.Join(agents, "worker.yaml"), testWorkerYAML)
	s = mustReflect(t, root)
	if _, ok := s.Knob("agent:worker.temperature"); !ok {
		t.Error("a broken sibling file suppressed a good agent's knobs")
	}
	if len(s.Warnings()) == 0 {
		t.Error("an unparseable agent file was skipped silently")
	}
}

// TestWhitelistRejectsSecuritySensitiveKnobs is the load-bearing whitelist
// test: the mutable surface is an allow-list, and none of the keys that decide
// who the harness talks to, what it may run or where it may write is on it.
func TestWhitelistRejectsSecuritySensitiveKnobs(t *testing.T) {
	for key, why := range SecuritySensitiveKeys() {
		if IsWhitelisted(key) {
			t.Errorf("%s is mutable but must never be (%s)", key, why)
		}
	}
	// A key that does not exist is not permission either, and the case/space
	// folding that makes "Temperature " match must not also make "API_KEY"
	// match something.
	for _, key := range []string{"", "nonexistent_key", "api-key", " API_KEY ", "Permission", "HOOKS_ENABLED"} {
		if IsWhitelisted(key) {
			t.Errorf("IsWhitelisted(%q) = true", key)
		}
	}
	// And the positive case, so the test cannot pass by whitelisting nothing.
	if !IsWhitelisted("think_passes") || !IsWhitelisted("temperature") {
		t.Fatal("the whitelist rejects its own entries")
	}
	if len(ConfigWhitelist()) == 0 {
		t.Fatal("the whitelist is empty — this test would pass vacuously")
	}
}

func TestSurfaceExcludesSecuritySensitiveKeys(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)
	for _, k := range s.Knobs() {
		if k.Source != SourceConfig {
			continue
		}
		if why, bad := SecuritySensitiveKeys()[k.Field]; bad {
			t.Fatalf("surface exposes %s (%s)", k.Field, why)
		}
	}
	// The project's config.yaml carries an api_key; reflection must not have
	// turned it into a knob just because it is present in the file.
	if _, ok := s.Knob("config:api_key"); ok {
		t.Fatal("api_key reached the surface")
	}
}

func TestApplyRefusesKnobsOffTheSurface(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)

	cases := []Change{
		{KnobID: "config:api_key", After: "leaked"},
		{KnobID: "config:permission", After: "auto"},
		{KnobID: "config:shell_allow", After: "rm -rf /"},
		{KnobID: "agent:worker.model", After: "gpt-4o"},
		{KnobID: "agent:worker.provider", After: "openai"},
		{KnobID: "", After: "x"},
	}
	for _, c := range cases {
		if err := s.Apply(c); !errors.Is(err, ErrNotMutable) {
			t.Errorf("Apply(%s) error = %v, want ErrNotMutable", c.KnobID, err)
		}
	}
	if body := readFile(t, filepath.Join(root, ".slmcode", "config.yaml")); !strings.Contains(body, "super-secret") {
		t.Fatal("config.yaml was rewritten by a refused change")
	}
}

func TestApplyRefusesValuesOutsideTheDomain(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)
	before := readFile(t, filepath.Join(root, ".slmcode", "agents", "worker.yaml"))

	for _, after := range []string{"5", "-1", "abc", ""} {
		err := s.Apply(Change{KnobID: "agent:worker.temperature", After: after})
		if err == nil {
			t.Errorf("temperature = %q was accepted", after)
		}
	}
	if got := readFile(t, filepath.Join(root, ".slmcode", "agents", "worker.yaml")); got != before {
		t.Fatal("a refused change still wrote to the file")
	}
	// Out-of-domain must be an error, never a silent clamp: a clamped write
	// would make the journal disagree with the file.
	if k, _ := s.Knob("agent:worker.temperature"); k.Value != "0.2" {
		t.Fatalf("in-memory value drifted to %q after refused writes", k.Value)
	}
}

func TestApplyEditsSurgicallyAndPreservesTheRestOfTheFile(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)
	path := filepath.Join(root, ".slmcode", "agents", "worker.yaml")

	if err := s.Apply(Change{KnobID: "agent:worker.temperature", After: "0.45"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body := readFile(t, path)
	for _, want := range []string{
		"temperature: 0.45",
		"# this comment must survive an edit",
		"- specialist-worker",
		"max_iter: 12",
		"Keep diffs small.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("edited file lost %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `"0.45"`) {
		t.Errorf("a numeric knob was written as a string:\n%s", body)
	}

	// Re-reflecting must see what was written.
	if k, _ := mustReflect(t, root).Knob("agent:worker.temperature"); k.Value != "0.45" {
		t.Fatalf("re-reflected temperature = %q, want 0.45", k.Value)
	}
}

func TestApplyCreatesAConfigKeyThatWasOnlyADefault(t *testing.T) {
	root := newTestProject(t)
	s := mustReflect(t, root)
	k, _ := s.Knob("config:memory_tokens")
	if k.InFile {
		t.Fatal("memory_tokens was already in the file; this test proves nothing")
	}
	if err := s.Apply(Change{KnobID: "config:memory_tokens", After: "450"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body := readFile(t, filepath.Join(root, ".slmcode", "config.yaml"))
	if !strings.Contains(body, "memory_tokens: 450") {
		t.Fatalf("key was not created:\n%s", body)
	}
	if !strings.Contains(body, "api_key: super-secret") {
		t.Fatalf("creating a key rewrote the rest of the file:\n%s", body)
	}
}

func TestDomainCandidatesAreFiniteAndStable(t *testing.T) {
	d := Domain{Kind: KnobFloat, Min: 0, Max: 1, Step: 0.05}
	got := d.Candidates()
	if len(got) != 21 {
		t.Fatalf("float domain gave %d candidates, want 21: %v", len(got), got)
	}
	// Repeated addition of 0.05 is 0.15000000000000002 unless it is rounded;
	// a value string that drifts is a value the history can never match.
	for _, want := range []string{"0", "0.15", "0.5", "1"} {
		if !containsString(got, want) {
			t.Errorf("float domain is missing %q: %v", want, got)
		}
	}
	if again := d.Candidates(); !equalStrings(again, got) {
		t.Error("candidate order is not stable")
	}
	// A text domain has no enumeration — that is what keeps system_prompt out
	// of the deterministic proposer's reach.
	if c := (Domain{Kind: KnobText, MaxLen: 100}).Candidates(); c != nil {
		t.Errorf("text domain enumerated %v", c)
	}
	// And nothing enumerates without bound.
	wide := Domain{Kind: KnobInt, Min: 0, Max: 1e9, Step: 1}
	if n := len(wide.Candidates()); n > MaxCandidates {
		t.Fatalf("an int domain enumerated %d candidates", n)
	}
}

func TestSurfaceFilesAreSortedAndUnique(t *testing.T) {
	root := newTestProject(t)
	files := mustReflect(t, root).Files()
	if len(files) != 2 {
		t.Fatalf("Files() = %v, want the agent file and config.yaml", files)
	}
	if !sort.StringsAreSorted(files) {
		t.Errorf("Files() is not sorted: %v", files)
	}
}

func knobIDs(s *Surface) []string {
	out := make([]string, 0, s.Len())
	for _, k := range s.Knobs() {
		out = append(out, k.ID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}
