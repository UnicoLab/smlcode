package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/piotrlaczkowski/slmcode/pkg/agents"
	"github.com/piotrlaczkowski/slmcode/pkg/config"
	"github.com/piotrlaczkowski/slmcode/pkg/harness"
	"github.com/piotrlaczkowski/slmcode/pkg/knowledge"
	"github.com/piotrlaczkowski/slmcode/pkg/orchestrator"
	"github.com/piotrlaczkowski/slmcode/pkg/plan"
	"github.com/piotrlaczkowski/slmcode/pkg/server"
	"github.com/piotrlaczkowski/slmcode/pkg/skills"
)

func TestStudioAPIAndKnowledge(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\nfunc Hello() string { return \"hi\" }\n"), 0o644)
	cfg := config.Default(root)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Config = cfg

	srv := server.New(h, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health %d", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/agents")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var ag []map[string]interface{}
	if err := json.Unmarshal(body, &ag); err != nil {
		t.Fatal(string(body), err)
	}
	want := map[string]bool{"coordinator": true, "docs": true, "deep": true, "architect": true, "worker": true}
	for _, a := range ag {
		id, _ := a["id"].(string)
		delete(want, id)
	}
	if len(want) > 0 {
		t.Fatalf("missing agents %v (roster=%d)", want, len(ag))
	}
	if len(agents.Specs()) < 12 {
		t.Fatalf("expected rich roster, got %d", len(agents.Specs()))
	}

	payload := []byte(`{"title":"T","description":"d","column":"ready_to_dev","role":"worker"}`)
	resp, err = http.Post(ts.URL+"/api/tasks", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("add task %d", resp.StatusCode)
	}

	board := &plan.Board{Tasks: []plan.Task{{
		ID: "T1", Title: "x", Column: plan.ColDone, Files: []string{"hello.go"},
		Output: `{"status":"done","files_changed":["hello.go"]}`,
	}}}
	board.Tasks[0].Normalize()
	ev, err := knowledge.Evolve(cfg.SlmDir(), "doc hello", board, "- prefer godoc\n", []skills.Skill{
		{Name: "atomic-coding", Description: "tiny", Path: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.SlmDir(), ev.SkillsIndex)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.SlmDir(), ev.LearnedSkill)); err != nil {
		t.Fatal(err)
	}

	resp, err = http.Get(ts.URL + "/api/docs")
	if err != nil {
		t.Fatal(err)
	}
	docsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(docsBody, []byte("SKILLS.md")) {
		t.Fatalf("docs=%s", docsBody)
	}
}
