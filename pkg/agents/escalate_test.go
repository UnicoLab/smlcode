package agents

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

func TestEscalatedRoleIDRoundTrips(t *testing.T) {
	for _, role := range []string{"worker", "corrector", "go-worker", "reviewer-strict"} {
		for rung := 1; rung <= 3; rung++ {
			id := EscalatedRoleID(role, rung)
			base, got := BaseRoleID(id)
			if base != role || got != rung {
				t.Errorf("EscalatedRoleID(%q,%d) = %q → (%q,%d)", role, rung, id, base, got)
			}
			if !IsEscalated(id) {
				t.Errorf("%q not recognized as escalated", id)
			}
		}
	}
}

func TestEscalatedRoleIDRungZeroIsTheBase(t *testing.T) {
	if got := EscalatedRoleID("worker", 0); got != "worker" {
		t.Errorf("rung 0 = %q", got)
	}
	if got := EscalatedRoleID("worker", -1); got != "worker" {
		t.Errorf("negative rung = %q", got)
	}
	if got := EscalatedRoleID("", 2); got != "" {
		t.Errorf("empty role = %q", got)
	}
}

func TestBaseRoleIDLeavesOrdinaryIDsAlone(t *testing.T) {
	// Hyphenated names are the reason the separator is '@' and not '-'.
	for _, id := range []string{
		"worker", "reviewer-strict", "go-worker", "python-tester", "",
		"worker@escX", "worker@esc", "worker@esc0", "worker@esc-1",
	} {
		base, rung := BaseRoleID(id)
		if base != id || rung != 0 {
			t.Errorf("BaseRoleID(%q) = (%q,%d), want the id unchanged", id, base, rung)
		}
		if IsEscalated(id) {
			t.Errorf("%q wrongly read as escalated", id)
		}
	}
}

func TestIsEscalableRole(t *testing.T) {
	for _, id := range []string{
		plan.RoleWorker, plan.RoleCorrector, plan.RoleReviewer, "deep",
		RoleEditor, RoleReviewerStrict, "go-worker", "python-worker", "rust-corrector",
	} {
		if !IsEscalableRole(id) {
			t.Errorf("%q should be escalable", id)
		}
	}
	// Escalating a splitter because a WORKER's task failed twice spends a
	// bigger model on the one part of the run that was not the problem.
	for _, id := range []string{
		"splitter", plan.RolePlanner, "memory", "context", "composer",
		"coordinator", "architect", "docs", plan.RoleExplorer,
	} {
		if IsEscalableRole(id) {
			t.Errorf("%q should not be escalable", id)
		}
	}
}

func TestSetEscalationTrimsAndCaps(t *testing.T) {
	f := NewFactory(nil, nil, "base-model", "omlx")
	f.SetEscalation([]string{" big-model ", "", "   ", "bigger-model", "huge", "way-too-far", "absurd"})
	lad := f.Escalation()
	if len(lad) > MaxEscalationRungs {
		t.Fatalf("ladder = %d rungs, want at most %d", len(lad), MaxEscalationRungs)
	}
	if lad[0] != "big-model" {
		t.Errorf("first rung = %q", lad[0])
	}
	for _, m := range lad {
		if strings.TrimSpace(m) == "" {
			t.Error("an empty rung survived")
		}
	}
	if f.EscalationRungs() != len(lad) {
		t.Errorf("EscalationRungs = %d, ladder = %d", f.EscalationRungs(), len(lad))
	}
}

func TestNoLadderMeansNoVariants(t *testing.T) {
	f := NewFactory(nil, nil, "base-model", "omlx")
	for _, spec := range f.AllSpecs() {
		if IsEscalated(spec.ID) {
			t.Fatalf("variant %q registered with no ladder configured", spec.ID)
		}
	}
}

func TestEscalatedSpecsArePinnedToTheirRungModel(t *testing.T) {
	f := NewFactory(nil, nil, "base-model", "omlx")
	f.SetEscalation([]string{"big-model", "bigger-model"})

	specs := f.AllSpecs()
	byID := map[string]RoleSpec{}
	for _, s := range specs {
		byID[s.ID] = s
	}
	w1, ok := byID[EscalatedRoleID(plan.RoleWorker, 1)]
	if !ok {
		t.Fatal("no rung-1 worker registered")
	}
	if w1.Model != "big-model" {
		t.Errorf("rung 1 model = %q, want big-model", w1.Model)
	}
	if got := f.EffectiveModel(w1); got != "big-model" {
		t.Errorf("EffectiveModel(rung 1) = %q", got)
	}
	w2 := byID[EscalatedRoleID(plan.RoleWorker, 2)]
	if w2.Model != "bigger-model" {
		t.Errorf("rung 2 model = %q, want bigger-model", w2.Model)
	}
	// The base agent is untouched.
	if base := byID[plan.RoleWorker]; base.Model != "" {
		t.Errorf("the base worker was pinned to %q", base.Model)
	}
}

func TestEscalatedSpecsInheritTheDecodingContract(t *testing.T) {
	// A rung that derived its own contract would derive it from a variant id
	// that names no schema role — and would then decode unconstrained, which
	// is the one thing this harness never lets a structured role do.
	f := NewFactory(nil, nil, "base-model", "omlx")
	f.SetEscalation([]string{"big-model"})
	byID := map[string]RoleSpec{}
	for _, s := range f.AllSpecs() {
		byID[s.ID] = s
	}
	for _, role := range []string{plan.RoleWorker, plan.RoleReviewer, plan.RoleCorrector} {
		base, v := byID[role], byID[EscalatedRoleID(role, 1)]
		if v.ID == "" {
			t.Fatalf("no rung registered for %q", role)
		}
		if v.SchemaRole != base.SchemaRole {
			t.Errorf("%s rung schema = %q, base = %q", role, v.SchemaRole, base.SchemaRole)
		}
		if v.JSONOnly != base.JSONOnly || v.SerialTools != base.SerialTools {
			t.Errorf("%s rung decoding drifted: json=%v/%v serial=%v/%v",
				role, v.JSONOnly, base.JSONOnly, v.SerialTools, base.SerialTools)
		}
		if len(v.Tools) != len(base.Tools) {
			t.Errorf("%s rung tools = %d, base = %d", role, len(v.Tools), len(base.Tools))
		}
		if v.SystemPrompt != base.SystemPrompt {
			t.Errorf("%s rung prompt drifted from its base", role)
		}
	}
}

func TestEscalatedRolesStayCodingAndLightClassified(t *testing.T) {
	// The invisible failure this guards: an escalated worker that stops
	// matching isCodingRole keeps running, just with the wrong token budget.
	if !isCodingRole(EscalatedRoleID(plan.RoleWorker, 1)) {
		t.Error("an escalated worker lost its coding-role classification")
	}
	if !isCodingRole(EscalatedRoleID(plan.RoleCorrector, 2)) {
		t.Error("an escalated corrector lost its coding-role classification")
	}
	if !isLightAgent(EscalatedRoleID(plan.RoleReviewer, 1)) {
		t.Error("an escalated reviewer lost its light-agent classification")
	}
	if genericRole(EscalatedRoleID("go-worker", 1)) != "worker" {
		t.Errorf("genericRole = %q for an escalated language specialist",
			genericRole(EscalatedRoleID("go-worker", 1)))
	}
}

func TestFactoryHasRoleFindsRungs(t *testing.T) {
	// The loop consults HasRole before every escalated dispatch, because an
	// unregistered id is a hard task failure — landing on exactly the tasks
	// that were already struggling.
	f := NewFactory(nil, nil, "base-model", "omlx")
	f.SetEscalation([]string{"big-model"})
	if !f.HasRole(EscalatedRoleID(plan.RoleWorker, 1)) {
		t.Error("HasRole missed a registered rung")
	}
	if f.HasRole(EscalatedRoleID(plan.RoleWorker, 2)) {
		t.Error("HasRole claimed a rung beyond the ladder")
	}
	if f.HasRole(EscalatedRoleID("splitter", 1)) {
		t.Error("HasRole claimed a rung for a non-escalable role")
	}
}

func TestNoDoubleEscalation(t *testing.T) {
	// AllSpecs feeds its own output back in; a variant must never spawn one.
	f := NewFactory(nil, nil, "base-model", "omlx")
	f.SetEscalation([]string{"big-model"})
	for _, s := range f.AllSpecs() {
		if strings.Count(s.ID, EscalationSuffix) > 1 {
			t.Errorf("doubly-escalated id %q", s.ID)
		}
	}
}

// ── Role→model pinning ────────────────────────────────────────────────────

func TestRoleModelOutranksFastAndClassification(t *testing.T) {
	f := NewFactory(nil, nil, "main-model", "omlx")
	f.SetFastModel("fast-model")
	reviewer := RoleSpec{ID: plan.RoleReviewer}

	// Baseline: the reviewer is a light agent, so it lands on the fast model.
	if got := f.EffectiveModel(reviewer); got != "fast-model" {
		t.Fatalf("baseline reviewer model = %q, want fast-model", got)
	}
	// A pin outranks the classification…
	f.SetRoleModel(plan.RoleReviewer, "pinned-model")
	if got := f.EffectiveModel(reviewer); got != "pinned-model" {
		t.Errorf("pinned reviewer model = %q", got)
	}
	// …and the bandit's per-role fast preference.
	f.SetPreferFast(plan.RoleReviewer, true)
	if got := f.EffectiveModel(reviewer); got != "pinned-model" {
		t.Errorf("fast preference overrode an explicit pin: %q", got)
	}
}

func TestSpecModelOutranksRoleModel(t *testing.T) {
	// The operator editing that agent's own definition file is the most
	// specific statement of intent available.
	f := NewFactory(nil, nil, "main-model", "omlx")
	f.SetRoleModel(plan.RoleWorker, "pinned-model")
	spec := RoleSpec{ID: plan.RoleWorker, Model: "agent-file-model"}
	if got := f.EffectiveModel(spec); got != "agent-file-model" {
		t.Errorf("EffectiveModel = %q, want the agent file's own model", got)
	}
}

func TestRoleModelIsCaseInsensitiveAndIgnoresBlanks(t *testing.T) {
	f := NewFactory(nil, nil, "main-model", "omlx")
	f.SetRoleModel("  Reviewer  ", "  pinned-model  ")
	if got, ok := f.RoleModel("REVIEWER"); !ok || got != "pinned-model" {
		t.Errorf("RoleModel = %q (ok=%v)", got, ok)
	}
	f.SetRoleModel("", "x")
	f.SetRoleModel("worker", "")
	if _, ok := f.RoleModel("worker"); ok {
		t.Error("a blank model was pinned")
	}
}

func TestSetRoleModelsReplacesTheWholeMap(t *testing.T) {
	f := NewFactory(nil, nil, "main-model", "omlx")
	f.SetRoleModels(map[string]string{"worker": "a", "reviewer": "b"})
	f.SetRoleModels(map[string]string{"worker": "c"})
	if got, _ := f.RoleModel("worker"); got != "c" {
		t.Errorf("worker = %q, want c", got)
	}
	if _, ok := f.RoleModel("reviewer"); ok {
		t.Error("the previous map survived a replacement")
	}
}

func TestNilFactoryModelHelpersAreSafe(t *testing.T) {
	var f *Factory
	f.SetRoleModel("worker", "m")
	f.SetRoleModels(map[string]string{"worker": "m"})
	f.SetEscalation([]string{"m"})
	if _, ok := f.RoleModel("worker"); ok {
		t.Error("nil factory reported a pin")
	}
	if f.EscalationRungs() != 0 || f.Escalation() != nil {
		t.Error("nil factory reported a ladder")
	}
}
