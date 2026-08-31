package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/squads"
)

// teamServer builds a Studio server over a workspace that looks like a
// full-stack repo, which is what makes preselection have anything to say.
func teamServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.23\n")
	write("cmd/server/main.go", "package main\n\nfunc main() {}\n")
	write("web/package.json", `{"name":"web"}`)
	write("web/src/App.tsx", "export default function App() { return null }\n")

	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	return New(h, nil), root
}

func do(t *testing.T, s *Server, method, target string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = newAPIRequest(method, target, nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = newAPIRequest(method, target, bytes.NewReader(raw))
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON (%v): %s", err, rec.Body.String())
	}
	return out
}

// The complaint this endpoint answers: the Teams page was empty unless a run
// happened to have assembled squads. The library exists on a cold workspace.
func TestTeamLibraryIsPopulatedWithoutARun(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodGet, "/api/teams", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decode(t, rec)
	list, _ := out["teams"].([]interface{})
	if len(list) < 2 {
		t.Fatalf("the builtin library must ship teams, got %v", out["teams"])
	}
	if agents, _ := out["agents"].([]interface{}); len(agents) == 0 {
		t.Fatal("the staffing roster must ride along — a picker cannot invent agent ids")
	}
	first, _ := list[0].(map[string]interface{})
	for _, key := range []string{"id", "owns", "acceptance", "worker", "match", "source", "builtin"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("team view is missing %q: %v", key, first)
		}
	}
}

func TestTeamCRUDRoundTrip(t *testing.T) {
	s, root := teamServer(t)

	create := map[string]interface{}{
		"id": "payments", "name": "Payments", "charter": "billing and invoices",
		"owns": []string{"billing/**"}, "acceptance": "go test ./billing/...",
		"description": "the money half",
		"match":       map[string]interface{}{"keywords": []string{"billing", "invoice"}},
	}
	rec := do(t, s, http.MethodPost, "/api/teams", create)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	if got["id"] != "payments" || got["source"] != "project" {
		t.Fatalf("created team=%v", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".slmcode", "blocks", "teams", "payments.yaml")); err != nil {
		t.Fatalf("a created team must be a file on disk: %v", err)
	}

	rec = do(t, s, http.MethodPut, "/api/teams/payments", map[string]interface{}{
		"id": "payments", "name": "Payments & Billing",
		"owns": []string{"billing/**", "invoices/**"}, "worker": "go-worker",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	got = decode(t, rec)
	if got["name"] != "Payments & Billing" {
		t.Fatalf("update lost the name: %v", got)
	}
	owns, _ := got["owns"].([]interface{})
	if len(owns) != 2 {
		t.Fatalf("owns=%v", got["owns"])
	}

	rec = do(t, s, http.MethodGet, "/api/teams/payments", nil)
	if rec.Code != http.StatusOK || decode(t, rec)["name"] != "Payments & Billing" {
		t.Fatalf("read-back status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = do(t, s, http.MethodDelete, "/api/teams/payments", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = do(t, s, http.MethodGet, "/api/teams/payments", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted team still resolves: status=%d", rec.Code)
	}
}

// A team is "these people", and how many there are is its author's business —
// not the four seats the harness happens to dispatch.
func TestATeamCanCarryAnyNumberOfAgentsAndSkills(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams", map[string]interface{}{
		"id": "platform", "owns": []string{"platform/**"}, "acceptance": "make test",
		"worker": "go-worker",
		"agents": []string{"go-reviewer", "go-tester", "worker", "reviewer"},
		"skills": []string{"atomic-coding", "go-table-tests", "go-concurrency"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	roster, _ := got["agents"].([]interface{})
	if len(roster) != 4 {
		t.Fatalf("agents=%v — every one the author listed must survive", got["agents"])
	}
	skills, _ := got["skills"].([]interface{})
	if len(skills) != 3 {
		t.Fatalf("skills=%v", got["skills"])
	}
	// And it reaches the org chart, which is what the run and the triage roster
	// actually read.
	if rec := do(t, s, http.MethodPost, "/api/teams/activate", map[string]interface{}{
		"teams": []string{"platform", "frontend-react"},
	}); rec.Code != http.StatusOK {
		t.Fatalf("activate: %s", rec.Body.String())
	}
	rec = do(t, s, http.MethodGet, "/api/squads", nil)
	view := decode(t, rec)
	squadList, _ := view["squads"].([]interface{})
	for _, raw := range squadList {
		sq, _ := raw.(map[string]interface{})
		if sq["id"] != "platform" {
			continue
		}
		if members, _ := sq["agents"].([]interface{}); len(members) != 4 {
			t.Fatalf("the org chart lost the roster: %v", sq["agents"])
		}
		return
	}
	t.Fatalf("platform is not on the org chart: %v", view["squads"])
}

// A team that owns nothing can never be routed a task. Refusing it at the API
// is the difference between a visible error and a team that sits idle all run.
func TestCreatingAnUnroutableTeamIsRefused(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams", map[string]interface{}{"id": "ghost", "name": "Ghost"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "owns no paths") {
		t.Fatalf("the refusal must say why: %s", rec.Body.String())
	}
}

// Editing a builtin writes a project override; the builtin itself lives inside
// the binary and cannot be removed. Deleting the override restores it.
func TestEditingABuiltinTeamCreatesAnOverride(t *testing.T) {
	s, _ := teamServer(t)

	rec := do(t, s, http.MethodPut, "/api/teams/backend-go", map[string]interface{}{
		"id": "backend-go", "name": "Our Go Backend",
		"owns": []string{"srv/**"}, "acceptance": "make test",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := decode(t, rec); got["source"] != "project" || got["name"] != "Our Go Backend" {
		t.Fatalf("override=%v", got)
	}

	rec = do(t, s, http.MethodDelete, "/api/teams/backend-go", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete override status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, http.MethodGet, "/api/teams/backend-go", nil)
	got := decode(t, rec)
	if got["source"] != "builtin" {
		t.Fatalf("deleting the override must reveal the builtin again: %v", got)
	}

	// The builtin has no file to remove, and saying so is more useful than a
	// success that deletes nothing.
	rec = do(t, s, http.MethodDelete, "/api/teams/backend-go", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "cannot be deleted") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// The page must be able to show what a query WOULD run with, before running it.
func TestPreselectPreviewsTheTeamsAQueryWouldGet(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams/preselect", map[string]interface{}{
		"query": "add a Go API endpoint and the React page that calls it",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decode(t, rec)
	if out["enabled"] != true {
		t.Fatalf("a full-stack query in a full-stack repo must enable teams: %v", out)
	}
	sel, _ := out["selected"].([]interface{})
	if len(sel) < 2 {
		t.Fatalf("selected=%v", out["selected"])
	}
	ev, _ := out["evidence"].([]interface{})
	if len(ev) == 0 {
		t.Fatal("a preselection with no evidence cannot be argued with")
	}
	first, _ := ev[0].(map[string]interface{})
	if reasons, _ := first["reasons"].([]interface{}); len(reasons) == 0 {
		t.Fatalf("every scored team needs stated reasons: %v", first)
	}
}

// Activating composes a plan on disk, which is what makes the page usable
// outside a run: before this the only way to get a squad plan was to start one
// and hope the manager produced it.
func TestActivateWritesARunnablePlanOutsideARun(t *testing.T) {
	s, root := teamServer(t)

	rec := do(t, s, http.MethodPost, "/api/teams/activate", map[string]interface{}{
		"teams": []string{"backend-go", "frontend-react"}, "summary": "todo app",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, ok, err := squads.Load(filepath.Join(root, ".slmcode"))
	if err != nil || !ok {
		t.Fatalf("no plan written: ok=%v err=%v", ok, err)
	}
	if len(p.Squads) != 2 {
		t.Fatalf("squads=%v", p.IDs())
	}
	// GET /api/squads is what the page reads back, and it was the endpoint that
	// used to be empty. It must now answer.
	rec = do(t, s, http.MethodGet, "/api/squads", nil)
	if out := decode(t, rec); out["ok"] != true {
		t.Fatalf("squads view=%v", out)
	}
}

// Activating has to change what the NEXT run does, not just what is on disk:
// the run recomputes its own preselection, so an org chart with no matching pin
// is overwritten and the button appears to work while changing nothing.
func TestActivatePinsTheTeamsSoTheNextRunKeepsThem(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams/activate", map[string]interface{}{
		"teams": []string{"backend-go", "docs"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// `docs` ships opted out of automatic selection, so it is exactly the case
	// that only a persisted pin can keep on the run.
	cfg := s.cfg()
	if len(cfg.Teams) != 2 || cfg.Teams[0] != "backend-go" || cfg.Teams[1] != "docs" {
		t.Fatalf("config teams = %v, want the activated pair pinned", cfg.Teams)
	}
	rec = do(t, s, http.MethodGet, "/api/teams", nil)
	pinned, _ := decode(t, rec)["pinned"].([]interface{})
	if len(pinned) != 2 {
		t.Fatalf("the page must read the pin back: %v", pinned)
	}
}

// One team is the single-stream pipeline wearing a hat. Refusing it here, with
// the reasons, beats writing a plan that nothing will use.
func TestActivateRefusesAOneTeamPlan(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams/activate", map[string]interface{}{
		"teams": []string{"backend-go"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Two teams whose globs collide would mean two agents writing one file in
// parallel. The request is understood and the plan is not runnable: 422, with
// the reasons the page shows.
func TestActivateRefusesOverlappingOwnershipWithReasons(t *testing.T) {
	s, _ := teamServer(t)
	if rec := do(t, s, http.MethodPost, "/api/teams", map[string]interface{}{
		"id": "greedy", "owns": []string{"**"}, "acceptance": "true",
	}); rec.Code != http.StatusOK {
		t.Fatalf("setup: %s", rec.Body.String())
	}
	rec := do(t, s, http.MethodPost, "/api/teams/activate", map[string]interface{}{
		"teams": []string{"backend-go", "greedy"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decode(t, rec)
	probs, _ := out["problems"].([]interface{})
	if len(probs) == 0 {
		t.Fatalf("a refusal with no reasons is unfixable: %v", out)
	}
}

// An id the library does not have is dropped rather than failing the request —
// but the response has to SAY so. Activating two of the three teams the user
// asked for is the kind of near-miss nobody notices until the run is short one.
func TestActivateReportsTheIdsItDropped(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams/activate", map[string]interface{}{
		"teams": []string{"backend-go", "frontend-react", "team-that-was-deleted"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decode(t, rec)
	unknown, _ := out["unknown"].([]interface{})
	if len(unknown) != 1 || unknown[0] != "team-that-was-deleted" {
		t.Fatalf("unknown = %v — a silently dropped id is a team missing from the run", out["unknown"])
	}
	if teams, _ := out["teams"].([]interface{}); len(teams) != 2 {
		t.Fatalf("the two real teams must still activate: %v", out["teams"])
	}
}

// A stale id and a contested path are different failures, and the refusal has
// to say which — "overlap" sends the user looking for a collision that is not
// there.
func TestActivateNamesATeamThatDoesNotExist(t *testing.T) {
	s, _ := teamServer(t)
	rec := do(t, s, http.MethodPost, "/api/teams/activate", map[string]interface{}{
		"teams": []string{"backend-go", "team-that-was-deleted"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "team-that-was-deleted") || !strings.Contains(body, "no such team") {
		t.Fatalf("the refusal must name the missing id: %s", body)
	}
}

// The contract is the one artifact a two-team run cannot recover from getting
// wrong. Editing it outside a run has to work, and has to be atomic with the
// org-chart edit in the same request.
func TestPatchSquadsEditsTheFrozenContract(t *testing.T) {
	s, root := teamServer(t)
	slmDir := filepath.Join(root, ".slmcode")
	if err := squads.Save(slmDir, squads.Plan{
		Squads: []squads.Squad{
			{ID: "backend", Owns: []string{"cmd/**"}, Acceptance: "go test ./..."},
			{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm run build"},
		},
		Contract: squads.Contract{Interfaces: []squads.Interface{
			{ID: "GET /todos", Provider: "backend", Consumers: []string{"frontend"}, Spec: "200 -> []"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	rec := do(t, s, http.MethodPatch, "/api/squads", map[string]interface{}{
		"interfaces": []map[string]interface{}{
			{"id": "GET /todos", "rename": "GET /api/todos", "spec": "200 -> [{id,title,done}]"},
			{"id": "POST /api/todos", "new": true, "provider": "backend",
				"consumers": []string{"frontend"}, "consumers_set": true, "spec": "{title} -> 201"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, _, err := squads.Load(slmDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Contract.Interfaces) != 2 {
		t.Fatalf("interfaces=%+v", p.Contract.Interfaces)
	}
	if p.Contract.Interfaces[0].ID != "GET /api/todos" {
		t.Fatalf("rename lost: %+v", p.Contract.Interfaces[0])
	}
	if p.Contract.Interfaces[0].Spec != "200 -> [{id,title,done}]" {
		t.Fatalf("spec lost: %+v", p.Contract.Interfaces[0])
	}
	if p.Contract.Interfaces[1].Provider != "backend" {
		t.Fatalf("new clause=%+v", p.Contract.Interfaces[1])
	}

	// The contract on disk is what the agents read. It must be rewritten too —
	// a JSON plan that disagrees with CONTRACT.md is the worst failure this
	// package has.
	body, err := os.ReadFile(filepath.Join(slmDir, squads.ContractFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "GET /api/todos") {
		t.Fatalf("CONTRACT.md was not regenerated:\n%s", body)
	}
}

// A clause naming a team that is not on the run is owed by nobody, and keeping
// it fails validation for the whole plan.
func TestPatchSquadsRefusesAClauseWithNoProvider(t *testing.T) {
	s, root := teamServer(t)
	if err := squads.Save(filepath.Join(root, ".slmcode"), squads.Plan{
		Squads: []squads.Squad{
			{ID: "backend", Owns: []string{"cmd/**"}, Acceptance: "go test ./..."},
			{ID: "frontend", Owns: []string{"web/**"}, Acceptance: "npm run build"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	rec := do(t, s, http.MethodPatch, "/api/squads", map[string]interface{}{
		"interfaces": []map[string]interface{}{
			{"id": "GET /x", "new": true, "provider": "nobody"},
		},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not a squad in this plan") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
