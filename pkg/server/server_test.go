package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/UnicoLab/slmcode/pkg/composer"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/permissions"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/workspace"
)

func TestPutConfigPartialPreservesDryRun(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	h.Config.DryRun = true
	h.Config.Permission = permissions.ModeDryRun
	if err := h.Config.Save(); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	body := []byte(`{"model":"patched-model"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["model"] != "patched-model" {
		t.Fatalf("model=%v", out["model"])
	}
	if out["dry_run"] != true {
		t.Fatalf("dry_run cleared: %v", out["dry_run"])
	}
	if out["permission"] != permissions.ModeDryRun {
		t.Fatalf("permission=%v", out["permission"])
	}
}

func TestPutConfigRejectedWhileRunActive(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"model":"patched-model"}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if h.Config.Model == "patched-model" {
		t.Fatal("config changed while run was active")
	}
}

func TestStatusIncludesOperationalFields(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	ask := plan.BuildPlanApproveAsk("ship it", &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "main"}},
	})
	if err := hitl.WriteAsk(h.Config.SlmDir(), "plan", ask); err != nil {
		t.Fatal(err)
	}
	comp := composer.Composition{
		Summary: "focused dynamic pipeline",
		Phases:  []composer.PhaseChoice{{ID: "plan", Agent: "planner", Enabled: true, When: "always"}},
		Execute: composer.ExecuteChoice{
			DefaultRole: "worker",
			Reviewer:    "reviewer",
			Corrector:   "corrector",
			MaxWaves:    1,
		},
	}
	if err := composer.SaveDynamic(h.Config.SlmDir(), &comp); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(fmt.Sprint(out["text"])) == "" {
		t.Fatalf("missing text status: %+v", out)
	}
	if out["running"] != true || out["plan_pending"] != true {
		t.Fatalf("missing operational flags: %+v", out)
	}
	if out["readiness"] == nil || out["composition"] == nil {
		t.Fatalf("missing readiness/composition: %+v", out)
	}
}

func TestPlanApproveRequiresCurrentAskID(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	ask := plan.BuildPlanApproveAsk("build cli", &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "main"}},
	})
	if err := hitl.WriteAsk(h.Config.SlmDir(), "plan", ask); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	bad := httptest.NewRequest(http.MethodPost, "/api/plan/approve", strings.NewReader(`{"ask_id":"old","decision":"approve"}`))
	badRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusConflict {
		t.Fatalf("mismatch status=%d body=%s", badRec.Code, badRec.Body.String())
	}

	body := `{"ask_id":"` + ask.ID + `","decision":"approve","notes":"keep it focused"}`
	good := httptest.NewRequest(http.MethodPost, "/api/plan/approve", strings.NewReader(body))
	goodRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(goodRec, good)
	if goodRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", goodRec.Code, goodRec.Body.String())
	}

	var ans plan.PlanApproveAnswer
	ok, err := hitl.ReadAnswers(h.Config.SlmDir(), "plan", &ans)
	if err != nil || !ok {
		t.Fatalf("read answer ok=%v err=%v", ok, err)
	}
	if ans.AskID != ask.ID || ans.Notes != "keep it focused" {
		t.Fatalf("answer=%+v", ans)
	}

	pending := httptest.NewRequest(http.MethodGet, "/api/plan/pending", nil)
	pendingRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(pendingRec, pending)
	if pendingRec.Code != http.StatusOK {
		t.Fatalf("pending status=%d body=%s", pendingRec.Code, pendingRec.Body.String())
	}
	var pendingOut map[string]any
	if err := json.Unmarshal(pendingRec.Body.Bytes(), &pendingOut); err != nil {
		t.Fatal(err)
	}
	if pendingOut["pending"] != false || pendingOut["answered"] != true {
		t.Fatalf("pending response=%+v", pendingOut)
	}

	dup := httptest.NewRequest(http.MethodPost, "/api/plan/approve", strings.NewReader(body))
	dupRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(dupRec, dup)
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", dupRec.Code, dupRec.Body.String())
	}
}

func TestPlanApproveRejectsInvalidDecision(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	ask := plan.BuildPlanApproveAsk("build cli", &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "main"}},
	})
	if err := hitl.WriteAsk(h.Config.SlmDir(), "plan", ask); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	for _, body := range []string{
		`{"ask_id":"` + ask.ID + `"}`,
		`{"ask_id":"` + ask.ID + `","decision":"edit"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/plan/approve", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if ok, err := hitl.ReadAnswers(h.Config.SlmDir(), "plan", &plan.PlanApproveAnswer{}); err != nil || ok {
		t.Fatalf("invalid decision wrote answer ok=%v err=%v", ok, err)
	}
}

func TestHITLAnswersRequireCurrentAskIDForAllKinds(t *testing.T) {
	type hitlCase struct {
		name     string
		endpoint string
		writeAsk func(*harness.Harness, string) error
		body     func(string) string
	}
	cases := []hitlCase{
		{
			name:     "clarify",
			endpoint: "/api/clarify/answer",
			writeAsk: func(h *harness.Harness, id string) error {
				return plan.WriteScopeAsk(h.Config.SlmDir(), plan.ScopeAsk{
					ID:        id,
					Kind:      "clarify",
					Query:     "q",
					Questions: []plan.ScopeQuestion{{ID: "q1", Question: "Pick?", Options: []plan.ScopeOption{{Label: "A"}}}},
					TimeoutS:  120,
					OnTimeout: "use_recommended",
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
				})
			},
			body: func(id string) string {
				return `{"ask_id":"` + id + `","answers":[{"question_id":"q1","selected":["A"]}]}`
			},
		},
		{
			name:     "plan",
			endpoint: "/api/plan/approve",
			writeAsk: func(h *harness.Harness, id string) error {
				ask := plan.BuildPlanApproveAsk("q", &plan.Board{Tasks: []plan.Task{{ID: "T1", Title: "main"}}})
				ask.ID = id
				ask.TimeoutS = 120
				return hitl.WriteAsk(h.Config.SlmDir(), "plan", ask)
			},
			body: func(id string) string {
				return `{"ask_id":"` + id + `","decision":"approve"}`
			},
		},
		{
			name:     "continue",
			endpoint: "/api/continue/answer",
			writeAsk: func(h *harness.Harness, id string) error {
				ask := plan.BuildContinueAsk("q", "reason", nil, nil)
				ask.ID = id
				ask.TimeoutS = 120
				return hitl.WriteAsk(h.Config.SlmDir(), "continue", ask)
			},
			body: func(id string) string {
				return `{"ask_id":"` + id + `","action":"continue"}`
			},
		},
		{
			name:     "escalate",
			endpoint: "/api/escalate/answer",
			writeAsk: func(h *harness.Harness, id string) error {
				ask := plan.BuildEscalateAsk(plan.Task{ID: "T1", Title: "main"}, "detail", 120)
				ask.ID = id
				return hitl.WriteAsk(h.Config.SlmDir(), "escalate", ask)
			},
			body: func(id string) string {
				return `{"ask_id":"` + id + `","action":"retry"}`
			},
		},
		{
			name:     "shell",
			endpoint: "/api/shell/approve",
			writeAsk: func(h *harness.Harness, id string) error {
				return hitl.WriteAsk(h.Config.SlmDir(), "shell", workspace.ShellAsk{
					ID:        id,
					Kind:      "shell",
					Command:   "go test ./...",
					TimeoutS:  120,
					OnTimeout: "deny",
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
				})
			},
			body: func(id string) string {
				return `{"ask_id":"` + id + `","decision":"approve"}`
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			h, err := harness.New(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Init(); err != nil {
				t.Fatal(err)
			}
			s := New(h, nil)

			missing := httptest.NewRequest(http.MethodPost, tc.endpoint, strings.NewReader(tc.body("ask-1")))
			missingRec := httptest.NewRecorder()
			s.Handler().ServeHTTP(missingRec, missing)
			if missingRec.Code != http.StatusNotFound {
				t.Fatalf("missing status=%d body=%s", missingRec.Code, missingRec.Body.String())
			}

			if err := tc.writeAsk(h, "ask-1"); err != nil {
				t.Fatal(err)
			}
			mismatch := httptest.NewRequest(http.MethodPost, tc.endpoint, strings.NewReader(tc.body("old")))
			mismatchRec := httptest.NewRecorder()
			s.Handler().ServeHTTP(mismatchRec, mismatch)
			if mismatchRec.Code != http.StatusConflict {
				t.Fatalf("mismatch status=%d body=%s", mismatchRec.Code, mismatchRec.Body.String())
			}

			success := httptest.NewRequest(http.MethodPost, tc.endpoint, strings.NewReader(tc.body("ask-1")))
			successRec := httptest.NewRecorder()
			s.Handler().ServeHTTP(successRec, success)
			if successRec.Code != http.StatusOK {
				t.Fatalf("success status=%d body=%s", successRec.Code, successRec.Body.String())
			}

			duplicate := httptest.NewRequest(http.MethodPost, tc.endpoint, strings.NewReader(tc.body("ask-1")))
			duplicateRec := httptest.NewRecorder()
			s.Handler().ServeHTTP(duplicateRec, duplicate)
			if duplicateRec.Code != http.StatusConflict {
				t.Fatalf("duplicate status=%d body=%s", duplicateRec.Code, duplicateRec.Body.String())
			}
		})
	}
}

func TestPlanApproveRejectsAnswerPastDeadline(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	ask := plan.BuildPlanApproveAsk("build cli", &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "main"}},
	})
	ask.TimeoutS = 1
	ask.CreatedAt = time.Now().UTC().Add(-2 * time.Second).Format(time.RFC3339Nano)
	if err := hitl.WriteAsk(h.Config.SlmDir(), "plan", ask); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	body := `{"ask_id":"` + ask.ID + `","decision":"approve"}`
	req := httptest.NewRequest(http.MethodPost, "/api/plan/approve", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPlanPendingClearsExpiredAsk(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	ask := plan.BuildPlanApproveAsk("build cli", &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "main"}},
	})
	ask.TimeoutS = 1
	ask.CreatedAt = time.Now().UTC().Add(-10 * time.Second).Format(time.RFC3339)
	if err := hitl.WriteAsk(h.Config.SlmDir(), "plan", ask); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/plan/pending", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["pending"] != false || out["expired"] != true {
		t.Fatalf("response=%+v", out)
	}
	ok, err := hitl.ReadAsk(h.Config.SlmDir(), "plan", &plan.PlanApproveAsk{})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expired ask was not cleared")
	}
}

func TestShellApproveRejectsInvalidDecision(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	ask := workspace.ShellAsk{
		ID:        "shell-1",
		Kind:      "shell",
		Command:   "go test ./...",
		TimeoutS:  120,
		OnTimeout: "deny",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := hitl.WriteAsk(h.Config.SlmDir(), "shell", ask); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	for _, body := range []string{
		`{"ask_id":"` + ask.ID + `"}`,
		`{"ask_id":"` + ask.ID + `","decision":"allow"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/shell/approve", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if ok, err := hitl.ReadAnswers(h.Config.SlmDir(), "shell", &workspace.ShellAnswer{}); err != nil || ok {
		t.Fatalf("invalid decision wrote answer ok=%v err=%v", ok, err)
	}
}

func TestPlanApproveRejectsExpiredAsk(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	ask := plan.BuildPlanApproveAsk("build cli", &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "main"}},
	})
	ask.TimeoutS = 1
	ask.CreatedAt = time.Now().UTC().Add(-10 * time.Second).Format(time.RFC3339)
	if err := hitl.WriteAsk(h.Config.SlmDir(), "plan", ask); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	body := `{"ask_id":"` + ask.ID + `","decision":"approve"}`
	req := httptest.NewRequest(http.MethodPost, "/api/plan/approve", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPlanPendingKeepsActiveAsk(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	ask := plan.BuildPlanApproveAsk("build cli", &plan.Board{
		Tasks: []plan.Task{{ID: "T1", Title: "main"}},
	})
	ask.TimeoutS = 120
	if err := hitl.WriteAsk(h.Config.SlmDir(), "plan", ask); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/plan/pending", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Pending bool                `json:"pending"`
		Ask     plan.PlanApproveAsk `json:"ask"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Pending || out.Ask.ID != ask.ID {
		t.Fatalf("response=%+v", out)
	}
}

func TestGetCompositionEndpoint(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	comp := &composer.Composition{
		Summary: "dynamic",
		Handoff: []string{"Verify with go test ./..."},
		Phases:  []composer.PhaseChoice{{ID: "execute", Agent: "go-worker", Enabled: true}},
		Execute: composer.ExecuteChoice{DefaultRole: "go-worker", Reviewer: "reviewer", Corrector: "corrector", MaxWaves: 2},
		Team:    []composer.TeamMember{{Role: "go-worker", Skills: []string{"atomic-coding"}}},
	}
	if err := composer.SaveDynamic(h.Config.SlmDir(), comp); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/composition", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("ok=%v", out["ok"])
	}
	got, _ := out["composition"].(map[string]any)
	if got["summary"] != "dynamic" {
		t.Fatalf("composition=%+v", got)
	}
	fit, _ := got["slm_fit"].([]any)
	if len(fit) == 0 || !strings.Contains(fmt.Sprint(fit), "enabled phases") {
		t.Fatalf("missing slm_fit in composition: %+v", got)
	}
}

func TestGetCompositionEndpointReportsLoadError(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Config.SlmDir(), composer.DynamicFileName), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/composition", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK               bool   `json:"ok"`
		Composition      any    `json:"composition"`
		CompositionError string `json:"composition_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.OK || out.Composition != nil || !strings.Contains(out.CompositionError, "read dynamic composition") {
		t.Fatalf("unexpected response: %+v body=%s", out, rec.Body.String())
	}
}

func TestPreviewCompositionEndpointIsSideEffectFree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg", "orchestrator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "orchestrator", "composer.go"), []byte("package orchestrator\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	body := []byte(`{"query":"fix composer.go dynamic pipeline preview"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/composition/preview", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		OK             bool                 `json:"ok"`
		DynamicEnabled bool                 `json:"dynamic_enabled"`
		Composition    composer.Composition `json:"composition"`
		SLMFit         []string             `json:"slm_fit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || !got.DynamicEnabled {
		t.Fatalf("preview flags wrong: %+v", got)
	}
	if got.Composition.Execute.DefaultRole != "go-worker" {
		t.Fatalf("default role=%q composition=%+v", got.Composition.Execute.DefaultRole, got.Composition)
	}
	if !strings.Contains(strings.Join(got.Composition.Handoff, "\n"), "composer.go") {
		t.Fatalf("preview missing target handoff: %+v", got.Composition.Handoff)
	}
	if !strings.Contains(strings.Join(got.SLMFit, "\n"), "enabled phases") || !strings.Contains(strings.Join(got.SLMFit, "\n"), "handoff") {
		t.Fatalf("preview missing SLM fit hints: %+v", got.SLMFit)
	}
	if _, err := os.Stat(filepath.Join(h.Config.SlmDir(), composer.DynamicFileName)); !os.IsNotExist(err) {
		t.Fatalf("preview should not persist latest composition, stat err=%v", err)
	}
}

func TestReadinessEndpointReportsSLMGuards(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	h.Config.DynamicPipeline = true
	h.Config.QAGate = true
	h.Config.RequireSmoke = true
	h.Config.ClaimsGate = true
	h.Config.OverEditGuard = true
	h.Config.WriteGuard = true
	h.Config.ReadBeforeEdit = true
	h.Config.FileCheckpoints = true
	h.Config.ShellWriteGuard = true
	h.Config.ShellWhitelist = true
	h.Config.ContextCompact = true
	h.Config.ReactCompact = true
	h.Config.SessionEventLog = true

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/readiness", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		OK     bool            `json:"ok"`
		Score  int             `json:"score"`
		Guards map[string]bool `json:"guards"`
		Checks []struct {
			ID       string         `json:"id"`
			OK       bool           `json:"ok"`
			FixPatch map[string]any `json:"fix_patch"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Score < 80 {
		t.Fatalf("readiness not ok: %+v body=%s", got, rec.Body.String())
	}
	if !got.Guards["write_guard"] || !got.Guards["claims_gate"] || !got.Guards["react_compact"] {
		t.Fatalf("missing guard state: %+v", got.Guards)
	}
	foundDynamic := false
	for _, check := range got.Checks {
		if check.ID == "dynamic_pipeline" && check.OK {
			foundDynamic = true
		}
	}
	if !foundDynamic {
		t.Fatalf("dynamic pipeline check missing: %+v", got.Checks)
	}

	h.Config.DynamicPipeline = false
	req = httptest.NewRequest(http.MethodGet, "/api/readiness", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	foundFix := false
	for _, check := range got.Checks {
		if check.ID == "dynamic_pipeline" {
			foundFix = check.OK == false && check.FixPatch["dynamic_pipeline"] == true
		}
	}
	if !foundFix {
		t.Fatalf("dynamic pipeline fix missing: %+v", got.Checks)
	}
}

func TestReadinessEndpointProbeIsExplicit(t *testing.T) {
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"local-coder"}]}`))
	}))
	defer modelSrv.Close()

	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	h.Config.Provider = "omlx"
	h.Config.Endpoint = modelSrv.URL
	h.Config.Model = "local-coder"

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/readiness", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var fast struct {
		Checks []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fast); err != nil {
		t.Fatal(err)
	}
	for _, check := range fast.Checks {
		if check.ID == "provider_model" {
			t.Fatalf("default readiness should not probe provider: %+v", fast.Checks)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/api/readiness?probe=1", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var probed struct {
		Checks []struct {
			ID       string `json:"id"`
			OK       bool   `json:"ok"`
			Endpoint string `json:"endpoint"`
			Latency  int64  `json:"latency_ms"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &probed); err != nil {
		t.Fatal(err)
	}
	foundProbe := false
	for _, check := range probed.Checks {
		if check.ID == "provider_model" {
			foundProbe = check.OK && check.Endpoint == modelSrv.URL && check.Latency >= 0
		}
	}
	if !foundProbe {
		t.Fatalf("probe readiness missing provider_model success: %+v", probed.Checks)
	}
}

func TestPutConfigSetsPermission(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	body := []byte(`{"permission":"review"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if h.Config.Permission != permissions.ModeReview {
		t.Fatalf("permission=%s", h.Config.Permission)
	}
	if h.Config.DryRun {
		t.Fatal("dry_run should be false for review")
	}

	// Clear via dry_run false after dry-run mode
	body = []byte(`{"permission":"dry-run"}`)
	req = httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if !h.Config.DryRun {
		t.Fatal("expected dry_run")
	}

	body = []byte(`{"dry_run":false}`)
	req = httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if h.Config.DryRun || h.Config.Permission != permissions.ModeAuto {
		t.Fatalf("clear: dry=%v perm=%s", h.Config.DryRun, h.Config.Permission)
	}

	body = []byte(`{"shell_whitelist":false}`)
	req = httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if h.Config.ShellWhitelist {
		t.Fatal("shell_whitelist patch was not applied")
	}
}

func TestGetBuiltinAgentDetailIncludesPrompt(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/agents/worker", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var detail map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	sp, _ := detail["system_prompt"].(string)
	if !strings.Contains(sp, "HARD SCOPE") {
		t.Fatalf("detail missing built-in prompt: %v", detail["system_prompt"])
	}
	// List must stay lean (no prompt dump for built-ins without override).
	req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var list []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a["id"] == "worker" {
			if _, ok := a["system_prompt"]; ok {
				t.Fatal("list must not include system_prompt for built-in worker")
			}
		}
	}
}

func TestAgentsCRUD(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)

	body := []byte(`{"id":"night-auditor","title":"Night Auditor","description":"quiet review","system_prompt":"Audit carefully.","skills":["atomic-coding"],"tools":true,"provider":"ollama","model":"qwen2.5-coder:14b","endpoint":"http://127.0.0.1:11434"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(h.Config.AgentsDir(), "night-auditor.yaml")); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "night-auditor") {
		t.Fatalf("list missing custom agent: %s", rec.Body.String())
	}

	upd := []byte(`{"id":"night-auditor","title":"Night Auditor v2","system_prompt":"Audit v2.","tools":false}`)
	req = httptest.NewRequest(http.MethodPut, "/api/agents/night-auditor", bytes.NewReader(upd))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("put %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/agents/night-auditor", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(h.Config.AgentsDir(), "night-auditor.yaml")); !os.IsNotExist(err) {
		t.Fatal("expected file removed")
	}

	// Cannot delete built-in without an override file
	req = httptest.NewRequest(http.MethodDelete, "/api/agents/worker", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Fatal("must not delete built-in worker without override")
	}

	// Builtin override via PUT, then Reset (DELETE override)
	ovr := []byte(`{"id":"worker","provider":"ollama","model":"qwen2.5-coder:7b","max_iter":18}`)
	req = httptest.NewRequest(http.MethodPut, "/api/agents/worker", bytes.NewReader(ovr))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("override put %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(h.Config.AgentsDir(), "worker.yaml")); err != nil {
		t.Fatal("expected worker override yaml")
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/agents/worker", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("reset override %d %s", rec.Code, rec.Body.String())
	}
}

func TestHealth(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestBoardNeverNullTasks(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/board", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	tasks, ok := out["tasks"].([]interface{})
	if !ok {
		t.Fatalf("tasks should be array, got %T %v", out["tasks"], out["tasks"])
	}
	if tasks == nil {
		t.Fatal("tasks null")
	}
}

func TestSSESendsConnected(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	// Give handler time to write hello
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	body := rec.Body.String()
	if !strings.Contains(body, "studio api connected") && !strings.Contains(body, `"kind":"connected"`) {
		t.Fatalf("missing connected event: %s", body)
	}
}

func TestArchivesAPI(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.Config.SlmDir(), "archives")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "20260730_120000_run-demo.md"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("# Archive\n\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/archives", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/archives/"+name, nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("missing content: %s", rec.Body.String())
	}
}

func TestQueriesAPI(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	turn, err := session.BeginTurn(h.Config.SlmDir(), "run-q1", "scope me")
	if err != nil {
		t.Fatal(err)
	}
	board := plan.Board{
		QueryID: turn.ID, Query: turn.Query,
		Plan:  plan.Plan{Summary: "scoped"},
		Tasks: []plan.Task{{ID: "T1", Title: "do", Column: plan.ColDone}},
	}
	board.Tasks[0].Normalize()
	_ = session.SaveTurnBoard(h.Config.SlmDir(), turn, board)
	_, _ = session.WriteTurnSummary(h.Config.SlmDir(), turn, board, "lesson")
	comp := &composer.Composition{
		Summary: "go coding composition",
		Handoff: []string{"Verify with go test ./..."},
		Phases:  []composer.PhaseChoice{{ID: "execute", Agent: "go-worker", Enabled: true}},
		Execute: composer.ExecuteChoice{DefaultRole: "go-worker", Reviewer: "reviewer", Corrector: "corrector", MaxWaves: 2},
		Team:    []composer.TeamMember{{Role: "go-worker", Skills: []string{"atomic-coding"}}},
	}
	if err := composer.SaveDynamic(session.TurnDir(h.Config.SlmDir(), turn.ID), comp); err != nil {
		t.Fatal(err)
	}
	h.Config.DynamicPipeline = false

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/queries", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "run-q1") {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/queries/run-q1", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "scope me") {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "summary_md") {
		t.Fatalf("expected summary_md: %s", rec.Body.String())
	}
	var got struct {
		Composition      map[string]any `json:"composition"`
		CompositionError string         `json:"composition_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.CompositionError != "" {
		t.Fatalf("unexpected composition error: %s", got.CompositionError)
	}
	if got.Composition == nil || got.Composition["summary"] != comp.Summary {
		t.Fatalf("composition not returned: %s", rec.Body.String())
	}
	if fit, _ := got.Composition["slm_fit"].([]any); len(fit) == 0 || !strings.Contains(fmt.Sprint(fit), "enabled phases") {
		t.Fatalf("missing slm_fit in query composition: %+v", got.Composition)
	}
	if strings.Contains(fmt.Sprint(got.Composition["slm_fit"]), "enable dynamic_pipeline") {
		t.Fatalf("historical composition used current dynamic_pipeline=false hint: %+v", got.Composition["slm_fit"])
	}
}

func TestQueriesAPIReportsCompositionLoadError(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	turn, err := session.BeginTurn(h.Config.SlmDir(), "run-q2", "inspect corrupt composition")
	if err != nil {
		t.Fatal(err)
	}
	board := plan.Board{
		QueryID: turn.ID, Query: turn.Query,
		Plan:  plan.Plan{Summary: "scoped"},
		Tasks: []plan.Task{{ID: "T1", Title: "do", Column: plan.ColDone}},
	}
	board.Tasks[0].Normalize()
	if err := session.SaveTurnBoard(h.Config.SlmDir(), turn, board); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session.TurnDir(h.Config.SlmDir(), turn.ID), composer.DynamicFileName), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/queries/run-q2", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Composition      any    `json:"composition"`
		CompositionError string `json:"composition_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Composition != nil || !strings.Contains(got.CompositionError, "read dynamic composition") {
		t.Fatalf("unexpected response: %+v body=%s", got, rec.Body.String())
	}
}

func TestInterruptedRunsAPIAndResumeConflict(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	turn, err := session.BeginTurn(h.Config.SlmDir(), "run-stop-1", "finish interrupted work")
	if err != nil {
		t.Fatal(err)
	}
	board := plan.Board{
		QueryID: turn.ID,
		Query:   turn.Query,
		Tasks: []plan.Task{
			{ID: "T1", Title: "done", Column: plan.ColDone},
			{ID: "T2", Title: "blocked", Column: plan.ColBlocked, Error: "review failed"},
		},
	}
	for i := range board.Tasks {
		board.Tasks[i].Normalize()
	}
	if err := session.MarkInterrupted(h.Config.SlmDir(), turn, board, session.PhaseExecute); err != nil {
		t.Fatal(err)
	}

	s := New(h, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/runs/interrupted", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list []struct {
		ID          string `json:"id"`
		Phase       string `json:"phase"`
		Tasks       int    `json:"tasks"`
		Done        int    `json:"done"`
		Blocked     int    `json:"blocked"`
		ReactResume bool   `json:"react_resume"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "run-stop-1" || list[0].Phase != session.PhaseExecute {
		t.Fatalf("unexpected list: %+v", list)
	}
	if list[0].Tasks != 2 || list[0].Done != 1 || list[0].Blocked != 1 || list[0].ReactResume {
		t.Fatalf("unexpected counters: %+v", list[0])
	}

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	req = httptest.NewRequest(http.MethodPost, "/api/runs/resume", strings.NewReader(`{"id":"run-stop-1"}`))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSPAContentTypes(t *testing.T) {
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html":                     &fstest.MapFile{Data: []byte("<html>ok</html>")},
		"app.jsx":                        &fstest.MapFile{Data: []byte("const x = 1")},
		"styles.css":                     &fstest.MapFile{Data: []byte("body{}")},
		"vendor/react.production.min.js": &fstest.MapFile{Data: []byte("/*react*/")},
	}
	s := New(h, ui)
	cases := []struct{ path, want string }{
		{"/app.jsx", "text/javascript"},
		{"/styles.css", "text/css"},
		{"/vendor/react.production.min.js", "application/javascript"},
		{"/", "text/html"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s status=%d", tc.path, rec.Code)
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.Contains(ct, tc.want) {
			t.Fatalf("%s Content-Type=%q want substring %q", tc.path, ct, tc.want)
		}
	}
}
