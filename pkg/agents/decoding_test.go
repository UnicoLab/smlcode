package agents

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/llm"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// outputMarker is how every prompt introduces its single output contract.
const outputMarker = "OUTPUT — "

// contractOf returns the JSON object a prompt ends with. The contract checks
// themselves live in contract_test.go, which validates every role's example
// against both its JSON Schema and its generated GBNF grammar.
func contractOf(t *testing.T, prompt string) string {
	t.Helper()
	i := strings.LastIndex(prompt, outputMarker)
	if i < 0 {
		return ""
	}
	tail := prompt[i:]
	start := strings.Index(tail, "{")
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for j := start; j < len(tail); j++ {
		c := tail[j]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return tail[start : j+1]
			}
		}
	}
	return ""
}

func TestEveryPromptStatesItsContractExactlyOnce(t *testing.T) {
	for _, spec := range Specs() {
		p := spec.SystemPrompt
		if strings.TrimSpace(p) == "" {
			t.Errorf("%s: empty prompt", spec.ID)
			continue
		}
		if n := strings.Count(p, outputMarker); n != 1 {
			t.Errorf("%s: %d output contracts, want exactly 1", spec.ID, n)
		}
		// The contract must be at the end — nothing but the contract itself and
		// a short clarifying line may follow it. Small models weight the tail.
		i := strings.LastIndex(p, outputMarker)
		if tail := p[i:]; len(tail) > 900 {
			t.Errorf("%s: %d chars after the output marker — contract is not at the end", spec.ID, len(tail))
		}
	}
}

func TestPromptsStayShortEnoughForA32KWindow(t *testing.T) {
	// These prompts compete with the code for the model's context. The old
	// worker prompt was ~2.4KB of mostly prohibitions.
	limits := map[string]int{
		plan.RoleWorker: 2600, "deep": 2600, plan.RoleCorrector: 2600,
		plan.RolePlaceholder: 2800, RoleEditor: 2600,
		// The composer carries the whole language-specialist roster, and it is
		// the one prompt whose cost is paid ONCE PER RUN rather than once per
		// turn. The budget above exists because prompts compete with the CODE
		// for the window; at compose time there is no code in the pack yet.
		"composer": 2100,
	}
	for _, spec := range Specs() {
		limit, ok := limits[spec.ID]
		if !ok {
			limit = 1800
		}
		if n := len(spec.SystemPrompt); n > limit {
			t.Errorf("%s prompt is %d bytes (limit %d)", spec.ID, n, limit)
		}
	}
}

func TestEditContractCarriesBothDemonstrations(t *testing.T) {
	if !strings.Contains(EditContract, "WORKED EXAMPLE") {
		t.Error("edit contract has no worked example")
	}
	if !strings.Contains(EditContract, "REPAIRING A FAILED EDIT") {
		t.Error("edit contract has no failed-edit repair example")
	}
	// The exact-match and line-prefix rules are the two failure modes the
	// workspace guard actually reports.
	for _, want := range []string{"byte-for-byte", "line-number prefix", "old_str not found"} {
		if !strings.Contains(EditContract, want) {
			t.Errorf("edit contract missing %q", want)
		}
	}
	// Every tool-using coding role must carry it.
	for _, p := range []string{PromptWorker, PromptDeepWorker, PromptCorrector, PromptPlaceholder, PromptEditor} {
		if !strings.Contains(p, "WORKED EXAMPLE") {
			t.Error("a coding prompt is missing the edit demonstrations")
		}
		if !strings.Contains(p, OneToolPerTurn) {
			t.Error("a coding prompt is missing the one-call-per-turn rule")
		}
	}
}

func TestAntiWanderCoreIsThreeRules(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(AntiWanderCore), "\n")
	if len(lines) != 4 {
		t.Fatalf("anti-wander core is %d lines, want a header plus 3 rules:\n%s", len(lines), AntiWanderCore)
	}
	// AGENTS.md refers to these markers.
	for _, want := range []string{"ANTI-WANDER", "HARD SCOPE"} {
		if !strings.Contains(AntiWanderCore, want) {
			t.Errorf("anti-wander core missing the %q marker AGENTS.md refers to", want)
		}
	}
}

func TestNormalizeDecoding(t *testing.T) {
	coding := []string{"ws_read", "ws_edit"}
	cases := []struct {
		name        string
		in          RoleSpec
		jsonOnly    bool
		serialTools bool
		schemaRole  string
		wantStops   bool
	}{
		{"planner", RoleSpec{ID: "planner"}, true, false, schema.RolePlan, true},
		{"splitter", RoleSpec{ID: "splitter"}, true, false, schema.RoleTasks, true},
		{"reviewer", RoleSpec{ID: "reviewer"}, true, false, schema.RoleReview, true},
		{"reviewer-strict", RoleSpec{ID: "reviewer-strict"}, true, false, schema.RoleReview, true},
		{"escalate", RoleSpec{ID: "escalate"}, true, false, schema.RoleEscalate, true},
		{"composer", RoleSpec{ID: "composer", SchemaRole: schema.RoleComposition}, true, false, schema.RoleComposition, true},
		{"coordinator", RoleSpec{ID: "coordinator"}, true, false, schema.RoleCoordinator, true},
		{"worker has tools", RoleSpec{ID: "worker", Tools: coding}, false, true, schema.RoleWorker, false},
		{"tester has tools", RoleSpec{ID: "tester", Tools: coding}, false, true, schema.RoleTester, false},
		{"block-defined go-worker", RoleSpec{ID: "go-worker", Tools: coding}, false, true, schema.RoleWorker, false},
		{"block-defined go-reviewer", RoleSpec{ID: "go-reviewer"}, true, false, schema.RoleReview, true},
		{"context is markdown", RoleSpec{ID: "context"}, false, false, "", false},
		{"memory is markdown", RoleSpec{ID: "memory"}, false, false, "", false},
		{"describer is prose", RoleSpec{ID: "describer"}, false, false, "", false},
		{"unknown tool-less role", RoleSpec{ID: "my-custom-thing"}, false, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.in
			NormalizeDecoding(&s)
			if s.JSONOnly != tc.jsonOnly {
				t.Errorf("JSONOnly = %v, want %v", s.JSONOnly, tc.jsonOnly)
			}
			if s.SerialTools != tc.serialTools {
				t.Errorf("SerialTools = %v, want %v", s.SerialTools, tc.serialTools)
			}
			if s.SchemaRole != tc.schemaRole {
				t.Errorf("SchemaRole = %q, want %q", s.SchemaRole, tc.schemaRole)
			}
			if got := len(s.StopSequences) > 0; got != tc.wantStops {
				t.Errorf("StopSequences present = %v, want %v", got, tc.wantStops)
			}
			if tc.wantStops && s.StopSequences[0] != "\n## " {
				t.Errorf("first stop = %q, want the markdown-section stop", s.StopSequences[0])
			}
		})
	}
	// Nil and empty ids must not panic.
	NormalizeDecoding(nil)
	empty := RoleSpec{}
	NormalizeDecoding(&empty)
}

func TestBuiltinRosterDecodingContracts(t *testing.T) {
	byID := map[string]RoleSpec{}
	for _, s := range Specs() {
		byID[s.ID] = s
	}
	jsonOnly := []string{
		"planner", "splitter", "reviewer", "reviewer-strict", "architect",
		"escalate", "composer", "coordinator", "orchestrator",
	}
	for _, id := range jsonOnly {
		s, ok := byID[id]
		if !ok {
			t.Fatalf("%s missing from the roster", id)
		}
		if !s.JSONOnly {
			t.Errorf("%s should be JSON-only", id)
		}
		if len(s.Tools) != 0 {
			t.Errorf("%s should have no tools", id)
		}
		if len(s.StopSequences) == 0 {
			t.Errorf("%s has no stop sequences — the prose tail will still be generated", id)
		}
	}
	toolRoles := []string{"worker", "deep", "corrector", "tester", "placeholder", "explorer", "docs", "editor"}
	for _, id := range toolRoles {
		s, ok := byID[id]
		if !ok {
			t.Fatalf("%s missing from the roster", id)
		}
		if !s.SerialTools {
			t.Errorf("%s should cap at one tool call per turn", id)
		}
		if len(s.Tools) == 0 {
			t.Errorf("%s should have tools", id)
		}
		if d := s.Directives(); d.ToolChoice != "auto" {
			t.Errorf("%s tool_choice = %q, want auto", id, d.ToolChoice)
		}
	}
}

func TestReviewerStrictIsRegistered(t *testing.T) {
	// pkg/loop's speculative review race asks SubAgentExecutor for this id.
	// Before it was registered the executor answered "subagent not found" and
	// the documented second reviewer never ran.
	if !IsKnownRole(RoleReviewerStrict) {
		t.Fatal("reviewer-strict is not a known role")
	}
	spec := FindSpec(RoleReviewerStrict)
	if spec == nil {
		t.Fatal("no spec for reviewer-strict")
	}
	if spec.SchemaRole != schema.RoleReview {
		t.Errorf("schema role = %q, want the reviewer contract", spec.SchemaRole)
	}
	primary := FindSpec("reviewer")
	if spec.Temperature >= primary.Temperature {
		t.Errorf("strict reviewer temperature %v should be below the primary's %v",
			spec.Temperature, primary.Temperature)
	}
	// It must appear in the executor registry the loop actually queries.
	f := NewFactory(nil, nil, "m", "omlx")
	reg, err := f.BuildRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.GetDefinition(RoleReviewerStrict); !ok {
		t.Fatal("reviewer-strict missing from the sub-agent registry SubAgentExecutor queries")
	}
}

func TestIsKnownRole(t *testing.T) {
	for _, id := range []string{
		"worker", "reviewer", "reviewer-strict", "tester", "planner",
		"splitter", "composer", "describer", "editor", "escalate",
	} {
		if !IsKnownRole(id) {
			t.Errorf("IsKnownRole(%q) = false", id)
		}
	}
	if IsKnownRole("  REVIEWER-STRICT  ") != true {
		t.Error("IsKnownRole should normalise case and space")
	}
	for _, id := range []string{"", "reviewer-stict", "go-worker", "nope"} {
		if IsKnownRole(id) {
			t.Errorf("IsKnownRole(%q) = true", id)
		}
	}
	// Block-defined roles are not built-ins but are creatable by a factory.
	f := NewFactory(nil, nil, "m", "omlx")
	f.ExtraCustoms = []CustomSpec{{ID: "go-worker", Title: "Go worker", SystemPrompt: "x", MaxIter: 4, MaxTokens: 100, Temperature: 0.1, Tools: BoolPtr(true)}}
	if !f.HasRole("go-worker") {
		t.Error("HasRole should see block-defined agents")
	}
	if f.HasRole("still-not-a-role") {
		t.Error("HasRole accepted an unknown role")
	}
}

func TestArchitectEditorPair(t *testing.T) {
	describer, editor := ArchitectEditorPair()
	d := FindSpec(describer)
	e := FindSpec(editor)
	if d == nil || e == nil {
		t.Fatal("pair roles are not registered")
	}
	// The describer must be unconstrained: no tools, no schema, no JSON mode.
	if len(d.Tools) != 0 || d.JSONOnly || d.SchemaRole != "" {
		t.Errorf("describer is constrained: tools=%d jsonOnly=%v schema=%q", len(d.Tools), d.JSONOnly, d.SchemaRole)
	}
	// The editor must be constrained and tool-capable.
	if len(e.Tools) == 0 || !e.SerialTools || e.SchemaRole != schema.RoleWorker {
		t.Errorf("editor is not the constrained half: %+v", e)
	}
	if e.Temperature >= d.Temperature {
		t.Errorf("editor temperature %v should be below the describer's %v", e.Temperature, d.Temperature)
	}
	// Each half's model is independently selectable through the usual override.
	f := NewFactory(nil, nil, "global-32b", "omlx")
	f.ExtraCustoms = []CustomSpec{}
	if got := f.EffectiveModel(*d); got != "global-32b" {
		t.Errorf("describer model = %q", got)
	}
	small := *e
	small.Model = "qwen2.5-coder:7b"
	if got := f.EffectiveModel(small); got != "qwen2.5-coder:7b" {
		t.Errorf("editor override not honored: %q", got)
	}
	in := EditorInput("add Sum", "put Sum in calc.go returning a+b")
	if !strings.Contains(in, "add Sum") || !strings.Contains(in, "a+b") {
		t.Errorf("editor input = %q", in)
	}
}

func TestFactoryBindsRoleScopedProviders(t *testing.T) {
	backends.ResetCapabilityCache()
	cfg := config.Default(t.TempDir())
	cfg.Provider = "omlx"
	cfg.Model = "fake-model"
	cfg.Endpoint = "http://127.0.0.1:9/v1"
	m := llm.NewProviderManager()
	if err := backends.RegisterLLM(m, cfg); err != nil {
		t.Fatal(err)
	}
	f := NewFactory(m, tools.NewToolRegistry(), cfg.Model, cfg.Provider)

	// A JSON-only role gets its own registration carrying the decoding contract.
	if _, err := f.Create("reviewer"); err != nil {
		t.Fatal(err)
	}
	key := "omlx" + backends.RoleKeySeparator + "reviewer"
	p, err := m.GetProvider(key)
	if err != nil {
		t.Fatalf("reviewer provider not bound: %v", err)
	}
	c := p.GetConfig()
	if c["slmcode_json_only"] != true {
		t.Errorf("bound provider is not JSON-only: %v", c)
	}
	if c["slmcode_schema_role"] != schema.RoleReview {
		t.Errorf("bound schema role = %v", c["slmcode_schema_role"])
	}

	// A tool role gets a serial-tools registration.
	if _, err := f.Create("worker"); err != nil {
		t.Fatal(err)
	}
	wp, err := m.GetProvider("omlx" + backends.RoleKeySeparator + "worker")
	if err != nil {
		t.Fatalf("worker provider not bound: %v", err)
	}
	if wp.GetConfig()["slmcode_serial_tools"] != true {
		t.Error("worker provider does not cap tool calls")
	}

	// Creating the same role twice must not fail on a duplicate registration.
	if _, err := f.Create("reviewer"); err != nil {
		t.Fatalf("second Create failed: %v", err)
	}
}

func TestFactoryWithoutManagerDegradesToPlainKey(t *testing.T) {
	// Studio, the CLI `agent list` path, and several tests build a factory with
	// a nil ProviderManager. Role binding must degrade to the plain provider key
	// instead of panicking or inventing an unregistered one.
	f := NewFactory(nil, nil, "m", "omlx")
	for _, id := range []string{"reviewer", "worker", "describer", "editor"} {
		spec := FindSpec(id)
		if spec == nil {
			t.Fatalf("no spec for %q", id)
		}
		def := f.definition(*spec)
		got := def.GetConfig().Provider
		if got != "omlx" {
			t.Errorf("%s provider = %q, want the plain key when no manager is wired", id, got)
		}
	}
	// And the registry still builds, so `agent list` and BuildRegistry work.
	if _, err := f.BuildRegistry(); err != nil {
		t.Fatalf("BuildRegistry with a nil manager: %v", err)
	}
}
