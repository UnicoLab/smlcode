package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

// The regression net for the whole harness.
//
// Every other test in this tree exercises one layer. This one drives the real
// thing — harness → orchestrator (plan/split/execute/test phases) → loop
// (worker, reviewer, corrector) → pkg/workspace (ws_write, ws_shell) — against
// a fake OpenAI-compatible server, and asserts the four artifacts a finished
// run is supposed to leave behind:
//
//  1. the file the worker wrote is on disk, with the content it claimed;
//  2. the board completed, with no failed or escalated task;
//  3. an episode was appended to .slmcode/memory/episodes.jsonl;
//  4. a metrics row was appended to .slmcode/metrics/runs.jsonl, and it
//     carries real edit accounting rather than the zeros it used to.
//
// It is hermetic (HOME is redirected so the cross-project memory and bandit
// policy stores land in the temp tree, not the developer's real ~/.slmcode)
// and fast enough for CI — one run, well under a second of model time, because
// every "model call" is a map lookup.

// packRoleRe reads the role off the scoped-context header every specialist
// prompt carries.
var packRoleRe = regexp.MustCompile(`Scoped context for role=([a-z0-9_-]+)`)

// fakeModel is an OpenAI-compatible endpoint whose answers are canned per
// role. It speaks both the streaming and non-streaming Chat Completions
// shapes, and native tool calls, because the harness uses all three.
type fakeModel struct {
	mu     sync.Mutex
	calls  int
	byRole map[string]int
}

func (f *fakeModel) counts() (int, map[string]int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for k, v := range f.byRole {
		out[k] = v
	}
	return f.calls, out
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/models") {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": fakeModelID, "object": "model"}},
		})
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(raw, &req)

	var all strings.Builder
	sawToolResult := false
	for _, m := range req.Messages {
		all.WriteString(m.Content)
		all.WriteByte('\n')
		if m.Role == "tool" {
			sawToolResult = true
		}
	}
	system := ""
	if len(req.Messages) > 0 {
		system = req.Messages[0].Content
	}
	role := roleOf(system, all.String())

	f.mu.Lock()
	f.calls++
	if f.byRole == nil {
		f.byRole = map[string]int{}
	}
	f.byRole[role]++
	f.mu.Unlock()

	content, call := answerFor(role, len(req.Tools) > 0, sawToolResult)
	if req.Stream {
		writeStreamedCompletion(w, content, call)
		return
	}
	writeCompletion(w, content, call)
}

// roleOf works out which specialist is asking.
//
// The system prompt states the role's own output contract, which is the
// precise signal; the pack header is the fallback for prompts assembled
// without one (pipeline slots, language packs).
func roleOf(system, all string) string {
	switch {
	case strings.Contains(system, `"passed"`):
		return "tester"
	case strings.Contains(system, `{"approved"`), strings.Contains(system, "never approved"):
		return "reviewer"
	case strings.Contains(system, `"tasks":[{"id":"T1"`):
		return "splitter"
	case strings.Contains(system, `"steps":["step one"`):
		return "planner"
	case strings.Contains(system, `"relevant_files"`):
		return "explorer"
	case strings.Contains(system, `"doc_files"`):
		return "docs"
	case strings.Contains(system, `"files_changed"`):
		return "worker"
	}
	if m := packRoleRe.FindStringSubmatch(all); m != nil {
		r := m[1]
		// Language packs register go-worker / python-tester / … — the same
		// contract with a language-specific prompt.
		for _, prefix := range []string{"go-", "python-", "react-", "ts-"} {
			r = strings.TrimPrefix(r, prefix)
		}
		return r
	}
	// Prose roles (CONTEXT.md, MEMORY.md distillation, reflection commentary)
	// have no JSON contract at all.
	return "prose"
}

const (
	fakeModelID  = "fake-model"
	targetFile   = "src/hello.go"
	targetSource = "package main\n\nfunc Hello() string { return \"hi\" }\n"
)

// answerFor is the canned response for one role. hasTools reports that the
// request offered the workspace tools; sawToolResult that a tool result is
// already in the transcript, which is how a two-turn ReAct agent knows to stop
// calling tools and finalize.
func answerFor(role string, hasTools, sawToolResult bool) (content string, call map[string]any) {
	switch role {
	case "explorer":
		return `{"summary":"tiny go module","relevant_files":["go.mod"],"key_symbols":[],"risks":[],"notes":""}`, nil
	case "docs":
		return `{"summary":"no docs yet","doc_files":[],"conventions":[],"apis":[],"gaps":[]}`, nil
	case "planner", "plan":
		return `{"summary":"create ` + targetFile + `","steps":["Create ` + targetFile +
			` with func Hello"],"goals":[],"assumptions":[],"risks":[]}`, nil
	case "splitter", "tasks":
		return `{"tasks":[{"id":"T1","title":"create ` + targetFile + `",` +
			`"description":"Create ` + targetFile + ` containing package main and func Hello() string.",` +
			`"role":"worker","files":["` + targetFile + `"],` +
			`"acceptance":"` + targetFile + ` exists with func Hello","depends_on":[]}]}`, nil
	case "worker", "deep", "corrector", "editor":
		if hasTools && !sawToolResult {
			return "", toolCall("ws_write", map[string]any{
				"path": targetFile, "content": targetSource,
			})
		}
		return `{"status":"done","summary":"created ` + targetFile +
			`","files_changed":["` + targetFile + `"],"notes":""}`, nil
	case "reviewer", "reviewer-strict":
		return `{"approved":true,"score":92,"summary":"` + targetFile +
			` contains func Hello","issues":[]}`, nil
	case "tester", "verifier":
		if hasTools && !sawToolResult {
			return "", toolCall("ws_shell", map[string]any{"command": "cat " + targetFile})
		}
		// The finalize echoes the ws_shell observation, the way a small model
		// does after a tool turn — plan.ParseTesterJSON refuses passed:true
		// without an execution trace beside the JSON.
		return "Observation: ws_shell `cat " + targetFile + "` exit status 0\n" +
			`{"passed":true,"commands":["cat ` + targetFile +
			`"],"summary":"` + targetFile + ` present and correct","failures":[]}`, nil
	case "architect":
		return `{"approach":"one file","components":["` + targetFile +
			`"],"interfaces":[],"risks":[],"non_goals":[]}`, nil
	}
	return "- The project is a tiny Go module with " + targetFile + ".\n", nil
}

func toolCall(name string, args map[string]any) map[string]any {
	b, _ := json.Marshal(args)
	return map[string]any{
		"id": "call_1", "type": "function",
		"function": map[string]any{"name": name, "arguments": string(b)},
	}
}

func writeCompletion(w http.ResponseWriter, content string, call map[string]any) {
	msg := map[string]any{"role": "assistant", "content": content}
	finish := "stop"
	if call != nil {
		msg["content"] = ""
		msg["tool_calls"] = []any{call}
		finish = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "cmpl-1", "object": "chat.completion", "model": fakeModelID,
		"choices": []map[string]any{{"index": 0, "finish_reason": finish, "message": msg}},
		"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 20, "total_tokens": 40},
	})
}

func writeStreamedCompletion(w http.ResponseWriter, content string, call map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	emit := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	delta := map[string]any{"role": "assistant"}
	finish := "stop"
	if call != nil {
		fn, _ := call["function"].(map[string]any)
		delta["tool_calls"] = []any{map[string]any{
			"index": 0, "id": call["id"], "type": "function",
			"function": map[string]any{"name": fn["name"], "arguments": fn["arguments"]},
		}}
		finish = "tool_calls"
	} else {
		delta["content"] = content
	}
	emit(map[string]any{"id": "cmpl-1", "object": "chat.completion.chunk", "model": fakeModelID,
		"choices": []map[string]any{{"index": 0, "delta": delta}}})
	emit(map[string]any{"id": "cmpl-1", "object": "chat.completion.chunk", "model": fakeModelID,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 20, "total_tokens": 40}})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func TestHarnessEndToEndAgainstAFakeModel(t *testing.T) {
	// Hermetic: the cross-project memory, repair rules and bandit policy all
	// live under the user's home dir, so redirect it. Without this the test
	// reads (and writes) the developer's real ~/.slmcode.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// The build shim exports GOFLAGS=-modfile=go.local.mod; a child process
	// launched inside the fixture module would inherit it and fail.
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module demo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	model := &fakeModel{}
	server := httptest.NewServer(model)
	defer server.Close()

	cfg := config.Default(root)
	cfg.Provider = "openai"
	cfg.Endpoint = server.URL + "/v1"
	cfg.Model = fakeModelID
	cfg.APIKey = "test-key"
	// Prompt-only JSON: no capability probing against a server that would
	// answer yes to everything.
	cfg.StructuredDecoding = "off"
	cfg.DynamicPipeline = false
	// Every human-in-the-loop gate off — this test has no human.
	cfg.ClarifyMode = "off"
	cfg.PlanApprove = "auto"
	cfg.ContinueAsk = "off"
	cfg.EscalateAsk = "off"
	// No external process: the run must not depend on a Go toolchain being
	// usable inside a temp module.
	cfg.QAGate = false
	cfg.PostWorkerSmoke = false
	cfg.RequireSmoke = false
	cfg.ScopeJudge = false
	cfg.PlaceholderPass = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 1
	cfg.MaxRetries = 1
	cfg.TaskTimeout = 30 * time.Second
	cfg.Normalize()

	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatal(err)
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatal(err)
	}
	// harness.New reloads config from disk; run against the config above.
	h.Config = cfg
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		t.Fatalf("install orchestrator: %v", cerr)
	}
	defer func() { _ = h.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := h.Run(ctx, "Create "+targetFile+" with a Hello function")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	calls, byRole := model.counts()
	t.Logf("%d model calls: %v", calls, byRole)

	// 1. The worker's ws_write reached the disk.
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(targetFile))) // #nosec G304 -- temp fixture
	if err != nil {
		t.Fatalf("%s was never written: %v", targetFile, err)
	}
	if string(got) != targetSource {
		t.Fatalf("%s content = %q, want %q", targetFile, got, targetSource)
	}

	// 2. The board finished: every task done, nothing failed or escalated.
	if !res.Success {
		t.Errorf("run did not succeed: %s", res.Summary)
	}
	if res.FailedTasks != 0 {
		t.Errorf("failed tasks = %d: %s", res.FailedTasks, res.Summary)
	}
	if len(res.Board.Tasks) == 0 {
		t.Fatal("the board has no tasks — plan/split never produced any")
	}
	for _, task := range res.Board.Tasks {
		if task.Column != plan.ColDone {
			t.Errorf("task %s ended in %q, want %q (%s)", task.ID, task.Column, plan.ColDone, task.Error)
		}
	}

	// 3. The run was recorded as an episode. This is the half of the
	//    self-improvement engine that persists between runs.
	episodes := readJSONL(t, filepath.Join(root, ".slmcode", "memory", "episodes.jsonl"))
	if len(episodes) == 0 {
		t.Fatal("no episode was recorded in .slmcode/memory/episodes.jsonl")
	}

	// 4. A metrics row was written, and it carries REAL edit accounting.
	//    Every field below read zero before pkg/orchestrator started copying
	//    the inner loop's ledger into the run report.
	rows := readJSONL(t, filepath.Join(root, ".slmcode", "metrics", "runs.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("metrics rows = %d, want exactly 1", len(rows))
	}
	row := rows[0]
	if row["run_id"] != res.ID {
		t.Errorf("metrics run_id = %v, want %s", row["run_id"], res.ID)
	}
	if n, _ := row["tasks_passed"].(float64); int(n) != len(res.Board.Tasks) {
		t.Errorf("metrics tasks_passed = %v, want %d", row["tasks_passed"], len(res.Board.Tasks))
	}
	if n, _ := row["edits_attempted"].(float64); n < 1 {
		t.Errorf("metrics edits_attempted = %v — edit accounting never reached the run report", row["edits_attempted"])
	}
	if n, _ := row["edits_applied"].(float64); n < 1 {
		t.Errorf("metrics edits_applied = %v, want >= 1", row["edits_applied"])
	}
	if s, _ := row["edit_format"].(string); strings.TrimSpace(s) == "" {
		t.Error("metrics edit_format is empty — the bandit arm the run used was not recorded")
	}
	if n, _ := row["tool_calls"].(float64); n < 1 {
		t.Errorf("metrics tool_calls = %v — the tool layer never reported to working memory", row["tool_calls"])
	}
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- temp fixture
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("%s: unparsable line %q: %v", path, line, err)
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}
