package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFakeModel_ConcurrentHTTPStress(t *testing.T) {
	fm := &fakeModel{
		file:        targetFile,
		source:      targetSource,
		rejectFirst: 20,
		badFirst:    20,
	}
	server := httptest.NewServer(fm)
	defer server.Close()

	client := server.Client()

	const goroutines = 25
	const iterationsPerGoroutine = 20

	testCases := []struct {
		name       string
		rolePrompt string
		stream     bool
		tools      bool
	}{
		{
			name:       "teamwork-spec",
			rolePrompt: "You are the teamwork_spec interviewer. ScopePRD needs_user",
			stream:     false,
		},
		{
			name:       "teamwork-spec-stream",
			rolePrompt: "teamwork-spec requirements interview",
			stream:     true,
		},
		{
			name:       "teamwork-plan",
			rolePrompt: "Teamwork architect and planner. Produce CONTRACT.md and teamwork_plan.",
			stream:     false,
		},
		{
			name:       "teamwork-worker-tool",
			rolePrompt: "Implement ONE atomic task for Teamwork. teamwork_worker",
			stream:     false,
			tools:      true,
		},
		{
			name:       "teamwork-review",
			rolePrompt: "Adversarial reviewer for Teamwork deliverables. teamwork_review challenge_scenarios",
			stream:     false,
		},
		{
			name:       "teamwork-review-stream",
			rolePrompt: "teamwork-review adversarial_reviewer",
			stream:     true,
		},
		{
			name:       "legacy-reviewer",
			rolePrompt: `Evaluate with {"approved": true} or never approved`,
			stream:     false,
		},
		{
			name:       "escalate",
			rolePrompt: `"action":"retry|re_scope|abort|mark_done"`,
			stream:     false,
		},
	}

	var wg sync.WaitGroup
	totalRequests := goroutines * iterationsPerGoroutine

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterationsPerGoroutine; j++ {
				tc := testCases[(workerID+j)%len(testCases)]

				reqBody := map[string]any{
					"stream": tc.stream,
					"messages": []map[string]any{
						{
							"role":    "system",
							"content": tc.rolePrompt,
						},
						{
							"role":    "user",
							"content": fmt.Sprintf("Worker %d Iteration %d task query", workerID, j),
						},
					},
				}
				if tc.tools {
					reqBody["tools"] = []map[string]any{
						{
							"type": "function",
							"function": map[string]any{
								"name": "ws_read",
							},
						},
					}
				}

				payload, err := json.Marshal(reqBody)
				if err != nil {
					t.Errorf("worker %d: json marshal error: %v", workerID, err)
					return
				}

				resp, err := client.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(payload))
				if err != nil {
					t.Errorf("worker %d: post failed: %v", workerID, err)
					return
				}

				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					t.Errorf("worker %d: read body failed: %v", workerID, err)
					return
				}

				if resp.StatusCode != http.StatusOK {
					t.Errorf("worker %d: unexpected status code: %d, body: %s", workerID, resp.StatusCode, string(body))
					return
				}
				if len(body) == 0 {
					t.Errorf("worker %d: empty response body", workerID)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	fm.mu.Lock()
	defer fm.mu.Unlock()

	if fm.calls != totalRequests {
		t.Fatalf("calls mismatch: expected %d, got %d", totalRequests, fm.calls)
	}

	sumByRole := 0
	for role, count := range fm.byRole {
		sumByRole += count
		if count <= 0 {
			t.Errorf("role %q has non-positive count %d", role, count)
		}
	}
	if sumByRole != totalRequests {
		t.Fatalf("sum(byRole) mismatch: expected %d, got %d", totalRequests, sumByRole)
	}
}

type completionResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func parseCompletionContent(t *testing.T, raw []byte) string {
	t.Helper()
	var res completionResp
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("failed to unmarshal completion: %v, raw=%s", err, string(raw))
	}
	if len(res.Choices) == 0 {
		t.Fatalf("empty choices in completion: %s", string(raw))
	}
	return res.Choices[0].Message.Content
}

func TestFakeModel_ConcurrentRoleOfAndFlagRouting(t *testing.T) {
	testMatrix := []struct {
		name           string
		teamworkReject bool
		teamworkVeto   bool
		teamworkAsk    bool
		expectedReview string
		expectedSpec   bool
	}{
		{
			name:           "veto",
			teamworkVeto:   true,
			expectedReview: `"verdict":"veto"`,
			expectedSpec:   false,
		},
		{
			name:           "reject",
			teamworkReject: true,
			expectedReview: `"verdict":"reject"`,
			expectedSpec:   false,
		},
		{
			name:           "ask",
			teamworkAsk:    true,
			expectedReview: `"verdict":"approve"`,
			expectedSpec:   true,
		},
	}

	for _, tc := range testMatrix {
		t.Run(tc.name, func(t *testing.T) {
			fm := &fakeModel{
				file:           targetFile,
				source:         targetSource,
				teamworkReject: tc.teamworkReject,
				teamworkVeto:   tc.teamworkVeto,
				teamworkAsk:    tc.teamworkAsk,
			}
			server := httptest.NewServer(fm)
			defer server.Close()
			client := server.Client()

			var wg sync.WaitGroup
			const workers = 10
			const iters = 10

			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for i := 0; i < iters; i++ {
						// Review check
						revReq := map[string]any{
							"stream": false,
							"messages": []map[string]any{
								{"role": "system", "content": "Adversarial reviewer for Teamwork deliverables teamwork_review challenge_scenarios"},
								{"role": "user", "content": "Review check"},
							},
						}
						b, _ := json.Marshal(revReq)
						resp, err := client.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(b))
						if err != nil {
							t.Errorf("worker %d: review post failed: %v", id, err)
							return
						}
						out, _ := io.ReadAll(resp.Body)
						_ = resp.Body.Close()

						content := parseCompletionContent(t, out)
						if !strings.Contains(content, tc.expectedReview) {
							t.Errorf("worker %d: review content missing expected %s: %s", id, tc.expectedReview, content)
						}

						// Spec check
						specReq := map[string]any{
							"stream": false,
							"messages": []map[string]any{
								{"role": "system", "content": "Teamwork specification and requirements ScopePRD teamwork_spec"},
								{"role": "user", "content": "Spec check"},
							},
						}
						b, _ = json.Marshal(specReq)
						resp, err = client.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(b))
						if err != nil {
							t.Errorf("worker %d: spec post failed: %v", id, err)
							return
						}
						out, _ = io.ReadAll(resp.Body)
						_ = resp.Body.Close()

						content = parseCompletionContent(t, out)
						expectedNeedsUser := fmt.Sprintf(`"needs_user":%t`, tc.expectedSpec)
						if !strings.Contains(content, expectedNeedsUser) {
							t.Errorf("worker %d: spec content missing %s: %s", id, expectedNeedsUser, content)
						}
					}
				}(w)
			}
			wg.Wait()
		})
	}
}

func TestFakeModel_ConcurrentRejectFirstAndBadFirstCounters(t *testing.T) {
	const threshold = 15
	fm := &fakeModel{
		file:        targetFile,
		source:      targetSource,
		rejectFirst: threshold,
		badFirst:    threshold,
	}
	server := httptest.NewServer(fm)
	defer server.Close()
	client := server.Client()

	var wg sync.WaitGroup
	const totalCallers = 40

	var rejectedCount int64
	var approvedCount int64
	var badWriteCount int64
	var goodWriteCount int64

	for i := 0; i < totalCallers; i++ {
		wg.Add(2)

		// Review worker
		go func(id int) {
			defer wg.Done()
			req := map[string]any{
				"stream": false,
				"messages": []map[string]any{
					{"role": "system", "content": "adversarial_reviewer teamwork_review"},
					{"role": "user", "content": fmt.Sprintf("review req %d", id)},
				},
			}
			b, _ := json.Marshal(req)
			resp, err := client.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(b))
			if err != nil {
				t.Errorf("review failed: %v", err)
				return
			}
			out, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			content := parseCompletionContent(t, out)
			if strings.Contains(content, `"verdict":"reject"`) {
				atomic.AddInt64(&rejectedCount, 1)
			} else if strings.Contains(content, `"verdict":"approve"`) {
				atomic.AddInt64(&approvedCount, 1)
			}
		}(i)

		// Worker write worker (simulates second turn after ws_read)
		go func(id int) {
			defer wg.Done()
			req := map[string]any{
				"stream": false,
				"messages": []map[string]any{
					{"role": "system", "content": "Implement ONE atomic task for Teamwork. teamwork_worker"},
					{"role": "tool", "content": "package calc\n// read calc.go"},
				},
				"tools": []map[string]any{
					{"type": "function", "function": map[string]any{"name": "ws_write"}},
				},
			}
			b, _ := json.Marshal(req)
			resp, err := client.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(b))
			if err != nil {
				t.Errorf("worker write post failed: %v", err)
				return
			}
			out, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			if bytes.Contains(out, []byte("func Divide( {")) {
				atomic.AddInt64(&badWriteCount, 1)
			} else if bytes.Contains(out, []byte("func Divide(a, b float64)")) {
				atomic.AddInt64(&goodWriteCount, 1)
			}
		}(i)
	}

	wg.Wait()

	fm.mu.Lock()
	defer fm.mu.Unlock()

	if fm.reviews != totalCallers {
		t.Fatalf("expected %d reviews, got %d", totalCallers, fm.reviews)
	}
	if rejectedCount != threshold {
		t.Errorf("expected exactly %d rejected reviews, got %d", threshold, rejectedCount)
	}
	if approvedCount != int64(totalCallers-threshold) {
		t.Errorf("expected %d approved reviews, got %d", totalCallers-threshold, approvedCount)
	}

	if fm.writes != totalCallers {
		t.Fatalf("expected %d writes, got %d", totalCallers, fm.writes)
	}
	if badWriteCount != threshold {
		t.Errorf("expected exactly %d bad writes, got %d", threshold, badWriteCount)
	}
	if goodWriteCount != int64(totalCallers-threshold) {
		t.Errorf("expected %d good writes, got %d", totalCallers-threshold, goodWriteCount)
	}
}

func TestFakeModel_ConcurrentTimeoutAndCancellation(t *testing.T) {
	fm := &fakeModel{
		file:  targetFile,
		delay: 100 * time.Millisecond,
	}
	server := httptest.NewServer(fm)
	defer server.Close()

	var wg sync.WaitGroup
	const concurrent = 15

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"messages":[{"role":"user","content":"hi"}]}`)))
			req.Header.Set("Content-Type", "application/json")

			resp, err := server.Client().Do(req)
			if err == nil {
				_ = resp.Body.Close()
				t.Errorf("expected timeout error for worker %d, got nil", id)
			}
		}(i)
	}
	wg.Wait()
}

func TestFakeModel_ModelsEndpointConcurrent(t *testing.T) {
	fm := &fakeModel{file: targetFile}
	server := httptest.NewServer(fm)
	defer server.Close()
	client := server.Client()

	var wg sync.WaitGroup
	const workers = 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resp, err := client.Get(server.URL + "/v1/models")
			if err != nil {
				t.Errorf("worker %d: models get failed: %v", id, err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("worker %d: models status %d", id, resp.StatusCode)
			}
			if !bytes.Contains(body, []byte(fakeModelID)) {
				t.Errorf("worker %d: models missing %s: %s", id, fakeModelID, string(body))
			}
		}(i)
	}
	wg.Wait()
}
