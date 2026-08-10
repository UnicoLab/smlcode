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
)

// newBlocksTestServer builds a harness + Server on a fresh temp project,
// following the setup pattern used across pkg/server tests.
func newBlocksTestServer(t *testing.T) (*Server, *harness.Harness) {
	t.Helper()
	root := t.TempDir()
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Init(); err != nil {
		t.Fatal(err)
	}
	return New(h, nil), h
}

// doReq performs a request against the server and returns the recorder.
func doReq(t *testing.T, s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

const testAgentBlock = `{
  "api_version": "blocks/v1",
  "kind": "agent",
  "id": "my-agent",
  "name": "My Agent",
  "description": "custom test agent",
  "spec": {
    "id": "my-agent",
    "title": "My Agent",
    "description": "custom test agent",
    "system_prompt": "You are a careful specialist.",
    "tools": true,
    "max_iter": 12,
    "temperature": 0.1,
    "max_tokens": 2048
  }
}`

func TestBlocksCreateAgent(t *testing.T) {
	s, h := newBlocksTestServer(t)

	rec := doReq(t, s, http.MethodPost, "/api/blocks/agent", []byte(testAgentBlock))
	if rec.Code != 200 {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["ok"] != true {
		t.Fatalf("create ok=%v, want true", created["ok"])
	}
	path, _ := created["path"].(string)
	wantSuffix := filepath.Join(".slmcode", "blocks", "agents", "my-agent.yaml")
	if !strings.HasSuffix(path, wantSuffix) {
		t.Fatalf("path=%q, want suffix %q", path, wantSuffix)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved block file missing: %v", err)
	}

	// GET returns the block with its spec.
	rec = doReq(t, s, http.MethodGet, "/api/blocks/agent/my-agent", nil)
	if rec.Code != 200 {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "my-agent" || got["name"] != "My Agent" {
		t.Fatalf("get block=%+v", got)
	}
	spec, _ := got["spec"].(map[string]interface{})
	if spec == nil || spec["title"] != "My Agent" || spec["system_prompt"] != "You are a careful specialist." {
		t.Fatalf("get spec=%+v", got["spec"])
	}

	// Duplicate create → 409.
	rec = doReq(t, s, http.MethodPost, "/api/blocks/agent", []byte(testAgentBlock))
	if rec.Code != 409 {
		t.Fatalf("duplicate status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("duplicate body=%q, want 'already exists'", rec.Body.String())
	}

	// The block is discoverable via the harness config root on disk.
	blockPath := filepath.Join(h.Config.Root, ".slmcode", "blocks", "agents", "my-agent.yaml")
	if _, err := os.Stat(blockPath); err != nil {
		t.Fatalf("expected project block file: %v", err)
	}
}

func TestBlocksCreateDuplicate(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	if rec := doReq(t, s, http.MethodPost, "/api/blocks/agent", []byte(testAgentBlock)); rec.Code != 200 {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec := doReq(t, s, http.MethodPost, "/api/blocks/agent", []byte(testAgentBlock))
	if rec.Code != 409 {
		t.Fatalf("duplicate status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
}

func TestBlocksUpdateAgent(t *testing.T) {
	s, h := newBlocksTestServer(t)

	if rec := doReq(t, s, http.MethodPost, "/api/blocks/agent", []byte(testAgentBlock)); rec.Code != 200 {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	upd := `{
	  "api_version": "blocks/v1",
	  "kind": "agent",
	  "id": "my-agent",
	  "name": "My Agent v2",
	  "spec": {
	    "id": "my-agent",
	    "title": "My Agent v2",
	    "system_prompt": "You are a sharper specialist.",
	    "tools": false,
	    "max_iter": 20
	  }
	}`
	rec := doReq(t, s, http.MethodPut, "/api/blocks/agent/my-agent", []byte(upd))
	if rec.Code != 200 {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated["ok"] != true {
		t.Fatalf("update ok=%v", updated["ok"])
	}

	// Block file on disk reflects the new name/spec.
	blockPath := filepath.Join(h.Config.Root, ".slmcode", "blocks", "agents", "my-agent.yaml")
	raw, err := os.ReadFile(blockPath)
	if err != nil {
		t.Fatalf("read block file: %v", err)
	}
	if !strings.Contains(string(raw), "My Agent v2") || !strings.Contains(string(raw), "sharper specialist") {
		t.Fatalf("block file not updated:\n%s", raw)
	}

	// Runtime agent materialized into .slmcode/agents/<id>.yaml.
	agentPath := filepath.Join(h.Config.AgentsDir(), "my-agent.yaml")
	if _, err := os.Stat(agentPath); err != nil {
		t.Fatalf("expected materialized agent file: %v", err)
	}
	materialized, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(materialized), "My Agent v2") {
		t.Fatalf("materialized agent missing update:\n%s", materialized)
	}

	// GET reflects the update.
	rec = doReq(t, s, http.MethodGet, "/api/blocks/agent/my-agent", nil)
	if rec.Code != 200 {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["name"] != "My Agent v2" {
		t.Fatalf("get name=%v, want My Agent v2", got["name"])
	}
	spec, _ := got["spec"].(map[string]interface{})
	if spec["title"] != "My Agent v2" {
		t.Fatalf("get spec.title=%v", spec["title"])
	}
}

func TestBlocksUpdatePipelineIDMismatch(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	body := []byte(`{
	  "api_version": "blocks/v1",
	  "kind": "pipeline",
	  "id": "other-pipe",
	  "name": "Other",
	  "spec": {"version": 1}
	}`)
	rec := doReq(t, s, http.MethodPut, "/api/blocks/pipeline/my-pipe", body)
	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "does not match path id") {
		t.Fatalf("body=%q, want id mismatch message", rec.Body.String())
	}
}

func TestBlocksDeleteQuality(t *testing.T) {
	s, h := newBlocksTestServer(t)

	create := []byte(`{
	  "api_version": "blocks/v1",
	  "kind": "quality",
	  "id": "qa-check",
	  "name": "QA Check",
	  "spec": {"qa_gate": "go test ./...", "test": [{"cmd": "go test ./...", "label": "go test"}]}
	}`)
	if rec := doReq(t, s, http.MethodPost, "/api/blocks/quality", create); rec.Code != 200 {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	blockPath := filepath.Join(h.Config.Root, ".slmcode", "blocks", "quality", "qa-check.yaml")
	if _, err := os.Stat(blockPath); err != nil {
		t.Fatalf("expected quality block file: %v", err)
	}

	rec := doReq(t, s, http.MethodDelete, "/api/blocks/quality/qa-check", nil)
	if rec.Code != 200 {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	var deleted map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted["ok"] != true {
		t.Fatalf("delete ok=%v, want true", deleted["ok"])
	}
	if _, err := os.Stat(blockPath); !os.IsNotExist(err) {
		t.Fatalf("expected block file removed, stat err=%v", err)
	}

	// Second delete → error status with message.
	rec = doReq(t, s, http.MethodDelete, "/api/blocks/quality/qa-check", nil)
	if rec.Code != 400 {
		t.Fatalf("second delete status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("second delete body=%q, want 'not found'", rec.Body.String())
	}
}

func TestBlocksDeleteBuiltinPipeline(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	rec := doReq(t, s, http.MethodDelete, "/api/blocks/pipeline/go", nil)
	if rec.Code != 400 {
		t.Fatalf("delete builtin status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot be deleted") {
		t.Fatalf("delete builtin body=%q, want 'cannot be deleted'", rec.Body.String())
	}
}

func TestBlocksList(t *testing.T) {
	s, _ := newBlocksTestServer(t)

	if rec := doReq(t, s, http.MethodPost, "/api/blocks/agent", []byte(testAgentBlock)); rec.Code != 200 {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Full list contains the custom block.
	rec := doReq(t, s, http.MethodGet, "/api/blocks", nil)
	if rec.Code != 200 {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var listed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	blocks, ok := listed["blocks"].([]interface{})
	if !ok || len(blocks) == 0 {
		t.Fatalf("expected blocks array: %+v", listed)
	}
	found := false
	for _, b := range blocks {
		entry, _ := b.(map[string]interface{})
		if entry["id"] == "my-agent" {
			found = true
			if entry["custom"] != true || entry["kind"] != "agent" {
				t.Fatalf("custom entry: %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("custom agent block missing from /api/blocks")
	}

	// kind=agent filters to agent blocks only.
	rec = doReq(t, s, http.MethodGet, "/api/blocks?kind=agent", nil)
	if rec.Code != 200 {
		t.Fatalf("list?kind=agent status=%d body=%s", rec.Code, rec.Body.String())
	}
	var filtered map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &filtered); err != nil {
		t.Fatal(err)
	}
	if filtered["kind"] != "agent" {
		t.Fatalf("kind=%v", filtered["kind"])
	}
	agentBlocks, ok := filtered["blocks"].([]interface{})
	if !ok || len(agentBlocks) == 0 {
		t.Fatalf("expected agent blocks: %+v", filtered)
	}
	for _, b := range agentBlocks {
		entry, _ := b.(map[string]interface{})
		if entry["kind"] != "agent" {
			t.Fatalf("non-agent entry in kind=agent list: %+v", entry)
		}
	}
}

const testPipelineBlock = `{
  "api_version": "blocks/v1",
  "kind": "pipeline",
  "id": "my-pipe",
  "name": "My Pipe",
  "description": "minimal test pipeline",
  "spec": {
    "version": 1,
    "order": ["init", "skills", "context", "explore", "plan", "split", "coord", "execute", "learn", "test", "memory", "done"],
    "groups": [
      {"id": "prepare", "label": "Prepare", "steps": ["init", "skills", "context", "explore"]},
      {"id": "design", "label": "Design", "steps": ["plan", "split"]},
      {"id": "build", "label": "Build", "steps": ["coord", "execute", "learn"]},
      {"id": "verify", "label": "Verify", "steps": ["test"]},
      {"id": "finish", "label": "Finish", "steps": ["memory", "done"]}
    ],
    "phases": {
      "init":    {"agent": "", "when": "always", "label": "Init"},
      "context": {"agent": "context", "when": "always", "label": "Context"},
      "explore": {"agent": "explorer", "when": "auto", "label": "Explore"},
      "plan":    {"agent": "planner", "when": "always", "label": "Plan"},
      "split":   {"agent": "splitter", "when": "always", "label": "Split"},
      "coord":   {"agent": "coordinator", "when": "always", "label": "Coord"},
      "execute": {"agent": "worker", "when": "always", "label": "Execute"},
      "learn":   {"agent": "memory", "when": "auto", "label": "Learn"},
      "test":    {"agent": "tester", "when": "always", "label": "Test"},
      "memory":  {"agent": "memory", "when": "always", "label": "Memory"},
      "done":    {"agent": "", "when": "always", "label": "Done"}
    },
    "execute": {"default_role": "worker", "reviewer": "reviewer", "corrector": "corrector", "max_waves": 2}
  }
}`

func TestBlocksPipelineCreateAndApplyPreset(t *testing.T) {
	s, h := newBlocksTestServer(t)

	rec := doReq(t, s, http.MethodPost, "/api/blocks/pipeline", []byte(testPipelineBlock))
	if rec.Code != 200 {
		t.Fatalf("create pipeline status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["ok"] != true {
		t.Fatalf("create ok=%v", created["ok"])
	}

	// Apply the preset end-to-end.
	rec = doReq(t, s, http.MethodPost, "/api/pipeline-presets/my-pipe/apply", nil)
	if rec.Code != 200 {
		t.Fatalf("apply preset status=%d body=%s", rec.Code, rec.Body.String())
	}
	var applied map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if applied["ok"] != true {
		t.Fatalf("apply ok=%v", applied["ok"])
	}
	cfg, _ := applied["config"].(map[string]interface{})
	if cfg["active_pipeline"] != "my-pipe" {
		t.Fatalf("config.active_pipeline=%v, want my-pipe (config=%+v)", cfg["active_pipeline"], cfg)
	}
	if h.Config.ActivePipeline != "my-pipe" {
		t.Fatalf("harness ActivePipeline=%q, want my-pipe", h.Config.ActivePipeline)
	}

	// Applying a missing preset → 400.
	rec = doReq(t, s, http.MethodPost, "/api/pipeline-presets/nope/apply", nil)
	if rec.Code != 400 {
		t.Fatalf("apply missing status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestBlocksCreateInvalid(t *testing.T) {
	s, _ := newBlocksTestServer(t)

	cases := []struct {
		name, kind, body string
	}{
		{
			name: "bad id",
			kind: "agent",
			body: `{
			  "api_version": "blocks/v1",
			  "kind": "agent",
			  "id": "Bad ID!",
			  "name": "Bad",
			  "spec": {"id": "Bad ID!", "title": "Bad", "system_prompt": "x"}
			}`,
		},
		{
			name: "agent spec id mismatch",
			kind: "agent",
			body: `{
			  "api_version": "blocks/v1",
			  "kind": "agent",
			  "id": "good-agent",
			  "spec": {"id": "other-agent", "title": "T", "system_prompt": "x"}
			}`,
		},
		{
			name: "quality missing spec",
			kind: "quality",
			body: `{
			  "api_version": "blocks/v1",
			  "kind": "quality",
			  "id": "no-spec",
			  "name": "No Spec"
			}`,
		},
		{
			name: "malformed json",
			kind: "agent",
			body: `{not json`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doReq(t, s, http.MethodPost, "/api/blocks/"+tc.kind, []byte(tc.body))
			if rec.Code != 400 {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestBlocksCreateUnknownKind(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	rec := doReq(t, s, http.MethodPost, "/api/blocks/bogus", []byte(testAgentBlock))
	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown kind") {
		t.Fatalf("body=%q, want 'unknown kind'", rec.Body.String())
	}
}
