// Command fakemodel is a standalone OpenAI-compatible model server whose
// answers are canned per specialist role.
//
// It is the same fake the end-to-end smoke test embeds (test/e2e/harness_smoke_test.go),
// lifted out so a REAL slmcode binary can be driven end to end with no model:
//
//	go run ./test/fakemodel -addr 127.0.0.1:8099 &
//	SLMCODE_ENDPOINT=http://127.0.0.1:8099/v1 slmcode run "…"
//
// Flags let a caller reproduce the failure modes `slmcode doctor` is supposed
// to explain: -mode=401, -mode=404, -mode=garbage, -mode=slow.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var packRoleRe = regexp.MustCompile(`Scoped context for role=([a-z0-9_-]+)`)

const (
	fakeModelID  = "fake-model"
	targetFile   = "calc.go"
	targetSource = "package calc\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Divide(a, b float64) (float64, error) {\n\tif b == 0 {\n\t\treturn 0, errDivZero\n\t}\n\treturn a / b, nil\n}\n"
)

type fakeModel struct {
	mu     sync.Mutex
	calls  int
	byRole map[string]int
	mode   string
	delay  time.Duration
	file   string
	source string
	logf   func(string, ...any)
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	switch f.mode {
	case "401":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`)
		return
	case "404":
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "404 page not found\n")
		return
	case "garbage":
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><head><title>nginx</title></head><body>it works</body></html>")
		return
	case "500":
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "internal server error")
		return
	}

	if strings.HasSuffix(r.URL.Path, "/models") {
		w.Header().Set("Content-Type", "application/json")
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
	n := f.calls
	f.mu.Unlock()
	if f.logf != nil {
		f.logf("call %d role=%s stream=%v tools=%d", n, role, req.Stream, len(req.Tools))
	}

	content, call := f.answerFor(role, len(req.Tools) > 0, sawToolResult)
	if req.Stream {
		writeStreamedCompletion(w, content, call)
		return
	}
	writeCompletion(w, content, call)
}

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
		for _, prefix := range []string{"go-", "python-", "react-", "ts-"} {
			r = strings.TrimPrefix(r, prefix)
		}
		return r
	}
	return "prose"
}

func (f *fakeModel) answerFor(role string, hasTools, sawToolResult bool) (string, map[string]any) {
	file, src := f.file, f.source
	switch role {
	case "explorer":
		return `{"summary":"tiny go module","relevant_files":["go.mod","` + file + `"],"key_symbols":[],"risks":[],"notes":""}`, nil
	case "docs":
		return `{"summary":"no docs yet","doc_files":[],"conventions":[],"apis":[],"gaps":[]}`, nil
	case "planner", "plan":
		return `{"summary":"add Divide to ` + file + `","steps":["Add a Divide function to ` + file + `"],"goals":[],"assumptions":[],"risks":[]}`, nil
	case "splitter", "tasks":
		return `{"tasks":[{"id":"T1","title":"add Divide to ` + file + `",` +
			`"description":"Add a Divide function to ` + file + `.",` +
			`"role":"worker","files":["` + file + `"],` +
			`"acceptance":"` + file + ` contains func Divide","depends_on":[]}]}`, nil
	case "worker", "deep", "corrector", "editor":
		if hasTools && !sawToolResult {
			return "", toolCall("ws_write", map[string]any{"path": file, "content": src})
		}
		return `{"status":"done","summary":"added Divide to ` + file +
			`","files_changed":["` + file + `"],"notes":""}`, nil
	case "reviewer", "reviewer-strict":
		return `{"approved":true,"score":92,"summary":"` + file + ` contains func Divide","issues":[]}`, nil
	case "tester", "verifier":
		if hasTools && !sawToolResult {
			return "", toolCall("ws_shell", map[string]any{"command": "cat " + file})
		}
		return "Observation: ws_shell `cat " + file + "` exit status 0\n" +
			`{"passed":true,"commands":["cat ` + file + `"],"summary":"` + file + ` present and correct","failures":[]}`, nil
	case "architect":
		return `{"approach":"one file","components":["` + file + `"],"interfaces":[],"risks":[],"non_goals":[]}`, nil
	}
	return "- The project is a tiny module with " + file + ".\n", nil
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
	if call != nil {
		fn, _ := call["function"].(map[string]any)
		emit(map[string]any{"id": "cmpl-1", "object": "chat.completion.chunk", "model": fakeModelID,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"index": 0, "id": call["id"], "type": "function",
					"function": map[string]any{"name": fn["name"], "arguments": fn["arguments"]},
				}},
			}}}})
		emit(map[string]any{"id": "cmpl-1", "object": "chat.completion.chunk", "model": fakeModelID,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
			"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 20, "total_tokens": 40}})
	} else {
		// Chunk the content so the CLI's token streaming path is exercised.
		for i, part := range chunks(content, 24) {
			delta := map[string]any{"content": part}
			if i == 0 {
				delta["role"] = "assistant"
			}
			emit(map[string]any{"id": "cmpl-1", "object": "chat.completion.chunk", "model": fakeModelID,
				"choices": []map[string]any{{"index": 0, "delta": delta}}})
		}
		emit(map[string]any{"id": "cmpl-1", "object": "chat.completion.chunk", "model": fakeModelID,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 20, "total_tokens": 40}})
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func chunks(s string, n int) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "listen address")
	mode := flag.String("mode", "ok", "ok | 401 | 404 | 500 | garbage")
	delay := flag.Duration("delay", 0, "artificial per-request delay")
	file := flag.String("file", targetFile, "file the fake worker writes")
	srcFile := flag.String("source-file", "", "read the fake worker's file content from this path")
	verbose := flag.Bool("v", false, "log each call")
	flag.Parse()

	src := targetSource
	if *srcFile != "" {
		b, err := os.ReadFile(*srcFile) // #nosec G304 -- test helper
		if err != nil {
			log.Fatalf("read -source-file: %v", err)
		}
		src = string(b)
	}
	f := &fakeModel{mode: *mode, delay: *delay, file: *file, source: src}
	if *verbose {
		f.logf = func(format string, a ...any) { log.Printf(format, a...) }
	}
	srv := &http.Server{Addr: *addr, Handler: f, ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("fakemodel listening on http://%s (mode=%s, model=%s)\n", *addr, *mode, fakeModelID)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
